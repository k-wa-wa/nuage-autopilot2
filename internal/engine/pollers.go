package engine

import (
	"context"
	"time"
)

// cursorNotifications は notifications API の since カーソルを保存するキー。
const cursorNotifications = "notifications:since"

// notificationOverlap は取りこぼし防止のためにカーソルを巻き戻す幅。
// 重複して拾っても item ごとの last_comment_id で除去されるため実害はない。
const notificationOverlap = 30 * time.Second

// pollProject は Projects v2 を GraphQL でポーリングし、Status の差分を検出する。
//
// カードのドラッグ（フィールド値変更）は通知を生成しないため、唯一の人間の手動操作である
// 「Inbox → Ready」はこのループでしか検知できない。
func (e *Engine) pollProject(ctx context.Context) {
	e.pollProjectOnce(ctx)
	t := time.NewTicker(e.cfg.Poll.Project)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.pollProjectOnce(ctx)
		}
	}
}

func (e *Engine) pollProjectOnce(ctx context.Context) {
	items, err := e.client.ListItems(ctx, e.project)
	if err != nil {
		e.log.Error("Project のポーリングに失敗", "err", err)
		return
	}
	for _, pi := range items {
		if !pi.IsIssue() || !e.watched(pi.Repo) {
			continue
		}
		cur, err := e.st.Get(pi.Repo, pi.IssueNumber)
		if err != nil {
			e.log.Error("ローカル状態の読み出しに失敗", "repo", pi.Repo, "issue", pi.IssueNumber, "err", err)
			continue
		}
		switch {
		case cur == nil:
			e.emit(ctx, Event{Kind: EvItemNew, Repo: pi.Repo, Issue: pi.IssueNumber,
				ItemID: pi.ItemID, Status: pi.Status})
		case cur.Terminal:
			// 終端。何が起きても起動しない。
		case pi.IsClosed():
			e.emit(ctx, Event{Kind: EvClosed, Repo: pi.Repo, Issue: pi.IssueNumber,
				ItemID: pi.ItemID, Status: pi.Status})
		case pi.Status != cur.LastStatus:
			e.emit(ctx, Event{Kind: EvStatusChanged, Repo: pi.Repo, Issue: pi.IssueNumber,
				ItemID: pi.ItemID, Status: pi.Status, Prev: cur.LastStatus})
		}
	}
}

// pollNotifications は通知をポーリングし、コメントとレビューの起床シグナルを流す。
//
// 既読/未読は状態として使わず、all=true と since カーソルだけを信頼する。
// 何が新しいかの判定はここでは行わず、item ごとの last_comment_id が担う。
func (e *Engine) pollNotifications(ctx context.Context) {
	interval := e.cfg.Poll.Notification
	for {
		next := e.pollNotificationsOnce(ctx)
		if next > interval {
			interval = next
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (e *Engine) pollNotificationsOnce(ctx context.Context) time.Duration {
	start := time.Now()
	raw, err := e.st.Cursor(cursorNotifications)
	if err != nil {
		e.log.Error("通知カーソルの読み出しに失敗", "err", err)
		return 0
	}
	since := start.Add(-notificationOverlap)
	if raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		}
	}

	notifs, pollInterval, err := e.client.ListNotifications(ctx, since)
	if err != nil {
		e.log.Error("通知のポーリングに失敗", "err", err)
		return 0
	}
	for _, n := range notifs {
		repo := n.Repo()
		if !e.watched(repo) || !n.IsIssueLike() {
			continue
		}
		num := n.Number()
		if num == 0 {
			continue
		}
		if n.IsPullRequest() {
			// PR スレッドは pr_number から Issue に逆引きする。
			it, err := e.findByPR(repo, num)
			if err != nil || it == nil {
				continue
			}
			e.emit(ctx, Event{Kind: EvReview, Repo: repo, Issue: it.IssueNumber})
			continue
		}
		e.emit(ctx, Event{Kind: EvComment, Repo: repo, Issue: num})
	}

	newCursor := start.Add(-notificationOverlap).UTC().Format(time.RFC3339)
	if err := e.st.SetCursor(cursorNotifications, newCursor); err != nil {
		e.log.Error("通知カーソルの保存に失敗", "err", err)
	}
	return pollInterval
}

// pollTicks は Verifying の CI 確認と、In Progress のスタック検知を回す。
//
// Verifying はエージェントを持たないため、ここでの確認はパイプラインを占有しない。
func (e *Engine) pollTicks(ctx context.Context) {
	t := time.NewTicker(e.cfg.Poll.Verify)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tickOnce(ctx)
		}
	}
}

func (e *Engine) tickOnce(ctx context.Context) {
	verifying, err := e.st.ListByStatus(e.cfg.Statuses.Verifying)
	if err != nil {
		e.log.Error("Verifying の一覧取得に失敗", "err", err)
	}
	for _, it := range verifying {
		if e.isInflight(it.Repo, it.IssueNumber) {
			continue
		}
		e.emit(ctx, Event{Kind: EvVerifyTick, Repo: it.Repo, Issue: it.IssueNumber})
	}

	inProgress, err := e.st.ListByStatus(e.cfg.Statuses.InProgress)
	if err != nil {
		e.log.Error("In Progress の一覧取得に失敗", "err", err)
		return
	}
	now := time.Now()
	for _, it := range inProgress {
		if e.isInflight(it.Repo, it.IssueNumber) {
			continue
		}
		if it.LeaseUntil.IsZero() || now.Before(it.LeaseUntil) {
			continue
		}
		e.emit(ctx, Event{Kind: EvLeaseTick, Repo: it.Repo, Issue: it.IssueNumber})
	}
}

// reconcile は通知の取りこぼしを自己修復する保険のループ。
func (e *Engine) reconcile(ctx context.Context) {
	t := time.NewTicker(e.cfg.Poll.Reconcile)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.reconcileOnce(ctx)
		}
	}
}

func (e *Engine) reconcileOnce(ctx context.Context) {
	since := time.Now().Add(-2 * e.cfg.Poll.Reconcile)
	for _, r := range e.cfg.Repos {
		repo := r.String()
		issues, err := e.client.ListIssuesUpdatedSince(ctx, repo, since)
		if err != nil {
			e.log.Error("リコンサイルに失敗", "repo", repo, "err", err)
			continue
		}
		for _, is := range issues {
			if is.IsPullRequest() {
				continue
			}
			it, err := e.st.Get(repo, is.Number)
			if err != nil || it == nil || it.Terminal {
				continue
			}
			if is.State == "closed" {
				e.emit(ctx, Event{Kind: EvClosed, Repo: repo, Issue: is.Number})
				continue
			}
			e.emit(ctx, Event{Kind: EvComment, Repo: repo, Issue: is.Number})
		}
	}
}
