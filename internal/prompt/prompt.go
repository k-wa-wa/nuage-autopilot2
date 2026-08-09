// Package prompt はエージェントに渡すプロンプトを組み立てる。
//
// GitHub 側の内容（Issue 本文・コメント・PR）の更新はエージェントが gh コマンドで
// 行う。ワーカーは Project の Status のみを管理する（責務の分離）。
package prompt

import (
	"fmt"
	"strings"
	"time"

	"nuage-autopilot2/internal/gh"
)

// Context はプロンプト生成に必要な情報。
type Context struct {
	Repo       string
	Issue      *gh.Issue
	Comments   []gh.Comment
	NewInputs  []string // 今回の起床要因となった人間の発言
	PRNumber   int
	Gate       string // 品質ゲート定義ファイルの内容
	GatePath   string
	RetryCount int
	MaxRetries int
	CIHint     string // CI 失敗などの追加情報
}

const common = `あなたは自動開発パイプラインで動作する自律エージェントです。
GitHub への書き込み（Issue 本文の更新・コメント投稿・PR 作成）は gh コマンドで自分で行ってください。
ただし **GitHub Projects の Status フィールドは絶対に変更しないでください**。Status はワーカーが管理します。

対象リポジトリ: %s
対象 Issue: #%d %s
`

// Refine は Inbox での仕様精緻化プロンプトを組み立てる。
func Refine(c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, common, c.Repo, c.Issue.Number, c.Issue.Title)
	b.WriteString(`
## あなたのタスク: 要求の精緻化

人間が書いた曖昧な要求を、実装可能な仕様に落とし込みます。

1. Issue 本文を読み、必要ならリポジトリのコードを調査する。
2. 次の構成で Issue 本文を書き換える（` + "`gh issue edit`" + ` を使う）:
   - ## 背景
   - ## 目的
   - ## スコープ外
   - ## 受け入れ条件（チェックリスト形式で、検証可能な粒度）
3. 判断できない不明点があれば、本文は草案のまま残し、質問を ` + "`gh issue comment`" + ` で投稿する。
   憶測で仕様を決めないこと。
4. 1 つの PR に収まらない規模だと判断した場合は、親 Issue に全体仕様をまとめ、
   子 Issue を ` + "`gh issue create`" + ` で起票して sub-issue として紐付ける。
   親 Issue 自体は実装対象にしない。
5. 仕様が確定した場合は、最後に「仕様の精緻化が完了しました。内容をご確認の上、Ready に移動してください」
   という主旨のコメントを投稿する。

## 出力の最後に必ず次の 1 行を出力すること

` + "```" + `
AUTOPILOT_ACTION: <READY_FOR_HUMAN | QUESTION_POSTED | SPLIT>
` + "```" + `

- READY_FOR_HUMAN: 仕様が確定し、人間の Ready 判断を待てる状態
- QUESTION_POSTED: 不明点を質問し、回答待ちの状態
- SPLIT: 子 Issue に分割した（親 Issue はここで停止する）
`)
	b.WriteString(issueSection(c))
	b.WriteString(inputSection(c))
	return b.String()
}

// Implement は実装フェーズのプロンプトを組み立てる。
func Implement(c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, common, c.Repo, c.Issue.Number, c.Issue.Title)
	b.WriteString(`
## あなたのタスク: 実装

1. カレントディレクトリは対象リポジトリのワークツリーで、既定ブランチの最新に同期済みです
   （再開の場合は既存の作業ブランチがチェックアウト済み）。
2. 作業ブランチを作成（または既存ブランチを継続）して実装する。ブランチ名は任意です。
3. リポジトリの流儀に従ってテストを追加・実行し、ローカルで通ることを確認する。
4. コミットして push し、PR を作成する。既に PR がある場合は push だけでよい。
   **PR 本文には必ず ` + "`Closes #%d`" + ` を含めること**（Issue との紐付けに使います）。
5. 実装できない技術的な問題に突き当たった場合は、無理に進めず BLOCKED を報告する。

## 出力の最後に必ず次の 2 行を出力すること

` + "```" + `
AUTOPILOT_ACTION: <PR_READY | BLOCKED>
AUTOPILOT_REASON: <1 行の要約。BLOCKED の場合は何に詰まったか>
` + "```" + `
`)
	fmt.Fprintf(&b, "\n（上記 4 の `Closes #%d` を忘れないこと）\n", c.Issue.Number)
	b.WriteString(issueSection(c))
	b.WriteString(gateSection(c))
	if c.CIHint != "" {
		fmt.Fprintf(&b, "\n## 前回の検証で失敗した内容（%d/%d 回目のリトライ）\n\n```\n%s\n```\n",
			c.RetryCount, c.MaxRetries, c.CIHint)
	}
	b.WriteString(inputSection(c))
	return b.String()
}

// Review は Verifying でのセルフレビュープロンプトを組み立てる。
func Review(c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, common, c.Repo, c.Issue.Number, c.Issue.Title)
	fmt.Fprintf(&b, `
## あなたのタスク: セルフレビューと品質ゲートの検証

対象 PR: #%d（CI は成功済み）

1. `+"`gh pr diff %d`"+` で差分を確認し、次を点検する:
   - Issue の受け入れ条件をすべて満たしているか
   - 不要な差分・デバッグコード・コメントアウトの残骸がないか
   - 明らかなバグ、エラーハンドリング漏れ、境界条件の見落としがないか
2. 品質ゲート定義（下記）がある場合は、その手順に従って検証する。
3. 軽微な問題は自分で修正して push してよい。その場合も最終的な判定は PASS とする。
4. 受け入れ条件を満たしていない、または自分では直せない問題がある場合は FAIL とし、
   `+"`gh pr comment`"+` で理由を残す。

## 出力の最後に必ず次の 2 行を出力すること

`+"```"+`
AUTOPILOT_VERDICT: <PASS | FAIL>
AUTOPILOT_REASON: <1 行の要約>
`+"```"+`
`, c.PRNumber, c.PRNumber)
	b.WriteString(issueSection(c))
	b.WriteString(gateSection(c))
	return b.String()
}

// TriageMode は triage プロンプトの動作モード。
type TriageMode int

// triage の対象レーン。
const (
	// TriageReview は In Review でレビュー提出を受けた場合。
	TriageReview TriageMode = iota
	// TriageBlocked は Blocked で助言コメントを受けた場合。
	TriageBlocked
)

// Triage は In Review / Blocked での人間の発言に対する判断プロンプトを組み立てる。
func Triage(c Context, mode TriageMode) string {
	var b strings.Builder
	fmt.Fprintf(&b, common, c.Repo, c.Issue.Number, c.Issue.Title)
	switch mode {
	case TriageReview:
		fmt.Fprintf(&b, `
## あなたのタスク: レビュー指摘のトリアージ

対象 PR: #%d は現在レビュー中です。レビュアーから下記の指摘が届きました。
内容を読み、次のどちらかを判断してください。

- **質問に答えれば済む場合**: `+"`gh pr comment`"+` で回答を投稿し、ANSWERED を報告する。
  コードは変更しないこと。
- **コードの修正が必要な場合**: この場ではまだ修正せず、NEEDS_FIX を報告する。
  ワーカーが実装フェーズに戻したうえで、改めて修正を依頼します。

## 出力の最後に必ず次の 2 行を出力すること

`+"```"+`
AUTOPILOT_ACTION: <ANSWERED | NEEDS_FIX>
AUTOPILOT_REASON: <1 行の要約>
`+"```"+`
`, c.PRNumber)
	case TriageBlocked:
		b.WriteString(`
## あなたのタスク: ブロック解除のトリアージ

この Issue は自力解決不能と判断してブロック中です。人間から助言が届きました。
内容を読み、次のどちらかを判断してください。

- **助言に従えば実装を続行できる場合**: RESUME を報告する。この場では修正しないこと。
  ワーカーが実装フェーズに戻します。
- **仕様レベルの問題で、要求の作り直しが必要な場合**: RESPEC を報告する。
  ワーカーが Inbox に差し戻し、仕様の精緻化からやり直します。

## 出力の最後に必ず次の 2 行を出力すること

` + "```" + `
AUTOPILOT_ACTION: <RESUME | RESPEC>
AUTOPILOT_REASON: <1 行の要約>
` + "```" + `
`)
	}
	b.WriteString(issueSection(c))
	b.WriteString(inputSection(c))
	return b.String()
}

func issueSection(c Context) string {
	var b strings.Builder
	b.WriteString("\n---\n\n## Issue #")
	fmt.Fprintf(&b, "%d: %s\n\n", c.Issue.Number, c.Issue.Title)
	body := strings.TrimSpace(c.Issue.Body)
	if body == "" {
		body = "(本文なし)"
	}
	b.WriteString(body)
	b.WriteString("\n")
	if len(c.Comments) > 0 {
		fmt.Fprintf(&b, "\n## 直近のコメント（古い順、最大 %d 件）\n", len(c.Comments))
		for _, cm := range c.Comments {
			fmt.Fprintf(&b, "\n### @%s (%s)\n%s\n",
				cm.User.Login, cm.CreatedAt.Format(time.RFC3339), strings.TrimSpace(cm.Body))
		}
	}
	return b.String()
}

func inputSection(c Context) string {
	if len(c.NewInputs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 今回あなたを起動した人間の発言\n")
	for _, s := range c.NewInputs {
		fmt.Fprintf(&b, "\n---\n%s\n", strings.TrimSpace(s))
	}
	return b.String()
}

func gateSection(c Context) string {
	if strings.TrimSpace(c.Gate) == "" {
		return ""
	}
	return fmt.Sprintf("\n## 品質ゲート定義（%s）\n\n%s\n", c.GatePath, strings.TrimSpace(c.Gate))
}
