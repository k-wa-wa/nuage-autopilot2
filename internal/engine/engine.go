// Package engine はポーリングループとディスパッチャを束ねる常駐ワーカー本体。
//
// 4 つのポーラ goroutine がイベントを 1 本のチャネルに流し込み、ディスパッチャが
// それを捌く。エージェントを起動する処理だけは 1 本のワーカー goroutine に直列化し、
// CI 待ち（Verifying）はエージェントを持たないためパイプラインを占有しない。
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/config"
	"nuage-autopilot2/internal/gh"
	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/workspace"
)

// EventKind はイベントの種別。
type EventKind string

// イベント種別。
const (
	// EvItemNew は Project に新しいカードが現れたことを表す。
	EvItemNew EventKind = "item_new"
	// EvStatusChanged は Status が変化したことを表す。
	EvStatusChanged EventKind = "status_changed"
	// EvComment は Issue にコメントが付いた可能性を表す起床シグナル。
	EvComment EventKind = "comment"
	// EvReview は PR にレビューが提出された可能性を表す起床シグナル。
	EvReview EventKind = "review"
	// EvClosed は Issue がクローズされたことを表す。
	EvClosed EventKind = "closed"
	// EvVerifyTick は Verifying レーンの定期確認。
	EvVerifyTick EventKind = "verify_tick"
	// EvLeaseTick は In Progress のスタック確認。
	EvLeaseTick EventKind = "lease_tick"
)

// Event はポーラが検出した出来事。
type Event struct {
	Kind   EventKind
	Repo   string
	Issue  int
	ItemID string
	Status string
	Prev   string
}

// Job はエージェントを起動する仕事。ワーカー goroutine が直列に処理する。
type Job struct {
	Phase  string
	Repo   string
	Issue  int
	Inputs []string
	CIHint string
}

// 実行フェーズ名。runs テーブルとログのファイル名にも使う。
const (
	PhaseRefine        = "refine"
	PhaseImplement     = "implement"
	PhaseReview        = "review"
	PhaseTriageReview  = "triage-review"
	PhaseTriageBlocked = "triage-blocked"
)

// Engine は常駐ワーカー。
type Engine struct {
	cfg     *config.Config
	st      *store.Store
	client  *gh.Client
	project *gh.Project
	ws      *workspace.Manager
	runner  *agent.Runner
	log     *slog.Logger
	env     []string

	events chan Event
	jobs   chan Job

	mu       sync.Mutex
	inflight map[string]bool
}

// New は Engine を組み立てる。GitHub への接続と Project メタ情報の解決まで行う。
func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Engine, error) {
	client, err := gh.New()
	if err != nil {
		return nil, err
	}
	if err := client.ResolveLogin(ctx); err != nil {
		return nil, fmt.Errorf("認証ユーザーの取得に失敗: %w", err)
	}
	project, err := client.LoadProject(ctx, cfg.Project.OwnerType, cfg.Project.Owner,
		cfg.Project.Number, cfg.Project.StatusField)
	if err != nil {
		return nil, err
	}
	// 設定された Status 名がすべて Project に存在するか、起動時に検証する。
	for _, s := range []string{
		cfg.Statuses.Inbox, cfg.Statuses.Ready, cfg.Statuses.InProgress,
		cfg.Statuses.Verifying, cfg.Statuses.InReview, cfg.Statuses.Blocked, cfg.Statuses.Done,
	} {
		if _, err := project.OptionID(s); err != nil {
			return nil, err
		}
	}

	st, err := store.Open(cfg.Database)
	if err != nil {
		return nil, err
	}

	env := append(os.Environ(), "GH_TOKEN="+client.Token(), "GITHUB_TOKEN="+client.Token())
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	logDir := filepath.Join(filepath.Dir(cfg.Database), "logs")
	e := &Engine{
		cfg:      cfg,
		st:       st,
		client:   client,
		project:  project,
		ws:       workspace.New(cfg.Workspace, "GH_TOKEN", "", ""),
		runner:   agent.New(logDir, env),
		log:      log,
		env:      env,
		events:   make(chan Event, 256),
		jobs:     make(chan Job, 256),
		inflight: map[string]bool{},
	}
	return e, nil
}

// Close は保持リソースを解放する。
func (e *Engine) Close() error { return e.st.Close() }

// Login は認証ユーザーのログイン名を返す。
func (e *Engine) Login() string { return e.client.Login }

// Items はローカル状態を一覧で返す。
func (e *Engine) Items() ([]*store.Item, error) { return e.st.List() }

// EnsureRepos は設定された全リポジトリが clone 済みであることを保証する。
func (e *Engine) EnsureRepos(ctx context.Context) error {
	repos := make([]string, 0, len(e.cfg.Repos))
	for _, r := range e.cfg.Repos {
		repos = append(repos, r.String())
	}
	return e.ws.EnsureAll(ctx, repos)
}

// Init はコールドスタートのシードを行う。
//
// 各 open item の最新コメント ID を「処理済み」として書き込み、通知カーソルを
// 現在時刻に合わせる。これをしないと、DB を作り直した瞬間に過去の全コメントを
// 再生してしまう。
func (e *Engine) Init(ctx context.Context) error {
	items, err := e.client.ListItems(ctx, e.project)
	if err != nil {
		return err
	}
	n := 0
	for _, pi := range items {
		if !pi.IsIssue() || !e.watched(pi.Repo) {
			continue
		}
		it := &store.Item{
			Repo:          pi.Repo,
			IssueNumber:   pi.IssueNumber,
			ProjectItemID: pi.ItemID,
			LastStatus:    pi.Status,
			Terminal:      pi.IsClosed(),
		}
		if existing, err := e.st.Get(pi.Repo, pi.IssueNumber); err == nil && existing != nil {
			// 既存レコードは壊さず、カーソルだけ補う。
			it = existing
			it.ProjectItemID = pi.ItemID
			it.LastStatus = pi.Status
			it.Terminal = pi.IsClosed()
		}
		if !it.Terminal && it.LastCommentID == 0 {
			comments, err := e.client.ListComments(ctx, pi.Repo, pi.IssueNumber, time.Time{})
			if err != nil {
				return fmt.Errorf("%s#%d のコメント取得に失敗: %w", pi.Repo, pi.IssueNumber, err)
			}
			for _, c := range comments {
				if c.ID > it.LastCommentID {
					it.LastCommentID = c.ID
				}
			}
		}
		if err := e.st.Upsert(it); err != nil {
			return err
		}
		n++
	}
	if err := e.st.SetCursor(cursorNotifications, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	e.log.Info("コールドスタートのシード完了", "items", n, "login", e.client.Login)
	return nil
}

// Run は常駐ワーカーを起動する。ctx がキャンセルされるまでブロックする。
func (e *Engine) Run(ctx context.Context) error {
	empty, err := e.st.IsEmpty()
	if err != nil {
		return err
	}
	if empty {
		e.log.Info("DB が空のためコールドスタートのシードを実行します")
		if err := e.Init(ctx); err != nil {
			return err
		}
	}

	e.log.Info("リポジトリを同期しています")
	if err := e.EnsureRepos(ctx); err != nil {
		return err
	}

	if err := e.recoverOrphans(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	start := func(name string, fn func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					e.log.Error("goroutine が panic しました", "name", name, "panic", r)
				}
			}()
			fn(ctx)
		}()
	}

	start("project-poller", e.pollProject)
	start("notification-poller", e.pollNotifications)
	start("tick-poller", e.pollTicks)
	start("reconciler", e.reconcile)
	start("dispatcher", e.dispatch)
	start("agent-worker", e.workAgents)

	e.log.Info("ワーカーを起動しました",
		"project", fmt.Sprintf("%s/%d", e.cfg.Project.Owner, e.cfg.Project.Number),
		"login", e.client.Login)

	<-ctx.Done()
	wg.Wait()
	e.log.Info("ワーカーを停止しました")
	return nil
}

// recoverOrphans は起動時に In Progress のまま残っている item を回収対象にする。
//
// ワーカーは単一インスタンス前提のため、起動直後に In Progress の item があれば
// 前回の異常終了で取り残されたものとみなし、lease を切らせて Blocked へ送る。
func (e *Engine) recoverOrphans() error {
	items, err := e.st.ListByStatus(e.cfg.Statuses.InProgress)
	if err != nil {
		return err
	}
	for _, it := range items {
		it.LeaseUntil = time.Now()
		if err := e.st.Upsert(it); err != nil {
			return err
		}
		e.log.Warn("前回の実行が残した In Progress を検出しました", "repo", it.Repo, "issue", it.IssueNumber)
	}
	return nil
}

func (e *Engine) watched(repo string) bool {
	for _, r := range e.cfg.Repos {
		if r.String() == repo {
			return true
		}
	}
	return false
}

func (e *Engine) emit(ctx context.Context, ev Event) {
	select {
	case e.events <- ev:
	case <-ctx.Done():
	default:
		e.log.Warn("イベントキューが満杯のため破棄しました", "kind", ev.Kind, "repo", ev.Repo, "issue", ev.Issue)
	}
}

func key(repo string, issue int) string { return fmt.Sprintf("%s#%d", repo, issue) }

// tryClaim は item を実行中としてマークする。既に実行中なら false。
func (e *Engine) tryClaim(repo string, issue int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := key(repo, issue)
	if e.inflight[k] {
		return false
	}
	e.inflight[k] = true
	return true
}

func (e *Engine) release(repo string, issue int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.inflight, key(repo, issue))
}

func (e *Engine) isInflight(repo string, issue int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inflight[key(repo, issue)]
}

// enqueue はエージェントジョブを投入する。同一 item が実行中なら投入しない。
func (e *Engine) enqueue(ctx context.Context, j Job) bool {
	if !e.tryClaim(j.Repo, j.Issue) {
		e.log.Debug("実行中のためジョブ投入をスキップ", "phase", j.Phase, "repo", j.Repo, "issue", j.Issue)
		return false
	}
	select {
	case e.jobs <- j:
		e.log.Info("ジョブを投入しました", "phase", j.Phase, "repo", j.Repo, "issue", j.Issue)
		return true
	case <-ctx.Done():
		e.release(j.Repo, j.Issue)
		return false
	}
}

// workAgents はジョブを 1 件ずつ直列に処理する。
func (e *Engine) workAgents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-e.jobs:
			e.runJob(ctx, j)
			e.release(j.Repo, j.Issue)
		}
	}
}
