package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/config"
	"nuage-autopilot2/internal/gh"
	"nuage-autopilot2/internal/prompt"
	"nuage-autopilot2/internal/store"
)

// ciFailureHint は CI 失敗時に実装エージェントへ渡す調査の手がかり。
const ciFailureHint = "CI が失敗しました。`gh pr checks` および `gh run view --log-failed` で失敗内容を確認し、原因を修正してください。"

// PR 発見のリトライ設定。
//
// 実装エージェントが gh pr create を終えた直後は、Issue の timeline に
// CROSS_REFERENCED_EVENT が反映されるまで数秒のラグがある。一度きりの問い合わせだと
// nil が返り、PR は正しく作られているのに誤って Blocked へ落ちてしまう。
const (
	linkedPRAttempts = 3
	linkedPRBackoff  = 2 * time.Second
)

// retryFindPR は PR が見つかるまで backoff を挟んで最大 attempts 回試行する。
//
// エラーは記録しつつ次の試行に進む（一時的な API エラーもラグと同様に吸収するため）。
// 全試行で見つからなければ (nil, 最後のエラー) を返す。エラーが一度も起きていなければ
// (nil, nil) となり、呼び出し側は「本当に PR が無い」と判断できる。
func retryFindPR(ctx context.Context, attempts int, backoff time.Duration,
	find func(context.Context) (*gh.PullRequest, error),
) (*gh.PullRequest, error) {
	var lastErr error
	for i := range attempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		pr, err := find(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		if pr != nil {
			return pr, nil
		}
	}
	return nil, lastErr
}

// findLinkedPR は timeline の反映ラグを吸収しつつ Issue に紐づく PR を探す。
func (e *Engine) findLinkedPR(ctx context.Context, repo string, issue int) (*gh.PullRequest, error) {
	pr, err := retryFindPR(ctx, linkedPRAttempts, linkedPRBackoff, func(ctx context.Context) (*gh.PullRequest, error) {
		return e.client.FindLinkedPR(ctx, repo, issue)
	})
	if err == nil && pr == nil {
		e.log.Warn("紐づく PR が見つかりませんでした", "repo", repo, "issue", issue, "attempts", linkedPRAttempts)
	}
	return pr, err
}

// dispatch はイベントを受け取り、状態遷移とジョブ投入を行う。
//
// ここでの処理はすべて短時間で終わるものに限る（エージェント起動はジョブに回す）。
func (e *Engine) dispatch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-e.events:
			if err := e.handleEvent(ctx, ev); err != nil {
				e.log.Error("イベント処理に失敗",
					"kind", ev.Kind, "repo", ev.Repo, "issue", ev.Issue, "err", err)
			}
		}
	}
}

func (e *Engine) handleEvent(ctx context.Context, ev Event) error {
	switch ev.Kind {
	case EvItemNew:
		return e.handleItemNew(ctx, ev)
	case EvStatusChanged:
		return e.handleStatusChanged(ctx, ev)
	case EvClosed:
		return e.handleClosed(ctx, ev)
	case EvComment:
		return e.handleComment(ctx, ev)
	case EvReview:
		return e.handleReview(ctx, ev)
	case EvVerifyTick:
		return e.handleVerifyTick(ctx, ev)
	case EvLeaseTick:
		return e.handleLeaseTick(ctx, ev)
	}
	return nil
}

func (e *Engine) handleItemNew(ctx context.Context, ev Event) error {
	it := &store.Item{
		Repo:          ev.Repo,
		IssueNumber:   ev.Issue,
		ProjectItemID: ev.ItemID,
		LastStatus:    ev.Status,
	}
	// 途中から Project に載ったカードの過去コメントを再生しないよう、現在をシードする。
	comments, err := e.client.ListComments(ctx, ev.Repo, ev.Issue, time.Time{})
	if err != nil {
		return err
	}
	for _, c := range comments {
		if c.ID > it.LastCommentID {
			it.LastCommentID = c.ID
		}
	}
	issue, err := e.client.GetIssue(ctx, ev.Repo, ev.Issue)
	if err != nil {
		return err
	}
	if issue.State == "closed" {
		it.Terminal = true
		return e.st.Upsert(it)
	}
	if err := e.st.Upsert(it); err != nil {
		return err
	}
	e.log.Info("新しいカードを検出しました", "repo", ev.Repo, "issue", ev.Issue, "status", ev.Status)

	switch ev.Status {
	case e.cfg.Statuses.Inbox:
		e.enqueue(ctx, Job{Phase: PhaseRefine, Repo: ev.Repo, Issue: ev.Issue})
	case e.cfg.Statuses.Ready:
		return e.startImplement(ctx, it, nil, "")
	}
	return nil
}

func (e *Engine) handleStatusChanged(ctx context.Context, ev Event) error {
	it, err := e.load(ev.Repo, ev.Issue)
	if err != nil || it == nil {
		return err
	}
	e.log.Info("Status の変化を検出しました",
		"repo", ev.Repo, "issue", ev.Issue, "from", ev.Prev, "to", ev.Status)

	if ev.ItemID != "" {
		it.ProjectItemID = ev.ItemID
	}
	it.LastStatus = ev.Status
	if ev.Status != e.cfg.Statuses.Verifying {
		it.VerifySince = time.Time{}
	}
	if err := e.st.Upsert(it); err != nil {
		return err
	}

	// 人間の手動操作で意味を持つのは Ready への移動だけ。
	if ev.Status == e.cfg.Statuses.Ready {
		it.RetryCount = 0
		return e.startImplement(ctx, it, nil, "")
	}
	return nil
}

func (e *Engine) handleClosed(ctx context.Context, ev Event) error {
	it, err := e.load(ev.Repo, ev.Issue)
	if err != nil || it == nil {
		return err
	}
	if it.Terminal {
		return nil
	}
	it.Terminal = true
	it.LeaseUntil = time.Time{}
	if err := e.st.Upsert(it); err != nil {
		return err
	}
	e.log.Info("Issue がクローズされたため終端にしました", "repo", ev.Repo, "issue", ev.Issue)
	return nil
}

// handleComment は新規コメントを判定し、Status に応じたジョブへ振り分ける。
//
// 何が新しいかの判定は通知ではなく last_comment_id で行う。自分（Bot）の発言は
// 無視するため、エージェントの投稿で自分が起床することはない。
func (e *Engine) handleComment(ctx context.Context, ev Event) error {
	it, err := e.load(ev.Repo, ev.Issue)
	if err != nil || it == nil || it.Terminal {
		return err
	}
	comments, err := e.client.ListComments(ctx, ev.Repo, ev.Issue, time.Time{})
	if err != nil {
		return err
	}
	maxID := it.LastCommentID
	var inputs []string
	for _, c := range comments {
		if c.ID <= it.LastCommentID {
			continue
		}
		if c.ID > maxID {
			maxID = c.ID
		}
		if c.User.Login == e.client.Login || c.User.IsBot() {
			continue
		}
		inputs = append(inputs, fmt.Sprintf("@%s:\n%s", c.User.Login, strings.TrimSpace(c.Body)))
	}
	if maxID == it.LastCommentID {
		return nil
	}

	advance := func() error {
		it.LastCommentID = maxID
		return e.st.Upsert(it)
	}
	if len(inputs) == 0 {
		return advance()
	}

	switch it.LastStatus {
	case e.cfg.Statuses.Inbox:
		if e.enqueue(ctx, Job{Phase: PhaseRefine, Repo: it.Repo, Issue: it.IssueNumber, Inputs: inputs}) {
			return advance()
		}
	case e.cfg.Statuses.Blocked:
		if e.enqueue(ctx, Job{Phase: PhaseTriageBlocked, Repo: it.Repo, Issue: it.IssueNumber, Inputs: inputs}) {
			return advance()
		}
	case e.cfg.Statuses.InReview:
		if e.enqueue(ctx, Job{Phase: PhaseTriageReview, Repo: it.Repo, Issue: it.IssueNumber, Inputs: inputs}) {
			return advance()
		}
	case e.cfg.Statuses.Ready:
		// 人間は Ready に動かすだけでよい。コメントは記録のみ。
		return advance()
	}
	// In Progress / Verifying は作業中。カーソルを進めず、後続のリコンサイルで再検出させる。
	return nil
}

// handleReview は PR に届いたレビューを判定する。
//
// 起床トリガーはレビュー提出の本文と diff のインラインコメントの両方。サマリを書かず
// 行コメントだけを送るレビューは珍しくないため、本文の有無だけで判定すると指摘を
// まるごと取りこぼす。両者は所属レビュー単位にまとめ、1 レビュー = 1 入力にする。
func (e *Engine) handleReview(ctx context.Context, ev Event) error {
	it, err := e.load(ev.Repo, ev.Issue)
	if err != nil || it == nil || it.Terminal || it.PRNumber == 0 {
		return err
	}
	if it.LastStatus != e.cfg.Statuses.InReview {
		return nil
	}
	// レビューと行コメントは別の ID 空間なので、カーソルも別々に持つ。
	reviewCK := fmt.Sprintf("review:%s#%d", it.Repo, it.PRNumber)
	inlineCK := fmt.Sprintf("review-comment:%s#%d", it.Repo, it.PRNumber)
	lastReview, err := e.cursorID(reviewCK)
	if err != nil {
		return err
	}
	lastInline, err := e.cursorID(inlineCK)
	if err != nil {
		return err
	}

	reviews, err := e.client.ListReviews(ctx, it.Repo, it.PRNumber)
	if err != nil {
		return err
	}
	inline, err := e.client.ListReviewComments(ctx, it.Repo, it.PRNumber)
	if err != nil {
		return err
	}

	// 新規の行コメントを所属レビューごとに束ねる。order は取得順（古い順）を保つため。
	maxInline := lastInline
	grouped := map[int64][]gh.ReviewComment{}
	var order []int64
	for _, rc := range inline {
		if rc.ID <= lastInline {
			continue
		}
		if rc.ID > maxInline {
			maxInline = rc.ID
		}
		if rc.User.Login == e.client.Login || rc.User.IsBot() || strings.TrimSpace(rc.Body) == "" {
			continue
		}
		if _, seen := grouped[rc.ReviewID]; !seen {
			order = append(order, rc.ReviewID)
		}
		grouped[rc.ReviewID] = append(grouped[rc.ReviewID], rc)
	}

	maxReview := lastReview
	var inputs []string
	for _, r := range reviews {
		if r.ID > maxReview {
			maxReview = r.ID
		}
		notes := grouped[r.ID]
		delete(grouped, r.ID)

		if r.User.Login == e.client.Login || r.User.IsBot() {
			continue
		}
		// Dismiss はレビューの取り消しで、行動を要求しない。
		if r.State == "DISMISSED" {
			continue
		}
		body := strings.TrimSpace(r.Body)
		switch {
		case r.ID <= lastReview:
			// 本文は処理済み。後から付いた行コメントだけを拾う。
			body = ""
		case r.State == "APPROVED":
			// Approve の本文は LGTM の類なので拾わない。ただし行コメントが付いていれば
			// nit の指摘なので、それだけを拾う。
			body = ""
		}
		if body == "" && len(notes) == 0 {
			continue
		}
		inputs = append(inputs, formatReviewInput(r.User.Login, r.State, body, notes))
	}
	// レビュー一覧に現れなかった行コメント（保険）。
	for _, id := range order {
		notes := grouped[id]
		if len(notes) == 0 {
			continue
		}
		inputs = append(inputs, formatReviewInput(notes[0].User.Login, "", "", notes))
	}

	if maxReview == lastReview && maxInline == lastInline {
		return nil
	}
	advance := func() error {
		if maxReview != lastReview {
			if err := e.st.SetCursor(reviewCK, strconv.FormatInt(maxReview, 10)); err != nil {
				return err
			}
		}
		if maxInline != lastInline {
			return e.st.SetCursor(inlineCK, strconv.FormatInt(maxInline, 10))
		}
		return nil
	}
	if len(inputs) == 0 {
		return advance()
	}
	if e.enqueue(ctx, Job{Phase: PhaseTriageReview, Repo: it.Repo, Issue: it.IssueNumber, Inputs: inputs}) {
		return advance()
	}
	return nil
}

// formatReviewInput はレビュー本文と行コメントを 1 件の入力にまとめる。
func formatReviewInput(author, state, body string, notes []gh.ReviewComment) string {
	var b strings.Builder
	if state != "" {
		fmt.Fprintf(&b, "@%s (%s):", author, state)
	} else {
		fmt.Fprintf(&b, "@%s:", author)
	}
	if body != "" {
		b.WriteString("\n" + body)
	}
	for _, n := range notes {
		fmt.Fprintf(&b, "\n\n- `%s`\n  %s", n.Location(), strings.TrimSpace(n.Body))
	}
	return b.String()
}

// cursorID は ID を保存したカーソルを読む。未設定・不正値は 0。
func (e *Engine) cursorID(name string) (int64, error) {
	raw, err := e.st.Cursor(name)
	if err != nil {
		return 0, err
	}
	v, _ := strconv.ParseInt(raw, 10, 64)
	return v, nil
}

// handleVerifyTick は Verifying の CI 状態を確認して分岐する。エージェントは起動しない。
func (e *Engine) handleVerifyTick(ctx context.Context, ev Event) error {
	it, err := e.load(ev.Repo, ev.Issue)
	if err != nil || it == nil || it.Terminal {
		return err
	}
	if it.LastStatus != e.cfg.Statuses.Verifying || e.isInflight(it.Repo, it.IssueNumber) {
		return nil
	}

	if it.PRNumber == 0 {
		pr, err := e.findLinkedPR(ctx, it.Repo, it.IssueNumber)
		if err != nil {
			return err
		}
		if pr == nil {
			return e.block(ctx, it, "PR が見つかりません。実装エージェントが PR を作成しなかった可能性があります。")
		}
		it.PRNumber = pr.Number
		it.Branch = pr.HeadRefName
		if err := e.st.Upsert(it); err != nil {
			return err
		}
	}

	pr, err := e.client.GetPullRequest(ctx, it.Repo, it.PRNumber)
	if err != nil {
		return err
	}
	if pr.Merged {
		it.Terminal = true
		return e.st.Upsert(it)
	}
	if pr.State == "CLOSED" {
		return e.block(ctx, it, fmt.Sprintf("PR #%d がマージされずにクローズされました。", it.PRNumber))
	}

	switch pr.CI() {
	case gh.CIPending:
		if it.VerifySince.IsZero() {
			it.VerifySince = time.Now()
			return e.st.Upsert(it)
		}
		if time.Since(it.VerifySince) > e.cfg.Limits.VerifyWait {
			return e.block(ctx, it, fmt.Sprintf("CI の完了待ちが %s を超えました（PR #%d）。",
				e.cfg.Limits.VerifyWait, it.PRNumber))
		}
		return nil

	case gh.CIFailure:
		it.RetryCount++
		if it.RetryCount > e.cfg.Limits.MaxRetries {
			return e.block(ctx, it, fmt.Sprintf(
				"CI の失敗に対する自動修正を %d 回試みましたが解決できませんでした（PR #%d）。\n"+
					"失敗内容を確認のうえ、方針を助言いただけると再開します。",
				e.cfg.Limits.MaxRetries, it.PRNumber))
		}
		e.log.Info("CI 失敗のため実装フェーズに差し戻します",
			"repo", it.Repo, "issue", it.IssueNumber, "retry", it.RetryCount)
		return e.startImplement(ctx, it, nil, ciFailureHint)

	default: // 成功、または CI 未設定
		e.enqueue(ctx, Job{Phase: PhaseReview, Repo: it.Repo, Issue: it.IssueNumber})
		return nil
	}
}

func (e *Engine) handleLeaseTick(ctx context.Context, ev Event) error {
	it, err := e.load(ev.Repo, ev.Issue)
	if err != nil || it == nil || it.Terminal {
		return err
	}
	if it.LastStatus != e.cfg.Statuses.InProgress || e.isInflight(it.Repo, it.IssueNumber) {
		return nil
	}
	return e.block(ctx, it, "実装フェーズが応答しなくなりました（ワーカーの異常終了かタイムアウト）。再開してよければコメントで指示してください。")
}

// startImplement は In Progress へ遷移させて実装ジョブを投入する。
func (e *Engine) startImplement(ctx context.Context, it *store.Item, inputs []string, hint string) error {
	it.LeaseUntil = time.Now().Add(e.cfg.Limits.ImplementTimeout + 5*time.Minute)
	it.VerifySince = time.Time{}
	if err := e.setStatus(ctx, it, e.cfg.Statuses.InProgress); err != nil {
		return err
	}
	e.enqueue(ctx, Job{Phase: PhaseImplement, Repo: it.Repo, Issue: it.IssueNumber, Inputs: inputs, CIHint: hint})
	return nil
}

// setStatus は Project の Status を変更し、ローカル状態に反映する。
//
// GitHub を先に更新する。途中で失敗してもローカルとの差分は次のポーリングで
// 検出されるが、遷移のトリガーになるのは Ready への移動だけなので副作用はない。
func (e *Engine) setStatus(ctx context.Context, it *store.Item, status string) error {
	if it.ProjectItemID == "" {
		return fmt.Errorf("%s#%d の project item ID が未解決です", it.Repo, it.IssueNumber)
	}
	if err := e.client.SetStatus(ctx, e.project, it.ProjectItemID, status); err != nil {
		return err
	}
	e.log.Info("Status を変更しました",
		"repo", it.Repo, "issue", it.IssueNumber, "from", it.LastStatus, "to", status)
	it.LastStatus = status
	return e.st.Upsert(it)
}

// block は Blocked へ遷移させ、理由を Issue にコメントする。
func (e *Engine) block(ctx context.Context, it *store.Item, reason string) error {
	body := "⏸ **Blocked**\n\n" + reason
	if err := e.client.AddComment(ctx, it.Repo, it.IssueNumber, body); err != nil {
		e.log.Error("Blocked コメントの投稿に失敗", "repo", it.Repo, "issue", it.IssueNumber, "err", err)
	}
	it.LeaseUntil = time.Time{}
	it.VerifySince = time.Time{}
	return e.setStatus(ctx, it, e.cfg.Statuses.Blocked)
}

func (e *Engine) load(repo string, issue int) (*store.Item, error) {
	it, err := e.st.Get(repo, issue)
	if err != nil {
		return nil, err
	}
	if it == nil {
		e.log.Debug("ローカル状態に存在しない item", "repo", repo, "issue", issue)
	}
	return it, nil
}

func (e *Engine) findByPR(repo string, pr int) (*store.Item, error) {
	all, err := e.st.List()
	if err != nil {
		return nil, err
	}
	for _, it := range all {
		if it.Repo == repo && it.PRNumber == pr {
			return it, nil
		}
	}
	return nil, nil
}

// promptContext はエージェントに渡す共通コンテキストを組み立てる。
func (e *Engine) promptContext(ctx context.Context, it *store.Item, inputs []string, hint string) (prompt.Context, error) {
	issue, err := e.client.GetIssue(ctx, it.Repo, it.IssueNumber)
	if err != nil {
		return prompt.Context{}, err
	}
	comments, err := e.client.LastComments(ctx, it.Repo, it.IssueNumber, e.cfg.Limits.ContextComments)
	if err != nil {
		return prompt.Context{}, err
	}
	// 行コメントは PR ができてから初めて存在する。自分の返信も含めて渡すことで、
	// 一度答えた指摘に再度反応することを防ぐ。
	var reviewComments []gh.ReviewComment
	if it.PRNumber != 0 {
		reviewComments, err = e.client.LastReviewComments(ctx, it.Repo, it.PRNumber, e.cfg.Limits.ContextComments)
		if err != nil {
			return prompt.Context{}, err
		}
	}
	return prompt.Context{
		Repo:           it.Repo,
		Issue:          issue,
		Comments:       comments,
		ReviewComments: reviewComments,
		NewInputs:      inputs,
		PRNumber:       it.PRNumber,
		Gate:           e.ws.ReadFile(it.Repo, e.cfg.GateFile),
		GatePath:       e.cfg.GateFile,
		RetryCount:     it.RetryCount,
		MaxRetries:     e.cfg.Limits.MaxRetries,
		CIHint:         hint,
	}, nil
}

// runJob はエージェントを 1 回起動し、結果に応じて状態を遷移させる。
func (e *Engine) runJob(ctx context.Context, j Job) {
	it, err := e.load(j.Repo, j.Issue)
	if err != nil || it == nil {
		e.log.Error("ジョブ対象の状態を読めません", "repo", j.Repo, "issue", j.Issue, "err", err)
		return
	}
	if it.Terminal {
		return
	}

	runID, err := e.st.StartRun(j.Repo, j.Issue, j.Phase)
	if err != nil {
		e.log.Error("実行ログの記録に失敗", "err", err)
	}
	result, err := e.execute(ctx, j, it)
	outcome := "ok"
	if err != nil {
		outcome = "error: " + err.Error()
	}
	if runID != 0 {
		if err := e.st.EndRun(runID, outcome); err != nil {
			e.log.Error("実行ログの更新に失敗", "err", err)
		}
	}
	if err != nil {
		e.log.Error("ジョブが失敗しました", "phase", j.Phase, "repo", j.Repo, "issue", j.Issue, "err", err)
		msg := fmt.Sprintf("エージェント（%s）の実行に失敗しました。\n\n```\n%s\n```", j.Phase, err.Error())
		if result != nil {
			if tail := result.Tail(2000); tail != "" {
				msg += fmt.Sprintf("\n\n出力の末尾:\n\n```\n%s\n```", tail)
			}
			if result.LogPath != "" {
				msg += fmt.Sprintf("\n\nログ: `%s`", result.LogPath)
			}
		}
		// 精緻化フェーズの失敗は Inbox に留め、実装系の失敗のみ Blocked にする。
		if j.Phase == PhaseRefine {
			if err := e.client.AddComment(ctx, it.Repo, it.IssueNumber, msg); err != nil {
				e.log.Error("失敗コメントの投稿に失敗", "err", err)
			}
			return
		}
		if err := e.block(ctx, it, msg); err != nil {
			e.log.Error("Blocked への遷移に失敗", "err", err)
		}
		return
	}
	e.log.Info("ジョブが完了しました", "phase", j.Phase, "repo", j.Repo, "issue", j.Issue,
		"duration", result.Duration.Round(time.Second), "markers", result.Markers)

	if err := e.applyResult(ctx, j, it, result); err != nil {
		e.log.Error("結果の反映に失敗", "phase", j.Phase, "repo", j.Repo, "issue", j.Issue, "err", err)
	}
}

// execute はワークツリーを整えてエージェントを起動する。
func (e *Engine) execute(ctx context.Context, j Job, it *store.Item) (*agent.Result, error) {
	branch := it.Branch
	if j.Phase == PhaseRefine {
		// 精緻化はコードを書かないので既定ブランチで読む。
		branch = ""
	}
	if _, err := e.ws.Prepare(ctx, it.Repo, branch, e.env); err != nil {
		return nil, fmt.Errorf("ワークツリーの準備に失敗: %w", err)
	}
	pc, err := e.promptContext(ctx, it, j.Inputs, j.CIHint)
	if err != nil {
		return nil, err
	}

	var text string
	var kind string
	switch j.Phase {
	case PhaseRefine:
		text, kind = prompt.Refine(pc), config.AgentRefine
	case PhaseImplement:
		text, kind = prompt.Implement(pc), config.AgentImplement
	case PhaseReview:
		text, kind = prompt.Review(pc), config.AgentReview
	case PhaseTriageReview:
		text, kind = prompt.Triage(pc, prompt.TriageReview), config.AgentTriage
	case PhaseTriageBlocked:
		text, kind = prompt.Triage(pc, prompt.TriageBlocked), config.AgentTriage
	default:
		return nil, fmt.Errorf("未知のフェーズ: %s", j.Phase)
	}

	spec := e.cfg.AgentFor(kind).Spec()
	attrs := []any{
		"phase", j.Phase,
		"repo", j.Repo,
		"issue", j.Issue,
		"command", spec.ResolvedCommand(),
		"adapter", spec.Adapter().Name(),
		"timeout", spec.Timeout,
	}
	if branch != "" {
		attrs = append(attrs, "branch", branch)
	}
	if spec.Model != "" {
		attrs = append(attrs, "model", spec.Model)
	}
	e.log.Info("エージェントを起動します", attrs...)
	return e.runner.Run(ctx, spec, j.Phase, e.ws.Path(it.Repo), text)
}

// applyResult はエージェントのマーカー出力を状態遷移に変換する。
func (e *Engine) applyResult(ctx context.Context, j Job, it *store.Item, res *agent.Result) error {
	action := strings.ToUpper(res.Marker(agent.MarkerAction))
	reason := res.Marker(agent.MarkerReason)

	switch j.Phase {
	case PhaseRefine:
		// Inbox に留まる。Ready への移動は人間の判断（唯一の手動操作）。
		return nil

	case PhaseImplement:
		if action == "BLOCKED" {
			return e.block(ctx, it, fmt.Sprintf("実装を続行できませんでした。\n\n%s", fallback(reason, "理由の報告がありません。")))
		}
		pr, err := e.findLinkedPR(ctx, it.Repo, it.IssueNumber)
		if err != nil {
			return err
		}
		if pr == nil {
			return e.block(ctx, it, "実装は完了したと報告されましたが、この Issue に紐づく PR が見つかりません。\n"+
				"PR 本文に `Closes #"+strconv.Itoa(it.IssueNumber)+"` が含まれているか確認してください。")
		}
		it.PRNumber = pr.Number
		it.Branch = pr.HeadRefName
		it.LeaseUntil = time.Time{}
		it.VerifySince = time.Now()
		return e.setStatus(ctx, it, e.cfg.Statuses.Verifying)

	case PhaseReview:
		if strings.ToUpper(res.Marker(agent.MarkerVerdict)) == "PASS" {
			it.RetryCount = 0
			it.VerifySince = time.Time{}
			return e.setStatus(ctx, it, e.cfg.Statuses.InReview)
		}
		it.RetryCount++
		if it.RetryCount > e.cfg.Limits.MaxRetries {
			return e.block(ctx, it, fmt.Sprintf(
				"セルフレビューが %d 回連続で不合格でした。\n\n最後の指摘: %s",
				e.cfg.Limits.MaxRetries, fallback(reason, "（理由の報告なし）")))
		}
		return e.startImplement(ctx, it, nil,
			"セルフレビューで不合格となりました。次の指摘に対応してください: "+fallback(reason, "（理由の報告なし）"))

	case PhaseTriageReview:
		if action == "NEEDS_FIX" {
			return e.startImplement(ctx, it, j.Inputs,
				"レビューで修正が要求されました: "+fallback(reason, "（理由の報告なし）"))
		}
		// ANSWERED。In Review のまま人間の判断を待つ。
		return nil

	case PhaseTriageBlocked:
		if action == "RESPEC" {
			it.RetryCount = 0
			it.LeaseUntil = time.Time{}
			if err := e.setStatus(ctx, it, e.cfg.Statuses.Inbox); err != nil {
				return err
			}
			e.enqueue(ctx, Job{Phase: PhaseRefine, Repo: it.Repo, Issue: it.IssueNumber, Inputs: j.Inputs})
			return nil
		}
		// RESUME。人間が介入したのでリトライ回数をリセットして実装に戻す。
		it.RetryCount = 0
		return e.startImplement(ctx, it, j.Inputs, fallback(reason, ""))
	}
	return nil
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
