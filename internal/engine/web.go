package engine

import (
	"context"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/config"
	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/web"
)

// このファイルは参照 UI へ状態を渡すためのアダプタである。
//
// パイプラインの判断には一切関与しない。Engine が web.Source を満たすための
// 読み取り専用メソッドと、実行中ジョブの記録だけを置く。
//
// UI を別プロセスにせず run に同居させたのは、キューの滞留・in-flight セット・
// 実行中フェーズが**メモリ上にしか無い**ためである。DB を読むだけの別プロセスに
// すると、実行中かどうかを runs.ended_at が空かどうかで推定するしかなく、
// ワーカーが異常終了して取り残された行を永久に「実行中」と誤表示してしまう。

// activeRun は agent-worker が今処理しているジョブ。e.mu で保護する。
type activeRun struct {
	runID          int64
	phase          string
	repo           string
	issue          int
	startedAt      time.Time
	agentStartedAt time.Time
	logPath        string
}

// beginActive はジョブの処理開始を記録する。
//
// エージェント起動は agent-worker 1 本に直列化されているので、
// 実行中のジョブは常に高々 1 件である。
func (e *Engine) beginActive(j Job, runID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active = &activeRun{
		runID:     runID,
		phase:     j.Phase,
		repo:      j.Repo,
		issue:     j.Issue,
		startedAt: time.Now(),
	}
}

// endActive はジョブの処理終了を記録する。
func (e *Engine) endActive() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active = nil
}

// onAgentStart は agent.Runner がプロセスを起動する直前に呼ぶフック。
//
// ログのパスはここで初めて確定する。参照 UI が実行中のプロンプトを読めるよう、
// メモリ上の実行中ジョブと runs の行の両方に書き戻す。
func (e *Engine) onAgentStart(info agent.RunInfo) {
	e.mu.Lock()
	runID := int64(0)
	if e.active != nil {
		e.active.logPath = info.LogPath
		e.active.agentStartedAt = info.StartedAt
		runID = e.active.runID
	}
	e.mu.Unlock()

	if runID == 0 || info.LogPath == "" {
		return
	}
	// 実行が終わった後もログを辿れるよう永続化する。失敗しても実行は続ける。
	if err := e.st.SetRunLog(runID, info.LogPath); err != nil {
		e.log.Warn("実行ログのパス記録に失敗", "run", runID, "err", err)
	}
}

// serveWeb は参照 UI を待ち受ける。web.addr が空なら何もしない。
func (e *Engine) serveWeb(ctx context.Context) {
	addr := e.cfg.Web.Listen()
	if addr == "" {
		e.log.Debug("web.addr が空のため参照 UI は起動しません")
		return
	}
	if err := web.New(e, e.log).Serve(ctx, addr); err != nil {
		e.log.Error("参照 UI が停止しました", "err", err)
	}
}

// 以降は web.Source の実装。

// Meta は画面の見出しとレーンの並びに使う情報を返す。
func (e *Engine) Meta() web.Meta {
	repos := make([]string, 0, len(e.cfg.Repos))
	for _, r := range e.cfg.Repos {
		repos = append(repos, r.String())
	}
	agents := make([]web.AgentInfo, 0, len(config.AgentUses))
	for _, use := range config.AgentUses {
		a := e.cfg.AgentFor(use)
		agents = append(agents, web.AgentInfo{
			Use:     use,
			Command: a.Spec().ResolvedCommand(),
			Model:   a.Model,
			Timeout: a.Timeout.String(),
		})
	}
	return web.Meta{
		Login:         e.client.Login,
		ProjectOwner:  e.cfg.Project.Owner,
		ProjectNumber: e.cfg.Project.Number,
		Repos:         repos,
		Statuses:      e.laneOrder(),
		Agents:        agents,
	}
}

// laneOrder はボード上のレーンの並び順を返す。
func (e *Engine) laneOrder() []string {
	s := e.cfg.Statuses
	return []string{s.Inbox, s.Ready, s.InProgress, s.Verifying, s.InReview, s.Blocked, s.Done}
}

// LatestRuns は Issue ごとの最新の実行を返す。
func (e *Engine) LatestRuns() ([]*store.Run, error) { return e.st.LatestRuns() }

// IssueRuns は 1 件の Issue の実行履歴を新しい順に返す。
func (e *Engine) IssueRuns(repo string, issue int, limit int) ([]*store.Run, error) {
	return e.st.ListRuns(repo, issue, limit)
}

// GetRun は実行を 1 件返す。
func (e *Engine) GetRun(id int64) (*store.Run, error) { return e.st.GetRun(id) }

// Active は実行中のジョブを返す。無ければ nil。
func (e *Engine) Active() *web.Active {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active == nil {
		return nil
	}
	a := e.active
	return &web.Active{
		RunID:          a.runID,
		Phase:          a.phase,
		Repo:           a.repo,
		Issue:          a.issue,
		StartedAt:      a.startedAt,
		AgentStartedAt: a.agentStartedAt,
		LogPath:        a.logPath,
	}
}

// QueueDepth は投入済みでまだ処理されていないジョブの数を返す。
func (e *Engine) QueueDepth() int { return len(e.jobs) }

// LogDir はエージェントのログの置き場を返す。
func (e *Engine) LogDir() string { return e.runner.LogDir() }
