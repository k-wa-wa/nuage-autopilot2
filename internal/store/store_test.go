package store

import (
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
