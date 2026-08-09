// Package workspace は対象リポジトリのローカル clone を管理する。
//
// 起動時に全リポジトリを clone し、エージェント実行の直前に origin の最新へ
// 巻き戻す。ブランチ名はワーカー側で指定せず、エージェントが作ったブランチを
// PR 経由で発見して再チェックアウトする。
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manager はワークスペースのルートディレクトリを管理する。
type Manager struct {
	root      string
	gitName   string
	gitEmail  string
	tokenEnv  string
	cloneOpts []string
}

// New は Manager を作る。tokenEnv は認証に使う環境変数名（例: GH_TOKEN）。
func New(root, tokenEnv, gitName, gitEmail string) *Manager {
	if gitName == "" {
		gitName = "nuage-autopilot"
	}
	if gitEmail == "" {
		gitEmail = "nuage-autopilot@users.noreply.github.com"
	}
	return &Manager{root: root, tokenEnv: tokenEnv, gitName: gitName, gitEmail: gitEmail}
}

// Path は "owner/name" に対応するローカルパスを返す。
func (m *Manager) Path(repo string) string {
	return filepath.Join(m.root, filepath.FromSlash(repo))
}

// EnsureAll は全リポジトリが clone 済みであることを保証する。
func (m *Manager) EnsureAll(ctx context.Context, repos []string) error {
	for _, r := range repos {
		if err := m.Ensure(ctx, r); err != nil {
			return fmt.Errorf("%s の clone に失敗: %w", r, err)
		}
	}
	return nil
}

// Ensure は clone 済みでなければ clone し、認証設定を適用する。
func (m *Manager) Ensure(ctx context.Context, repo string) error {
	dir := m.Path(repo)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return m.configure(ctx, repo)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	url := "https://github.com/" + repo + ".git"
	if _, err := run(ctx, filepath.Dir(dir), nil, "git", "clone", url, filepath.Base(dir)); err != nil {
		return err
	}
	return m.configure(ctx, repo)
}

// configure はローカル repo に認証ヘルパとコミット者情報を設定する。
//
// credential.helper は環境変数からトークンを読むシェル関数として登録するため、
// トークンがディスクに残らない。エージェントが自分で git push する際にも効く。
func (m *Manager) configure(ctx context.Context, repo string) error {
	dir := m.Path(repo)
	helper := fmt.Sprintf(`!f() { echo "username=x-access-token"; echo "password=${%s}"; }; f`, m.tokenEnv)
	settings := [][]string{
		{"credential.helper", helper},
		{"user.name", m.gitName},
		{"user.email", m.gitEmail},
	}
	for _, kv := range settings {
		if _, err := run(ctx, dir, nil, "git", "config", "--local", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// Prepare は実行直前にワークツリーを整える。
//
// branch が空なら既定ブランチの最新へ、非空ならそのリモートブランチの最新へ
// 強制的に合わせる。前回のクラッシュで残った変更は破棄する。
func (m *Manager) Prepare(ctx context.Context, repo, branch string, env []string) (string, error) {
	dir := m.Path(repo)
	if err := m.Ensure(ctx, repo); err != nil {
		return "", err
	}
	if _, err := run(ctx, dir, env, "git", "fetch", "--prune", "origin"); err != nil {
		return "", err
	}
	// 未コミットの残骸を破棄してから切り替える。
	if _, err := run(ctx, dir, env, "git", "reset", "--hard"); err != nil {
		return "", err
	}
	if _, err := run(ctx, dir, env, "git", "clean", "-fd"); err != nil {
		return "", err
	}

	target := branch
	if target != "" {
		if _, err := run(ctx, dir, env, "git", "rev-parse", "--verify", "--quiet", "origin/"+target); err != nil {
			// リモートに無いブランチは既定ブランチにフォールバックする。
			target = ""
		}
	}
	if target == "" {
		def, err := m.DefaultBranch(ctx, repo, env)
		if err != nil {
			return "", err
		}
		target = def
	}
	if _, err := run(ctx, dir, env, "git", "checkout", "-B", target, "origin/"+target); err != nil {
		return "", err
	}
	if _, err := run(ctx, dir, env, "git", "reset", "--hard", "origin/"+target); err != nil {
		return "", err
	}
	return target, nil
}

// DefaultBranch は origin の既定ブランチ名を返す。
func (m *Manager) DefaultBranch(ctx context.Context, repo string, env []string) (string, error) {
	dir := m.Path(repo)
	out, err := run(ctx, dir, env, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		if s := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); s != "" {
			return s, nil
		}
	}
	// clone 時に HEAD が張られていない場合は再取得を試みる。
	if _, err := run(ctx, dir, env, "git", "remote", "set-head", "origin", "--auto"); err == nil {
		out, err := run(ctx, dir, env, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err == nil {
			if s := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); s != "" {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("%s の既定ブランチを特定できません", repo)
}

// CurrentBranch は現在チェックアウトしているブランチ名を返す。
func (m *Manager) CurrentBranch(ctx context.Context, repo string, env []string) (string, error) {
	out, err := run(ctx, m.Path(repo), env, "git", "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// ReadFile はワークスペース内の相対パスのファイルを読む。存在しなければ空文字。
func (m *Manager) ReadFile(repo, rel string) string {
	b, err := os.ReadFile(filepath.Join(m.Path(repo), filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(b)
}

func run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
