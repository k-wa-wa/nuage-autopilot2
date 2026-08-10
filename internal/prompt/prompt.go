// Package prompt はエージェントに渡すプロンプトを組み立てる。
//
// プロンプト本文は templates/*.tmpl に text/template として置き、バイナリに
// 埋め込む。Go 側は Context の組み立てとテンプレートの実行だけを担う。
//
// GitHub 側の内容（Issue 本文・コメント・PR）の更新はエージェントが gh コマンドで
// 行う。ワーカーは Project の Status のみを管理する（責務の分離）。
package prompt

import (
	"embed"
	"strings"
	"text/template"
	"time"

	"nuage-autopilot2/internal/gh"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// tmpl は埋め込みテンプレートを起動時に一括で解析したもの。
// テンプレートが壊れていれば起動時点で panic する（実行時に黙って壊れるより良い）。
var tmpl = template.Must(template.New("prompt").Funcs(funcs).ParseFS(templatesFS, "templates/*.tmpl"))

var funcs = template.FuncMap{
	"trim": strings.TrimSpace,
	"ts":   func(t time.Time) string { return t.Format(time.RFC3339) },
	// issueBody は Issue 本文を整形する。空ならプレースホルダを返す。
	"issueBody": func(i *gh.Issue) string {
		if body := strings.TrimSpace(i.Body); body != "" {
			return body
		}
		return "(本文なし)"
	},
}

// Context はプロンプト生成に必要な情報。
type Context struct {
	Repo                 string
	Issue                *gh.Issue
	Comments             []gh.Comment
	ReviewComments       []gh.ReviewComment // PR の diff に付いた行コメント
	NewInputs            []string           // 今回の起床要因となった人間の発言
	PRNumber             int
	Gate                 string // 品質ゲート定義ファイルの内容
	GatePath             string
	RetryCount           int
	MaxRetries           int
	CIHint               string // CI 失敗などの追加情報
	ProjectOwner         string
	ProjectNumber        int
	ProjectID            string
	ProjectStatusFieldID string
	ProjectInboxOptionID string
	StatusInbox          string
}

// HasProject は子 Issue を追加すべき Project が特定できているかを返す。
func (c Context) HasProject() bool { return c.ProjectOwner != "" && c.ProjectNumber > 0 }

// HasProjectIDs は Status を API で直接設定できるだけの ID が揃っているかを返す。
func (c Context) HasProjectIDs() bool {
	return c.ProjectID != "" && c.ProjectStatusFieldID != "" && c.ProjectInboxOptionID != ""
}

// InboxStatusName は Inbox レーンの表示名を返す。未設定なら既定値。
func (c Context) InboxStatusName() string {
	if c.StatusInbox != "" {
		return c.StatusInbox
	}
	return "Inbox"
}

// render は名前付きテンプレートを実行して文字列にする。
// テンプレートは起動時に検証済みで、データも実行時に増減しないため、
// ここでのエラーは発生し得ない（発生したらプログラムのバグ）。
func render(name string, c Context) string {
	var b strings.Builder
	if err := tmpl.ExecuteTemplate(&b, name, c); err != nil {
		panic("prompt: テンプレート " + name + " の実行に失敗: " + err.Error())
	}
	return b.String()
}

// Refine は Inbox での仕様精緻化プロンプトを組み立てる。
func Refine(c Context) string { return render("refine", c) }

// Implement は実装フェーズのプロンプトを組み立てる。
func Implement(c Context) string { return render("implement", c) }

// Review は Verifying でのセルフレビュープロンプトを組み立てる。
func Review(c Context) string { return render("review", c) }

// TriageMode は triage プロンプトの動作モード。
type TriageMode int

// triage の対象レーン。
const (
	// TriageReview は In Review でレビュー提出を受けた場合。
	TriageReview TriageMode = iota
	// TriageBlocked は Blocked で助言コメントを受けた場合。
	TriageBlocked
)

// triageTemplates はモードごとのテンプレート名。
var triageTemplates = map[TriageMode]string{
	TriageReview:  "triage_review",
	TriageBlocked: "triage_blocked",
}

// Triage は In Review / Blocked での人間の発言に対する判断プロンプトを組み立てる。
func Triage(c Context, mode TriageMode) string {
	name, ok := triageTemplates[mode]
	if !ok {
		name = "triage_bare"
	}
	return render(name, c)
}
