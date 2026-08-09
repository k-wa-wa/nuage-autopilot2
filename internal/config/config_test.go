package config

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func examplePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller を取得できません")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "nuage.example.yaml")
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
