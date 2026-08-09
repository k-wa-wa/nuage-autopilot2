package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/config"
	"nuage-autopilot2/internal/gh"
	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/workspace"
)

// fakeGitHub はテスト用の GitHub REST / GraphQL サーバー。
type fakeGitHub struct {
	mu            sync.Mutex
	statusRecord  []string // setStatus で設定された status の履歴
	comments      []string // AddComment で投稿された本文
	issueComments []gh.Comment
	prState       string // "OPEN", "CLOSED", "MERGED"
	prCheckState  string // "SUCCESS", "FAILURE", "PENDING"
	linkedPRNum   int
	reviews       []gh.Review
}

func newFakeGitHubServer(t *testing.T, s *fakeGitHub) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		// REST エンドポイント
		if r.Method == http.MethodGet && r.URL.Path == "/user" {
			json.NewEncoder(w).Encode(map[string]string{"login": "test-bot"})
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/issues/") && !strings.Contains(r.URL.Path, "/comments") {
			json.NewEncoder(w).Encode(gh.Issue{
				Number: 1,
				Title:  "Test Issue",
				Body:   "Implement feature X",
				State:  "open",
			})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments") {
			json.NewEncoder(w).Encode(s.issueComments)
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/comments") {
			var payload struct {
				Body string `json:"body"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			s.comments = append(s.comments, payload.Body)
			json.NewEncoder(w).Encode(map[string]any{"id": 100})
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/") && strings.Contains(r.URL.Path, "/reviews") {
			json.NewEncoder(w).Encode(s.reviews)
			return
		}

		// GraphQL エンドポイント
		if r.URL.Path == "/graphql" {
			bodyBytes, _ := io.ReadAll(r.Body)
			bodyStr := string(bodyBytes)

			// LoadProject クエリ
			if strings.Contains(bodyStr, "projectV2(number:") {
				fmt.Fprintln(w, `{
					"data": {
						"user": {
							"projectV2": {
								"id": "proj_123",
								"field": {
									"id": "field_status",
									"name": "Status",
									"options": [
										{"id": "opt_inbox", "name": "📥 Inbox"},
										{"id": "opt_ready", "name": "🎯 Ready"},
										{"id": "opt_in_progress", "name": "🚧 In Progress"},
										{"id": "opt_verifying", "name": "🔍 Verifying"},
										{"id": "opt_in_review", "name": "👀 In Review"},
										{"id": "opt_blocked", "name": "⏸ Blocked"},
										{"id": "opt_done", "name": "✅ Done"}
									]
								}
							}
						}
					}
				}`)
				return
			}

			// setStatus mutation
			if strings.Contains(bodyStr, "updateProjectV2ItemFieldValue") {
				for _, pair := range [][]string{
					{"opt_inbox", "📥 Inbox"},
					{"opt_ready", "🎯 Ready"},
					{"opt_in_progress", "🚧 In Progress"},
					{"opt_verifying", "🔍 Verifying"},
					{"opt_in_review", "👀 In Review"},
					{"opt_blocked", "⏸ Blocked"},
					{"opt_done", "✅ Done"},
				} {
					if strings.Contains(bodyStr, pair[0]) {
						s.statusRecord = append(s.statusRecord, pair[1])
						break
					}
				}
				fmt.Fprintln(w, `{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"item_1"}}}}`)
				return
			}

			// FindLinkedPR クエリ (timelineItems)
			if strings.Contains(bodyStr, "timelineItems") {
				if s.linkedPRNum > 0 {
					fmt.Fprintf(w, `{
						"data": {
							"repository": {
								"issue": {
									"timelineItems": {
										"nodes": [
											{
												"__typename": "ConnectedEvent",
												"subject": {
													"number": %d,
													"state": "%s",
													"merged": %t,
													"headRefName": "feat/test-pr"
												}
											}
										]
									}
								}
							}
						}
					}`, s.linkedPRNum, s.prState, s.prState == "MERGED")
				} else {
					fmt.Fprintln(w, `{"data":{"repository":{"issue":{"timelineItems":{"nodes":[]}}}}}`)
				}
				return
			}

			// GetPullRequest クエリ
			if strings.Contains(bodyStr, "pullRequest(number:") {
				fmt.Fprintf(w, `{
					"data": {
						"repository": {
							"pullRequest": {
								"number": %d,
								"state": "%s",
								"isDraft": false,
								"merged": %t,
								"headRefName": "feat/test-pr",
								"reviewDecision": "",
								"commits": {
									"nodes": [
										{
											"commit": {
												"statusCheckRollup": {
													"state": "%s"
												}
											}
										}
									]
								}
							}
						}
					}
				}`, s.linkedPRNum, s.prState, s.prState == "MERGED", s.prCheckState)
				return
			}
		}

		http.NotFound(w, r)
	}))
}

// setupTestEngine はシナリオテスト用の Engine とテスト環境を構築する。
func setupTestEngine(t *testing.T, fake *fakeGitHub, fakeAgentScript string) (*Engine, string, func()) {
	t.Helper()
	server := newFakeGitHubServer(t, fake)

	tmpDir, err := os.MkdirTemp("", "autopilot-scenario-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}

	client := gh.NewForTest("test-token", server.URL, server.URL+"/graphql")
	client.Login = "test-bot"

	project := &gh.Project{
		ID:            "proj_123",
		StatusFieldID: "field_status",
		StatusField:   "Status",
		Options: map[string]string{
			"📥 Inbox":       "opt_inbox",
			"🎯 Ready":       "opt_ready",
			"🚧 In Progress": "opt_in_progress",
			"🔍 Verifying":   "opt_verifying",
			"👀 In Review":   "opt_in_review",
			"⏸ Blocked":     "opt_blocked",
			"✅ Done":        "opt_done",
		},
	}

	wsDir := filepath.Join(tmpDir, "workspaces")
	bareDir := filepath.Join(tmpDir, "origin.git")
	seedDir := filepath.Join(tmpDir, "seed")

	// bare リポジトリと初期コミットのセットアップ
	ctx := context.Background()
	mustRunGit(t, ctx, tmpDir, "git", "init", "--bare", "--initial-branch=main", bareDir)
	mustRunGit(t, ctx, tmpDir, "git", "init", "--initial-branch=main", seedDir)
	mustRunGit(t, ctx, seedDir, "git", "config", "user.email", "t@example.com")
	mustRunGit(t, ctx, seedDir, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, ctx, seedDir, "git", "add", ".")
	mustRunGit(t, ctx, seedDir, "git", "commit", "-m", "init")
	mustRunGit(t, ctx, seedDir, "git", "remote", "add", "origin", bareDir)
	mustRunGit(t, ctx, seedDir, "git", "push", "-u", "origin", "main")

	// ワークスペースに clone
	repoDir := filepath.Join(wsDir, "owner", "repo")
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, ctx, wsDir, "git", "clone", bareDir, repoDir)

	scriptPath := filepath.Join(tmpDir, "fake_agent.sh")
	if err := os.WriteFile(scriptPath, []byte(fakeAgentScript), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg := &config.Config{
		Workspace: wsDir,
		Database:  dbPath,
		GateFile:  ".agents/autopilot-gate.md",
		Project: config.Project{
			Owner:       "owner",
			Number:      1,
			OwnerType:   "user",
			StatusField: "Status",
		},
		Repos: []config.Repo{{Owner: "owner", Name: "repo"}},
		Statuses: config.Statuses{
			Inbox:      "📥 Inbox",
			Ready:      "🎯 Ready",
			InProgress: "🚧 In Progress",
			Verifying:  "🔍 Verifying",
			InReview:   "👀 In Review",
			Blocked:    "⏸ Blocked",
			Done:       "✅ Done",
		},
		Limits: config.Limits{
			MaxRetries:       5,
			ImplementTimeout: time.Hour,
			AgentTimeout:     10 * time.Minute,
			VerifyWait:       30 * time.Minute,
			ContextComments:  10,
		},
		Agents: map[string]config.Agent{
			config.AgentRefine:    {Command: scriptPath, Timeout: 10 * time.Second},
			config.AgentImplement: {Command: scriptPath, Timeout: 10 * time.Second},
			config.AgentReview:    {Command: scriptPath, Timeout: 10 * time.Second},
			config.AgentTriage:    {Command: scriptPath, Timeout: 10 * time.Second},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	logDir := filepath.Join(tmpDir, "logs")

	e := &Engine{
		cfg:      cfg,
		st:       st,
		client:   client,
		project:  project,
		ws:       workspace.New(wsDir, "GH_TOKEN", "", ""),
		runner:   agent.New(logDir, os.Environ()),
		log:      logger,
		events:   make(chan Event, 256),
		jobs:     make(chan Job, 256),
		inflight: map[string]bool{},
	}

	cleanup := func() {
		e.Close()
		server.Close()
		os.RemoveAll(tmpDir)
	}
	return e, tmpDir, cleanup
}

// シナリオ 1: ハッピーパス（Ready -> Implement -> Verifying -> Review PASS -> In Review）
func TestScenario_HappyPath(t *testing.T) {
	fake := &fakeGitHub{
		prState:      "OPEN",
		prCheckState: "SUCCESS",
		linkedPRNum:  2,
	}
	script := `#!/bin/sh
cat > /dev/null
echo "AUTOPILOT_ACTION: PR_READY"
echo "AUTOPILOT_VERDICT: PASS"
echo "AUTOPILOT_REASON: All tests passed"
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	ctx := context.Background()

	// 1. 初期アイテム登録
	it := &store.Item{
		Repo:          "owner/repo",
		IssueNumber:   1,
		ProjectItemID: "item_1",
		LastStatus:    "📥 Inbox",
	}
	if err := e.st.Upsert(it); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// 2. 人間がボード上で Ready に移動したイベント
	err := e.handleStatusChanged(ctx, Event{
		Kind:   EvStatusChanged,
		Repo:   "owner/repo",
		Issue:  1,
		ItemID: "item_1",
		Status: "🎯 Ready",
		Prev:   "📥 Inbox",
	})
	if err != nil {
		t.Fatalf("handleStatusChanged failed: %v", err)
	}

	// 3. In Progress に遷移し、ジョブが投入されたか確認
	item, _ := e.st.Get("owner/repo", 1)
	if item.LastStatus != "🚧 In Progress" {
		t.Errorf("LastStatus = %q, want 🚧 In Progress", item.LastStatus)
	}

	// 4. ジョブを実行 (PhaseImplement)
	select {
	case job := <-e.jobs:
		if job.Phase != PhaseImplement {
			t.Fatalf("Job.Phase = %q, want %s", job.Phase, PhaseImplement)
		}
		e.runJob(ctx, job)
		e.release(job.Repo, job.Issue)
	case <-time.After(3 * time.Second):
		t.Fatal("Job が投入されませんでした")
	}

	// 5. PR が見つかり Verifying に遷移したか確認
	item, _ = e.st.Get("owner/repo", 1)
	if item.LastStatus != "🔍 Verifying" {
		t.Errorf("LastStatus = %q, want 🔍 Verifying", item.LastStatus)
	}
	if item.PRNumber != 2 {
		t.Errorf("PRNumber = %d, want 2", item.PRNumber)
	}

	// 6. Verifying の Tick 検証 (CI 成功 -> PhaseReview ジョブ投入)
	err = e.handleVerifyTick(ctx, Event{
		Kind:  EvVerifyTick,
		Repo:  "owner/repo",
		Issue: 1,
	})
	if err != nil {
		t.Fatalf("handleVerifyTick failed: %v", err)
	}

	// 7. PhaseReview ジョブを実行
	select {
	case job := <-e.jobs:
		if job.Phase != PhaseReview {
			t.Fatalf("Job.Phase = %q, want %s", job.Phase, PhaseReview)
		}
		e.runJob(ctx, job)
		e.release(job.Repo, job.Issue)
	case <-time.After(3 * time.Second):
		t.Fatal("PhaseReview ジョブが投入されませんでした")
	}

	// 8. セルフレビュー合格で In Review に到達したか確認
	item, _ = e.st.Get("owner/repo", 1)
	if item.LastStatus != "👀 In Review" {
		t.Errorf("LastStatus = %q, want 👀 In Review", item.LastStatus)
	}
}

// シナリオ 2: CI 失敗時のリトライとリトライ上限で Blocked
func TestScenario_CIFailure_RetriesAndBlocks(t *testing.T) {
	fake := &fakeGitHub{
		prState:      "OPEN",
		prCheckState: "FAILURE",
		linkedPRNum:  2,
	}
	script := `#!/bin/sh
cat > /dev/null
echo "AUTOPILOT_ACTION: PR_READY"
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	ctx := context.Background()

	it := &store.Item{
		Repo:          "owner/repo",
		IssueNumber:   1,
		ProjectItemID: "item_1",
		LastStatus:    "🔍 Verifying",
		PRNumber:      2,
		RetryCount:    4, // すでに4回リトライ済み
	}
	e.st.Upsert(it)

	// 5回目の CI 失敗
	err := e.handleVerifyTick(ctx, Event{Kind: EvVerifyTick, Repo: "owner/repo", Issue: 1})
	if err != nil {
		t.Fatalf("handleVerifyTick failed: %v", err)
	}

	// 5回目なのでまだギリギリ再試行（startImplement）
	item, _ := e.st.Get("owner/repo", 1)
	if item.LastStatus != "🚧 In Progress" {
		t.Errorf("LastStatus = %q, want 🚧 In Progress", item.LastStatus)
	}
	if item.RetryCount != 5 {
		t.Errorf("RetryCount = %d, want 5", item.RetryCount)
	}

	// ジョブを取り出して Verifying に戻す
	select {
	case job := <-e.jobs:
		e.runJob(ctx, job)
		e.release(job.Repo, job.Issue)
	default:
	}

	// 6回目の CI 失敗 (MaxRetries: 5 を超過)
	err = e.handleVerifyTick(ctx, Event{Kind: EvVerifyTick, Repo: "owner/repo", Issue: 1})
	if err != nil {
		t.Fatalf("handleVerifyTick failed: %v", err)
	}

	item, _ = e.st.Get("owner/repo", 1)
	if item.LastStatus != "⏸ Blocked" {
		t.Errorf("LastStatus = %q, want ⏸ Blocked", item.LastStatus)
	}
	if len(fake.comments) == 0 || !strings.Contains(fake.comments[len(fake.comments)-1], "Blocked") {
		t.Errorf("Blocked の理由コメントが投稿されていません: %v", fake.comments)
	}
}

// シナリオ 3: In Review での修正要求 -> In Progress への自動復帰
func TestScenario_ReviewNeedsFix_ReturnsToInProgress(t *testing.T) {
	fake := &fakeGitHub{
		prState:      "OPEN",
		prCheckState: "SUCCESS",
		linkedPRNum:  2,
		reviews: []gh.Review{
			{
				ID:    101,
				Body:  "Please fix error handling",
				State: "CHANGES_REQUESTED",
				User:  gh.User{Login: "reviewer-san"},
			},
		},
	}
	script := `#!/bin/sh
cat > /dev/null
echo "AUTOPILOT_ACTION: NEEDS_FIX"
echo "AUTOPILOT_REASON: Fixing error handling as requested"
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	ctx := context.Background()

	it := &store.Item{
		Repo:          "owner/repo",
		IssueNumber:   1,
		ProjectItemID: "item_1",
		LastStatus:    "👀 In Review",
		PRNumber:      2,
	}
	e.st.Upsert(it)

	// PR レビュー提出イベント
	err := e.handleReview(ctx, Event{
		Kind:  EvReview,
		Repo:  "owner/repo",
		Issue: 1,
	})
	if err != nil {
		t.Fatalf("handleReview failed: %v", err)
	}

	// PhaseTriageReview ジョブを処理
	select {
	case job := <-e.jobs:
		if job.Phase != PhaseTriageReview {
			t.Fatalf("Job.Phase = %q, want %s", job.Phase, PhaseTriageReview)
		}
		e.runJob(ctx, job)
		e.release(job.Repo, job.Issue)
	case <-time.After(3 * time.Second):
		t.Fatal("PhaseTriageReview ジョブが投入されませんでした")
	}

	// NEEDS_FIX なので In Progress に自動復帰しているか確認
	item, _ := e.st.Get("owner/repo", 1)
	if item.LastStatus != "🚧 In Progress" {
		t.Errorf("LastStatus = %q, want 🚧 In Progress", item.LastStatus)
	}
}

// シナリオ 4: Blocked 中に人間が助言コメントを投稿 -> In Progress に自動復帰
func TestScenario_BlockedResumeComment_ReturnsToInProgress(t *testing.T) {
	fake := &fakeGitHub{
		issueComments: []gh.Comment{
			{
				ID:        51,
				Body:      "ライブラリのバージョンを上げてみてください",
				User:      gh.User{Login: "developer-san"},
				CreatedAt: time.Now(),
			},
		},
	}
	script := `#!/bin/sh
cat > /dev/null
echo "AUTOPILOT_ACTION: RESUME"
echo "AUTOPILOT_REASON: Resuming with user advice"
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	ctx := context.Background()

	it := &store.Item{
		Repo:          "owner/repo",
		IssueNumber:   1,
		ProjectItemID: "item_1",
		LastStatus:    "⏸ Blocked",
		RetryCount:    5,
		LastCommentID: 50,
	}
	e.st.Upsert(it)

	// 新規コメント検知イベント
	err := e.handleComment(ctx, Event{
		Kind:  EvComment,
		Repo:  "owner/repo",
		Issue: 1,
	})
	if err != nil {
		t.Fatalf("handleComment failed: %v", err)
	}

	// PhaseTriageBlocked ジョブを処理
	select {
	case job := <-e.jobs:
		if job.Phase != PhaseTriageBlocked {
			t.Fatalf("Job.Phase = %q, want %s", job.Phase, PhaseTriageBlocked)
		}
		e.runJob(ctx, job)
		e.release(job.Repo, job.Issue)
	case <-time.After(3 * time.Second):
		t.Fatal("PhaseTriageBlocked ジョブが投入されませんでした")
	}

	// RESUME なので In Progress に戻り、RetryCount がリセットされているか確認
	item, _ := e.st.Get("owner/repo", 1)
	if item.LastStatus != "🚧 In Progress" {
		t.Errorf("LastStatus = %q, want 🚧 In Progress", item.LastStatus)
	}
	if item.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (リセット)", item.RetryCount)
	}
	if item.LastCommentID != 51 {
		t.Errorf("LastCommentID = %d, want 51", item.LastCommentID)
	}
}

// シナリオ 5: Issue クローズ時の終端処理
func TestScenario_ClosedEvent_SetsTerminal(t *testing.T) {
	fake := &fakeGitHub{}
	script := `#!/bin/sh
cat > /dev/null
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	ctx := context.Background()

	it := &store.Item{
		Repo:          "owner/repo",
		IssueNumber:   1,
		ProjectItemID: "item_1",
		LastStatus:    "👀 In Review",
		PRNumber:      2,
		Terminal:      false,
	}
	e.st.Upsert(it)

	err := e.handleClosed(ctx, Event{
		Kind:   EvClosed,
		Repo:   "owner/repo",
		Issue:  1,
		ItemID: "item_1",
	})
	if err != nil {
		t.Fatalf("handleClosed failed: %v", err)
	}

	item, _ := e.st.Get("owner/repo", 1)
	if !item.Terminal {
		t.Errorf("Terminal = false, want true")
	}
}

func mustRunGit(t *testing.T, ctx context.Context, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v (%s)", name, args, err, string(out))
	}
}
