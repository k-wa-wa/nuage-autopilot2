package web

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/store"
)

// WEB_PREVIEW=addr go test ./internal/web -run Preview で手元確認用に起動する。
func TestPreview(t *testing.T) {
	addr := os.Getenv("WEB_PREVIEW")
	if addr == "" {
		t.Skip("WEB_PREVIEW にアドレスを指定すると起動する")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "x.log")
	mustWrite(t, logPath, []byte("=== phase=implement adapter=claude cmd=claude -p dir=/ws/owner/repo at=2026-08-11T00:00:00Z ===\n"+
		agent.LogPromptSep+"\nあなたは実装エージェントである。\n\nIssue: ログイン画面の文言を直す\n\n完了したら AUTOPILOT_ACTION を出力せよ。\n"+
		agent.LogOutputSep+"\n実装しました。\nAUTOPILOT_ACTION: PR_READY\n"))

	now := time.Now()
	src := &fakeSource{
		logDir: dir,
		queue:  1,
		items: []*store.Item{
			{Repo: "k-wa-wa/example", IssueNumber: 12, LastStatus: "🚧 In Progress", Branch: "feat/login", RetryCount: 1, UpdatedAt: now},
			{Repo: "k-wa-wa/example", IssueNumber: 9, LastStatus: "🔍 Verifying", PRNumber: 31, Branch: "feat/api", VerifySince: now.Add(-20 * time.Minute), UpdatedAt: now.Add(-time.Minute)},
			{Repo: "k-wa-wa/example", IssueNumber: 4, LastStatus: "📥 Inbox", UpdatedAt: now.Add(-time.Hour)},
			{Repo: "k-wa-wa/example", IssueNumber: 2, LastStatus: "⏸ Blocked", PRNumber: 20, RetryCount: 5, UpdatedAt: now.Add(-3 * time.Hour)},
			{Repo: "k-wa-wa/other", IssueNumber: 1, LastStatus: "✅ Done", Terminal: true, PRNumber: 3, UpdatedAt: now.Add(-30 * time.Hour)},
		},
		runs: []*store.Run{
			{ID: 1, Repo: "k-wa-wa/example", IssueNumber: 12, Phase: "implement", StartedAt: now.Add(-8 * time.Minute), LogPath: logPath},
			{ID: 2, Repo: "k-wa-wa/example", IssueNumber: 9, Phase: "review", StartedAt: now.Add(-90 * time.Minute), EndedAt: now.Add(-88 * time.Minute), Result: "ok", LogPath: logPath},
			{ID: 3, Repo: "k-wa-wa/example", IssueNumber: 2, Phase: "implement", StartedAt: now.Add(-4 * time.Hour), EndedAt: now.Add(-3 * time.Hour), Result: "error: エージェントが 2h0m0s でタイムアウトしました (phase: implement)", LogPath: logPath},
			{ID: 4, Repo: "k-wa-wa/other", IssueNumber: 1, Phase: "review", StartedAt: now.Add(-31 * time.Hour), EndedAt: now.Add(-30 * time.Hour), Result: "ok"},
		},
		active: &Active{RunID: 1, Phase: "implement", Repo: "k-wa-wa/example", Issue: 12,
			StartedAt: now.Add(-8 * time.Minute), AgentStartedAt: now.Add(-7 * time.Minute), LogPath: logPath},
	}
	t.Logf("preview: http://%s", addr)
	http.ListenAndServe(addr, New(src, slog.New(slog.NewTextHandler(os.Stderr, nil))))
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
