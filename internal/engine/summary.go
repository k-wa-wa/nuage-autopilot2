package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/config"
	"nuage-autopilot2/internal/prompt"
	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/summary"
)

// このファイルは「人間がやるべきこと」を定期的にまとめるサマリ生成である。
//
// パイプラインの状態遷移には一切関与しない。Status を動かさず、GitHub にも書き込まず、
// 結果を DB に置いて参照 UI に見せるだけである。失敗しても Blocked にはせず、
// 次回の実行を待つ（レーンの進行を止めないため）。
//
// それでもエージェントの起動は agent-worker の直列キューに載せる。実装フェーズと
// 同時に走らせると、ワークツリーと API レート、そして人間の CLI 課金枠を取り合うためである。

// summaryMaxItems はプロンプトに載せるカードの上限。
//
// 未終端のカードが増えてもプロンプトが線形に膨らまないようにする。
// 人間の関与点（Inbox / In Review / Blocked）を優先して残す。
const summaryMaxItems = 60

// summaryRawLimit は解釈できなかった出力を保存する際の上限（バイト）。
const summaryRawLimit = 32 * 1024

// scheduleSummaries は cron 式に従ってサマリ生成のジョブを投入する。
//
// summary.schedule が空なら何もしない。
func (e *Engine) scheduleSummaries(ctx context.Context) {
	if e.summaryCron == nil {
		e.log.Debug("summary.schedule が空のためサマリ生成は行いません")
		return
	}
	for {
		next := e.summaryCron.Next(time.Now())
		if next.IsZero() {
			e.log.Error("サマリ生成の次回実行時刻を決められません", "schedule", e.summaryCron.String())
			return
		}
		e.setSummaryNext(next)
		e.log.Info("次回のサマリ生成を予約しました", "at", next.Format(time.RFC3339))

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		e.enqueue(ctx, Job{Phase: PhaseSummarize})
	}
}

func (e *Engine) setSummaryNext(t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.summaryNext = t
}

// RunSummaryNow はサマリを 1 回その場で生成する。CLI から手動で叩くためのもの。
func (e *Engine) RunSummaryNow(ctx context.Context) (*store.Summary, error) {
	if err := e.runSummary(ctx, Job{Phase: PhaseSummarize}); err != nil {
		return nil, err
	}
	sums, err := e.st.ListSummaries(1)
	if err != nil || len(sums) == 0 {
		return nil, err
	}
	return sums[0], nil
}

// runSummary はサマリ生成エージェントを 1 回起動し、結果を保存する。
func (e *Engine) runSummary(ctx context.Context, j Job) error {
	// runs には repo 無し・issue 0 の行として記録する。参照 UI は
	// これをカードと結び付けないが、実行ログには /api/run から辿れる。
	runID, err := e.st.StartRun("", 0, PhaseSummarize)
	if err != nil {
		e.log.Error("実行ログの記録に失敗", "err", err)
	}
	e.beginActive(j, runID)
	defer e.endActive()

	res, runErr := e.executeSummary(ctx)
	outcome := "ok"
	if runErr != nil {
		outcome = "error: " + runErr.Error()
	}
	if runID != 0 {
		if err := e.st.EndRun(runID, outcome); err != nil {
			e.log.Error("実行ログの更新に失敗", "err", err)
		}
	}
	if runErr != nil {
		// パイプラインには影響しないので、記録だけ残して次回を待つ。
		e.log.Error("サマリ生成に失敗しました", "err", runErr)
		return runErr
	}

	sum := &store.Summary{RunID: runID}
	report, perr := summary.Parse(res.Stdout)
	if perr != nil {
		// 生成物を捨てないよう、解釈できなくても出力そのものは残す。
		e.log.Warn("サマリの出力を解釈できませんでした", "err", perr)
		sum.Raw = res.Tail(summaryRawLimit)
	} else {
		b, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("サマリの保存形式への変換に失敗: %w", err)
		}
		sum.Payload = string(b)
	}
	if _, err := e.st.AddSummary(sum); err != nil {
		return fmt.Errorf("サマリの保存に失敗: %w", err)
	}
	if err := e.st.TrimSummaries(e.cfg.Summary.Keep); err != nil {
		e.log.Warn("古いサマリの削除に失敗", "err", err)
	}
	if report != nil {
		e.log.Info("サマリを生成しました", "todos", len(report.Todos), "headline", report.Headline)
	}
	return nil
}

// executeSummary はプロンプトを組み立ててエージェントを起動する。
//
// 対象が特定のリポジトリではないため、ワークツリーの準備（fetch / reset）は行わず、
// ワークスペースの直上で動かす。読み取り専用なので clone の状態に依存しない。
func (e *Engine) executeSummary(ctx context.Context) (*agent.Result, error) {
	if err := os.MkdirAll(e.cfg.Workspace, 0o755); err != nil {
		return nil, fmt.Errorf("ワークスペースを用意できません: %w", err)
	}
	pc, err := e.summaryContext()
	if err != nil {
		return nil, err
	}
	spec := e.cfg.AgentFor(config.AgentSummarize).Spec()
	e.log.Info("エージェントを起動します",
		"phase", PhaseSummarize,
		"command", spec.ResolvedCommand(),
		"adapter", spec.Adapter().Name(),
		"timeout", spec.Timeout,
		"items", len(pc.Items))
	return e.runner.Run(ctx, spec, PhaseSummarize, e.cfg.Workspace, prompt.Summarize(pc))
}

// summaryContext はローカル状態からプロンプトの入力を組み立てる。
func (e *Engine) summaryContext() (prompt.SummaryContext, error) {
	items, err := e.st.List()
	if err != nil {
		return prompt.SummaryContext{}, err
	}
	latest, err := e.st.LatestRuns()
	if err != nil {
		return prompt.SummaryContext{}, err
	}
	runByItem := make(map[string]*store.Run, len(latest))
	for _, r := range latest {
		runByItem[key(r.Repo, r.IssueNumber)] = r
	}

	repos := make([]string, 0, len(e.cfg.Repos))
	for _, r := range e.cfg.Repos {
		repos = append(repos, r.String())
	}

	var live []*store.Item
	for _, it := range items {
		if it.Terminal {
			continue
		}
		live = append(live, it)
	}
	// 上限で切る際に人間の関与点が落ちないよう、優先度の高いレーンから並べる。
	sort.SliceStable(live, func(i, j int) bool {
		ri, rj := e.laneRank(live[i].LastStatus), e.laneRank(live[j].LastStatus)
		if ri != rj {
			return ri < rj
		}
		// 同じレーンなら滞留の長い順。
		return live[i].UpdatedAt.Before(live[j].UpdatedAt)
	})
	truncated := false
	if len(live) > summaryMaxItems {
		live = live[:summaryMaxItems]
		truncated = true
	}

	out := make([]prompt.SummaryItem, 0, len(live))
	for _, it := range live {
		si := prompt.SummaryItem{
			Repo:       it.Repo,
			Issue:      it.IssueNumber,
			Status:     it.LastStatus,
			PRNumber:   it.PRNumber,
			RetryCount: it.RetryCount,
			UpdatedAt:  it.UpdatedAt,
		}
		if run := runByItem[key(it.Repo, it.IssueNumber)]; run != nil {
			si.LastRunPhase = run.Phase
			si.LastRunResult = run.Result
			if si.LastRunResult == "" {
				si.LastRunResult = "実行中"
			}
		}
		out = append(out, si)
	}

	s := e.cfg.Statuses
	return prompt.SummaryContext{
		GeneratedAt:   time.Now(),
		ProjectOwner:  e.cfg.Project.Owner,
		ProjectNumber: e.cfg.Project.Number,
		Repos:         repos,
		Statuses: prompt.SummaryStatuses{
			Inbox: s.Inbox, Ready: s.Ready, InReview: s.InReview, Blocked: s.Blocked,
		},
		Items:      out,
		Truncated:  truncated,
		MaxRetries: e.cfg.Limits.MaxRetries,
	}, nil
}

// laneRank はサマリに載せる優先順位を返す。小さいほど優先する。
//
// 設定に無いレーン名（Project 側だけで増やした選択肢など）は最後に回す。
func (e *Engine) laneRank(status string) int {
	s := e.cfg.Statuses
	for i, name := range []string{s.Blocked, s.InReview, s.Inbox, s.Verifying, s.InProgress, s.Ready} {
		if name == status {
			return i
		}
	}
	return 99
}
