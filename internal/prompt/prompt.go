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
	"fmt"
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
	Repo           string
	Issue          *gh.Issue
	Comments       []gh.Comment
	ReviewComments []gh.ReviewComment // PR の diff に付いた行コメント
	NewInputs      []string           // 今回の起床要因となった人間の発言
	PRNumber       int
	Gate           string // 品質ゲート定義ファイルの内容
	GatePath       string
	RetryCount     int
	MaxRetries     int
	CIHint         string // CI 失敗などの追加情報
	// Project は子 Issue を追加する先。config で必須なので常に埋まる。
	ProjectOwner  string
	ProjectNumber int
}

// render は名前付きテンプレートを実行して文字列にする。
// テンプレートは起動時に検証済みで、データも実行時に増減しないため、
// ここでのエラーは発生し得ない（発生したらプログラムのバグ）。
func render(name string, data any) string {
	var b strings.Builder
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
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

// SummaryContext は人間向けサマリの生成プロンプトに渡す情報。
//
// 個別の Issue ではなくパイプライン全体が対象なので、Context とは別に持つ。
type SummaryContext struct {
	GeneratedAt   time.Time
	ProjectOwner  string
	ProjectNumber int
	Repos         []string
	// Statuses は人間の関与点を説明するために使うレーン名。
	Statuses SummaryStatuses
	// Items は未終端のカードのスナップショット。
	Items []SummaryItem
	// Truncated は Items を件数で打ち切ったかどうか。
	Truncated  bool
	MaxRetries int
}

// SummaryStatuses はサマリのプロンプトが参照するレーン名。
type SummaryStatuses struct {
	Inbox    string
	Ready    string
	InReview string
	Blocked  string
}

// SummaryItem は 1 枚のカードのローカル状態。
type SummaryItem struct {
	Repo          string
	Issue         int
	Status        string
	PRNumber      int
	RetryCount    int
	UpdatedAt     time.Time
	LastRunPhase  string
	LastRunResult string
	// Age は Summarize が GeneratedAt との差から埋める表示用の文字列。呼び出し側は設定しない。
	Age string
}

// Summarize は人間向けサマリの生成プロンプトを組み立てる。
func Summarize(c SummaryContext) string {
	// 滞留の長さは絶対時刻より「どれだけ放置されているか」の方が判断に効く。
	// テンプレート側で time.Since を呼ぶと生成時刻に依存して出力が揺れるため、
	// 基準時刻を持つここで文字列にしておく。
	items := make([]SummaryItem, len(c.Items))
	copy(items, c.Items)
	for i := range items {
		items[i].Age = age(c.GeneratedAt, items[i].UpdatedAt)
	}
	c.Items = items
	return render("summarize", c)
}

// age は now から見た t の経過時間を日本語にする。
func age(now, t time.Time) string {
	if t.IsZero() {
		return "不明"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "たった今"
	case d < time.Hour:
		return fmt.Sprintf("%d 分前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 時間前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 日前", int(d.Hours()/24))
	}
}

// Notice はワーカーが Issue に投稿する通知文の組み立てに必要な情報。
type Notice struct {
	Repo  string
	Issue int
}

// PRNotFoundImplement は実装完了の報告後に PR を発見できなかった場合の Blocked 理由。
func PRNotFoundImplement(n Notice) string { return render("pr_not_found_implement", n) }

// PRNotFoundVerify は検証フェーズで PR を発見できなかった場合の Blocked 理由。
func PRNotFoundVerify(n Notice) string { return render("pr_not_found_verify", n) }

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
