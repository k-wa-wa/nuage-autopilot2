// Package config はワーカーの設定ファイル（YAML）を読み込む。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"nuage-autopilot2/internal/agent"
)

// Config はワーカー全体の設定。
type Config struct {
	// Workspace は対象リポジトリの clone を置くディレクトリ。
	Workspace string `yaml:"workspace"`
	// Database は SQLite ファイルのパス。
	Database string `yaml:"database"`
	// GateFile は対象リポジトリ内の品質ゲート定義ファイルの相対パス。
	GateFile string `yaml:"gate_file"`

	Project  Project           `yaml:"project"`
	Repos    []Repo            `yaml:"repos"`
	Poll     Poll              `yaml:"poll"`
	Statuses Statuses          `yaml:"statuses"`
	Limits   Limits            `yaml:"limits"`
	Agents   map[string]Agent  `yaml:"agents"`
	Env      map[string]string `yaml:"env"`
}

// Project は監視対象の GitHub Projects v2。
type Project struct {
	Owner     string `yaml:"owner"`
	Number    int    `yaml:"number"`
	OwnerType string `yaml:"owner_type"` // "user" or "organization"
	// StatusField は Single select フィールドの名前。
	StatusField string `yaml:"status_field"`
}

// Repo は監視対象リポジトリ。
type Repo struct {
	Owner string `yaml:"owner"`
	Name  string `yaml:"name"`
}

// String は "owner/name" 形式を返す。
func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Poll は各ポーリングループの間隔。
type Poll struct {
	Project      time.Duration `yaml:"project_interval"`
	Notification time.Duration `yaml:"notification_interval"`
	Reconcile    time.Duration `yaml:"reconcile_interval"`
	// Verify は Verifying レーンの CI 確認間隔。
	Verify time.Duration `yaml:"verify_interval"`
}

// Statuses は Project の Status フィールドの選択肢名。絵文字を含む表示名をそのまま書く。
type Statuses struct {
	Inbox      string `yaml:"inbox"`
	Ready      string `yaml:"ready"`
	InProgress string `yaml:"in_progress"`
	Verifying  string `yaml:"verifying"`
	InReview   string `yaml:"in_review"`
	Blocked    string `yaml:"blocked"`
	Done       string `yaml:"done"`
}

// Limits はリトライ回数とタイムアウト。
type Limits struct {
	MaxRetries       int           `yaml:"max_retries"`
	ImplementTimeout time.Duration `yaml:"implement_timeout"`
	AgentTimeout     time.Duration `yaml:"agent_timeout"`
	VerifyWait       time.Duration `yaml:"verify_wait_timeout"`
	// ContextComments はプロンプトに含める直近コメント数。
	ContextComments int `yaml:"context_comments"`
}

// Agent は 1 つの用途で使うエージェント CLI の設定。
//
// 起動方法（プロンプトを標準入力で渡すか argv で渡すか等）は Command から
// 解決したアダプタが決める。Args はアダプタが組み立てた引数の後ろに付く追加分。
type Agent struct {
	// Command は実行するコマンド。パスでもよい。空なら claude。
	Command string `yaml:"command"`
	// Model は使用するモデル。空なら CLI の既定に任せる。
	Model string `yaml:"model"`
	// Args はアダプタが組み立てた引数に追加する分。
	Args []string          `yaml:"args"`
	Env  map[string]string `yaml:"env"`
	// Timeout が 0 の場合は Limits の既定値を使う。
	Timeout time.Duration `yaml:"timeout"`
}

// Spec はランタイム用の起動設定に変換する。
func (a Agent) Spec() agent.Spec {
	return agent.Spec{
		Command:   a.Command,
		Model:     a.Model,
		ExtraArgs: a.Args,
		Env:       a.Env,
		Timeout:   a.Timeout,
	}
}

// エージェントの用途キー。設定ファイルの agents: 配下のキーと対応する。
const (
	AgentRefine    = "refine"
	AgentImplement = "implement"
	AgentReview    = "review"
	AgentTriage    = "triage"
)

// AgentUses は設定を持つ用途の一覧。
var AgentUses = []string{AgentRefine, AgentImplement, AgentReview, AgentTriage}

// Load は設定ファイルを読み、既定値の補完と検証を行う。
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("設定ファイルの解析に失敗: %w", err)
	}
	if err := c.applyDefaults(); err != nil {
		return nil, err
	}
	return &c, c.validate()
}

func (c *Config) applyDefaults() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	expand := func(p, def string) string {
		if p == "" {
			p = def
		}
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		return abs
	}
	c.Workspace = expand(c.Workspace, "~/.autopilot/workspaces")
	c.Database = expand(c.Database, "~/.autopilot/autopilot.db")

	setDefault(&c.GateFile, ".agents/autopilot-gate.md")
	setDefault(&c.Project.OwnerType, "user")
	setDefault(&c.Project.StatusField, "Status")

	setDefault(&c.Statuses.Inbox, "📥 Inbox")
	setDefault(&c.Statuses.Ready, "🎯 Ready")
	setDefault(&c.Statuses.InProgress, "🚧 In Progress")
	setDefault(&c.Statuses.Verifying, "🔍 Verifying")
	setDefault(&c.Statuses.InReview, "👀 In Review")
	setDefault(&c.Statuses.Blocked, "⏸ Blocked")
	setDefault(&c.Statuses.Done, "✅ Done")

	setDefaultDuration(&c.Poll.Project, 60*time.Second)
	setDefaultDuration(&c.Poll.Notification, 60*time.Second)
	setDefaultDuration(&c.Poll.Reconcile, 10*time.Minute)
	setDefaultDuration(&c.Poll.Verify, 60*time.Second)

	if c.Limits.MaxRetries == 0 {
		c.Limits.MaxRetries = 5
	}
	if c.Limits.ContextComments == 0 {
		c.Limits.ContextComments = 10
	}
	setDefaultDuration(&c.Limits.ImplementTimeout, 2*time.Hour)
	setDefaultDuration(&c.Limits.AgentTimeout, 30*time.Minute)
	setDefaultDuration(&c.Limits.VerifyWait, 1*time.Hour)

	if c.Agents == nil {
		c.Agents = map[string]Agent{}
	}
	// 既定はすべて claude。設定で用途ごとに差し替えられる。
	// 非対話・権限スキップなどの必須フラグはアダプタが付けるので、ここでは指定しない。
	for _, u := range AgentUses {
		a := c.Agents[u]
		if a.Command == "" {
			a.Command = agent.DefaultCommand
		}
		c.Agents[u] = a
	}
	return nil
}

func (c *Config) validate() error {
	if c.Project.Owner == "" || c.Project.Number == 0 {
		return fmt.Errorf("project.owner と project.number は必須")
	}
	if c.Project.OwnerType != "user" && c.Project.OwnerType != "organization" {
		return fmt.Errorf("project.owner_type は user か organization")
	}
	if len(c.Repos) == 0 {
		return fmt.Errorf("repos が空")
	}
	for _, r := range c.Repos {
		if r.Owner == "" || r.Name == "" {
			return fmt.Errorf("repos の要素に owner/name が不足")
		}
	}
	// 用途キーの打ち間違いは黙って無視されるため、起動時に弾く。
	known := map[string]bool{}
	for _, u := range AgentUses {
		known[u] = true
	}
	for name := range c.Agents {
		if !known[name] {
			return fmt.Errorf("agents.%s は未知の用途です（利用可能: %s）", name, strings.Join(AgentUses, ", "))
		}
	}
	return nil
}

// AgentFor は用途に対応するエージェント定義を返す。Timeout 未設定なら既定値を埋める。
func (c *Config) AgentFor(kind string) Agent {
	a := c.Agents[kind]
	if a.Timeout == 0 {
		if kind == AgentImplement {
			a.Timeout = c.Limits.ImplementTimeout
		} else {
			a.Timeout = c.Limits.AgentTimeout
		}
	}
	return a
}

// HasRepo は監視対象リポジトリかどうかを返す。
func (c *Config) HasRepo(owner, name string) bool {
	for _, r := range c.Repos {
		if strings.EqualFold(r.Owner, owner) && strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

func setDefault(p *string, v string) {
	if *p == "" {
		*p = v
	}
}

func setDefaultDuration(p *time.Duration, v time.Duration) {
	if *p == 0 {
		*p = v
	}
}
