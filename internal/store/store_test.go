package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open に失敗: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertAndGet(t *testing.T) {
	s := openTemp(t)

	empty, err := s.IsEmpty()
	if err != nil || !empty {
		t.Fatalf("初期状態が空ではありません: empty=%v err=%v", empty, err)
	}

	lease := time.Now().Add(time.Hour).Truncate(time.Second)
	in := &Item{
		Repo: "o/r", IssueNumber: 7, ProjectItemID: "PVTI_x", LastStatus: "🚧 In Progress",
		LastCommentID: 42, PRNumber: 9, Branch: "feat/x", RetryCount: 2, LeaseUntil: lease,
	}
	if err := s.Upsert(in); err != nil {
		t.Fatalf("Upsert に失敗: %v", err)
	}

	got, err := s.Get("o/r", 7)
	if err != nil || got == nil {
		t.Fatalf("Get に失敗: %v (got=%v)", err, got)
	}
	if got.LastStatus != in.LastStatus || got.LastCommentID != 42 || got.PRNumber != 9 ||
		got.Branch != "feat/x" || got.RetryCount != 2 {
		t.Errorf("往復で値が壊れています: %+v", got)
	}
	if !got.LeaseUntil.Equal(lease) {
		t.Errorf("LeaseUntil = %v, want %v", got.LeaseUntil, lease)
	}
	if !got.VerifySince.IsZero() {
		t.Errorf("未設定の時刻がゼロ値になっていません: %v", got.VerifySince)
	}

	// 更新が上書きされること。
	got.RetryCount = 3
	got.Terminal = true
	if err := s.Upsert(got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Get("o/r", 7)
	if again.RetryCount != 3 || !again.Terminal {
		t.Errorf("更新が反映されていません: %+v", again)
	}
}

func TestGetMissingReturnsNil(t *testing.T) {
	s := openTemp(t)
	got, err := s.Get("o/r", 1)
	if err != nil {
		t.Fatalf("存在しない item でエラー: %v", err)
	}
	if got != nil {
		t.Errorf("存在しない item が返りました: %+v", got)
	}
}

func TestListByStatusExcludesTerminal(t *testing.T) {
	s := openTemp(t)
	items := []*Item{
		{Repo: "o/r", IssueNumber: 1, LastStatus: "🔍 Verifying"},
		{Repo: "o/r", IssueNumber: 2, LastStatus: "🔍 Verifying", Terminal: true},
		{Repo: "o/r", IssueNumber: 3, LastStatus: "👀 In Review"},
	}
	for _, it := range items {
		if err := s.Upsert(it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListByStatus("🔍 Verifying")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IssueNumber != 1 {
		t.Errorf("終端の item が除外されていません: %+v", got)
	}
}

func TestCursors(t *testing.T) {
	s := openTemp(t)
	v, err := s.Cursor("missing")
	if err != nil || v != "" {
		t.Fatalf("未設定カーソルが空文字になりません: %q %v", v, err)
	}
	if err := s.SetCursor("notifications:since", "2026-08-09T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor("notifications:since", "2026-08-09T01:00:00Z"); err != nil {
		t.Fatal(err)
	}
	v, err = s.Cursor("notifications:since")
	if err != nil || v != "2026-08-09T01:00:00Z" {
		t.Errorf("カーソルが更新されていません: %q %v", v, err)
	}
}

func TestRuns(t *testing.T) {
	s := openTemp(t)
	id, err := s.StartRun("o/r", 1, "implement")
	if err != nil || id == 0 {
		t.Fatalf("StartRun に失敗: id=%d err=%v", id, err)
	}
	if err := s.EndRun(id, "ok"); err != nil {
		t.Fatalf("EndRun に失敗: %v", err)
	}
}

func TestRunHistory(t *testing.T) {
	s := openTemp(t)

	// 同じ Issue で 2 回、別の Issue で 1 回。
	first, _ := s.StartRun("o/r", 1, "implement")
	if err := s.SetRunLog(first, "/logs/a.log"); err != nil {
		t.Fatalf("SetRunLog に失敗: %v", err)
	}
	if err := s.EndRun(first, "ok"); err != nil {
		t.Fatal(err)
	}
	second, _ := s.StartRun("o/r", 1, "review")
	other, _ := s.StartRun("o/r", 2, "refine")
	if err := s.EndRun(other, "error: 失敗した"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetRun(first)
	if err != nil || got == nil {
		t.Fatalf("GetRun に失敗: %v (got=%v)", err, got)
	}
	if got.LogPath != "/logs/a.log" || got.Result != "ok" || got.Phase != "implement" {
		t.Errorf("往復で値が壊れています: %+v", got)
	}
	if got.StartedAt.IsZero() || got.EndedAt.IsZero() {
		t.Errorf("時刻が入っていません: %+v", got)
	}

	missing, err := s.GetRun(9999)
	if err != nil || missing != nil {
		t.Errorf("存在しない run が返りました: %v %v", missing, err)
	}

	// 実行中の行は ended_at がゼロ値のまま。
	running, _ := s.GetRun(second)
	if !running.EndedAt.IsZero() {
		t.Errorf("未終了の run に終了時刻が入っています: %v", running.EndedAt)
	}

	runs, err := s.ListRuns("o/r", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != second {
		t.Fatalf("Issue の履歴が新しい順になっていません: %+v", runs)
	}

	// limit が効くこと。
	runs, _ = s.ListRuns("o/r", 1, 1)
	if len(runs) != 1 {
		t.Errorf("limit が効いていません: %d 件", len(runs))
	}

	latest, err := s.LatestRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("Issue ごとに 1 件になっていません: %+v", latest)
	}
	for _, r := range latest {
		if r.IssueNumber == 1 && r.ID != second {
			t.Errorf("Issue 1 の最新が古い方です: %+v", r)
		}
	}
}

// log_path を持たない旧 DB を開いても、履歴を捨てずに列だけが足されること。
func TestMigrationAddsLogPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repo TEXT NOT NULL, issue_number INTEGER NOT NULL, phase TEXT NOT NULL,
		started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL DEFAULT 0,
		result TEXT NOT NULL DEFAULT ''
	);
	INSERT INTO runs (repo, issue_number, phase, started_at, ended_at, result)
	VALUES ('o/r', 5, 'implement', 100, 200, 'ok');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("旧スキーマの DB を開けません: %v", err)
	}
	defer s.Close()

	runs, err := s.ListRuns("o/r", 5, 10)
	if err != nil {
		t.Fatalf("移行後に読めません: %v", err)
	}
	if len(runs) != 1 || runs[0].Phase != "implement" || runs[0].LogPath != "" {
		t.Errorf("既存の履歴が保たれていません: %+v", runs)
	}

	// 追加された列に書けること。
	if err := s.SetRunLog(runs[0].ID, "/logs/x.log"); err != nil {
		t.Fatalf("移行後に log_path を書けません: %v", err)
	}
	got, _ := s.GetRun(runs[0].ID)
	if got.LogPath != "/logs/x.log" {
		t.Errorf("log_path = %q", got.LogPath)
	}
}
