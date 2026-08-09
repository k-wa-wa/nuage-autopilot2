package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRemote は origin となるベアリポジトリを作り、初期コミットを 1 つ置く。
func setupRemote(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	bare := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	mustRun(t, ctx, dir, "git", "init", "--bare", "--initial-branch=main", bare)
	mustRun(t, ctx, dir, "git", "init", "--initial-branch=main", seed)
	mustRun(t, ctx, seed, "git", "config", "user.email", "t@example.com")
	mustRun(t, ctx, seed, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, ctx, seed, "git", "add", ".")
	mustRun(t, ctx, seed, "git", "commit", "-m", "init")
	mustRun(t, ctx, seed, "git", "remote", "add", "origin", bare)
	mustRun(t, ctx, seed, "git", "push", "-u", "origin", "main")
	return bare
}

// setupManager は bare を origin として clone 済みのワークスペースを用意する。
func setupManager(t *testing.T, bare string) (*Manager, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	m := New(root, "GH_TOKEN", "tester", "tester@example.com")
	repo := "o/r"
	dest := m.Path(repo)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, ctx, root, "git", "clone", bare, dest)
	return m, repo
}

func TestPrepareResetsToDefaultBranch(t *testing.T) {
	ctx := context.Background()
	bare := setupRemote(t)
	m, repo := setupManager(t, bare)

	// クラッシュの残骸を模して、未コミットの変更と未追跡ファイルを置く。
	if err := os.WriteFile(filepath.Join(m.Path(repo), "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Path(repo), "junk.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	branch, err := m.Prepare(ctx, repo, "", nil)
	if err != nil {
		t.Fatalf("Prepare に失敗: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}
	b, err := os.ReadFile(filepath.Join(m.Path(repo), "README.md"))
	if err != nil || strings.TrimSpace(string(b)) != "hello" {
		t.Errorf("変更が破棄されていません: %q %v", string(b), err)
	}
	if _, err := os.Stat(filepath.Join(m.Path(repo), "junk.txt")); !os.IsNotExist(err) {
		t.Error("未追跡ファイルが削除されていません")
	}
}

func TestPrepareChecksOutExistingBranch(t *testing.T) {
	ctx := context.Background()
	bare := setupRemote(t)
	m, repo := setupManager(t, bare)
	dir := m.Path(repo)

	// エージェントが作った作業ブランチを模して push しておく。
	mustRun(t, ctx, dir, "git", "config", "user.email", "t@example.com")
	mustRun(t, ctx, dir, "git", "config", "user.name", "t")
	mustRun(t, ctx, dir, "git", "checkout", "-b", "feat/x")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, ctx, dir, "git", "add", ".")
	mustRun(t, ctx, dir, "git", "commit", "-m", "work")
	mustRun(t, ctx, dir, "git", "push", "-u", "origin", "feat/x")
	mustRun(t, ctx, dir, "git", "checkout", "main")

	branch, err := m.Prepare(ctx, repo, "feat/x", nil)
	if err != nil {
		t.Fatalf("Prepare に失敗: %v", err)
	}
	if branch != "feat/x" {
		t.Fatalf("branch = %q, want feat/x", branch)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Errorf("作業ブランチの内容が復元されていません: %v", err)
	}
	cur, err := m.CurrentBranch(ctx, repo, nil)
	if err != nil || cur != "feat/x" {
		t.Errorf("CurrentBranch = %q (%v)", cur, err)
	}
}

func TestPrepareFallsBackWhenBranchMissing(t *testing.T) {
	ctx := context.Background()
	bare := setupRemote(t)
	m, repo := setupManager(t, bare)

	// リモートに存在しないブランチを指定すると既定ブランチへ落ちる。
	branch, err := m.Prepare(ctx, repo, "feat/deleted", nil)
	if err != nil {
		t.Fatalf("Prepare に失敗: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}
}

func TestReadFile(t *testing.T) {
	bare := setupRemote(t)
	m, repo := setupManager(t, bare)
	if got := strings.TrimSpace(m.ReadFile(repo, "README.md")); got != "hello" {
		t.Errorf("ReadFile = %q", got)
	}
	if got := m.ReadFile(repo, ".agents/autopilot-gate.md"); got != "" {
		t.Errorf("存在しないファイルが空文字になりません: %q", got)
	}
}

func mustRun(t *testing.T, ctx context.Context, dir, name string, args ...string) {
	t.Helper()
	if out, err := run(ctx, dir, nil, name, args...); err != nil {
		t.Fatalf("%s %v: %v (%s)", name, args, err, out)
	}
}
