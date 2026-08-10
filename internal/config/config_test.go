package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func examplePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller を取得できません")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "config.example.yaml")
}

func TestLoadExample(t *testing.T) {
	c, err := Load(examplePath(t))
	if err != nil {
		t.Fatalf("example 設定の読み込みに失敗: %v", err)
	}
	if c.Project.Owner != "k-wa-wa" || c.Project.Number != 1 {
		t.Errorf("project の読み込みが不正: %+v", c.Project)
	}
	if len(c.Repos) != 1 || c.Repos[0].String() != "k-wa-wa/example-repo" {
		t.Errorf("repos の読み込みが不正: %+v", c.Repos)
	}
	if c.Poll.Project != 60*time.Second {
		t.Errorf("poll.project_interval = %v, want 60s", c.Poll.Project)
	}
	if c.Limits.MaxRetries != 5 {
		t.Errorf("limits.max_retries = %d, want 5", c.Limits.MaxRetries)
	}
	if !filepath.IsAbs(c.Workspace) || !filepath.IsAbs(c.Database) {
		t.Errorf("パスが絶対パスに展開されていません: %q %q", c.Workspace, c.Database)
	}
	if !c.HasRepo("k-wa-wa", "example-repo") {
		t.Error("HasRepo が対象リポジトリを認識していません")
	}
	if c.HasRepo("k-wa-wa", "other") {
		t.Error("HasRepo が対象外リポジトリを true にしています")
	}
}

func TestAgentForFillsTimeout(t *testing.T) {
	c, err := Load(examplePath(t))
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	if got := c.AgentFor(AgentImplement).Timeout; got != c.Limits.ImplementTimeout {
		t.Errorf("implement の timeout = %v, want %v", got, c.Limits.ImplementTimeout)
	}
	if got := c.AgentFor(AgentReview).Timeout; got != c.Limits.AgentTimeout {
		t.Errorf("review の timeout = %v, want %v", got, c.Limits.AgentTimeout)
	}
}

func TestValidateRejectsMissingProject(t *testing.T) {
	c := &Config{Repos: []Repo{{Owner: "a", Name: "b"}}}
	if err := c.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := c.validate(); err == nil {
		t.Error("project 未指定でもエラーになりませんでした")
	}
}

func TestAgentCommandDefaultsToClaude(t *testing.T) {
	c, err := Load(examplePath(t))
	if err != nil {
		t.Fatal(err)
	}
	// example では review だけ agy にしている。
	if got := c.AgentFor(AgentReview).Command; got != "agy" {
		t.Errorf("review の command = %q, want agy", got)
	}
	if got := c.AgentFor(AgentImplement).Command; got != "claude" {
		t.Errorf("implement の command = %q, want claude", got)
	}
	// agents: に書かれていない用途も claude で埋まる。
	c2 := &Config{
		Project: Project{Owner: "o", Number: 1},
		Repos:   []Repo{{Owner: "o", Name: "r"}},
	}
	if err := c2.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	for _, u := range AgentUses {
		if got := c2.Agents[u].Command; got != "claude" {
			t.Errorf("%s の command = %q, want claude", u, got)
		}
	}
}

// 用途キーの打ち間違いは黙って無視されるので、起動時に弾く。
func TestValidateRejectsUnknownAgentUse(t *testing.T) {
	c := &Config{
		Project: Project{Owner: "o", Number: 1},
		Repos:   []Repo{{Owner: "o", Name: "r"}},
		Agents:  map[string]Agent{"implment": {Command: "claude"}},
	}
	if err := c.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	err := c.validate()
	if err == nil {
		t.Fatal("未知の用途キーがエラーになりません")
	}
	if !strings.Contains(err.Error(), "implment") {
		t.Errorf("どのキーか示されていません: %v", err)
	}
}

func TestSpecCarriesAgentSettings(t *testing.T) {
	a := Agent{Command: "/opt/agy", Model: "m", Args: []string{"--x"}, Timeout: time.Minute}
	s := a.Spec()
	if s.ResolvedCommand() != "/opt/agy" || s.Model != "m" ||
		len(s.ExtraArgs) != 1 || s.Timeout != time.Minute {
		t.Errorf("Spec への変換が不正: %+v", s)
	}
	// command からアダプタが解決されること。
	if got := s.Adapter().Name(); got != "agy" {
		t.Errorf("解決されたアダプタ = %s, want agy", got)
	}
}

func TestWebAddrDefaults(t *testing.T) {
	// 未指定なら既定のループバック。
	var c Config
	if got := c.Web.Listen(); got != DefaultWebAddr {
		t.Errorf("未指定時 = %q, want %q", got, DefaultWebAddr)
	}

	// 明示的な空文字は「起動しない」の意味であり、既定で埋めてはならない。
	empty := ""
	c.Web.Addr = &empty
	if got := c.Web.Listen(); got != "" {
		t.Errorf("空文字指定が既定で上書きされています: %q", got)
	}

	addr := "0.0.0.0:9000"
	c.Web.Addr = &addr
	if got := c.Web.Listen(); got != addr {
		t.Errorf("指定値 = %q, want %q", got, addr)
	}
}

func TestValidateRejectsBadWebAddr(t *testing.T) {
	bad := "8080"
	c := &Config{
		Project: Project{Owner: "o", Number: 1},
		Repos:   []Repo{{Owner: "o", Name: "r"}},
		Web:     Web{Addr: &bad},
	}
	if err := c.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	err := c.validate()
	if err == nil || !strings.Contains(err.Error(), "web.addr") {
		t.Errorf("host:port でない値が通っています: %v", err)
	}

	// 空文字（無効化）は検証を通ること。
	empty := ""
	c.Web.Addr = &empty
	if err := c.validate(); err != nil {
		t.Errorf("無効化の指定が弾かれています: %v", err)
	}
}

// YAML から web.addr が読めること。
func TestLoadReadsWebAddr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	body := "project:\n  owner: o\n  number: 1\nrepos:\n  - owner: o\n    name: r\nweb:\n  addr: \"127.0.0.1:9999\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load に失敗: %v", err)
	}
	if got := c.Web.Listen(); got != "127.0.0.1:9999" {
		t.Errorf("web.addr = %q", got)
	}
}
