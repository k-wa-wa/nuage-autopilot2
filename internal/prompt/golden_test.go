package prompt

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nuage-autopilot2/internal/gh"
)

// -update を付けて実行すると testdata の期待値を再生成する。
// テンプレート移植の前後で出力が変わらないことを確認するためのゴールデンテスト。
var update = flag.Bool("update", false, "update golden files")

var (
	t1 = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	t2 = time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
)

// base は全分岐を踏むためのフル装備の Context。
func base() Context {
	return Context{
		Repo: "k-wa-wa/example",
		Issue: &gh.Issue{
			Number: 12,
			Title:  "ログイン画面がほしい",
			Body:   "  なんかいい感じのログイン画面。\n複数行の本文。  ",
		},
		Comments: []gh.Comment{
			{User: gh.User{Login: "k-wa-wa"}, Body: " OAuth も対応して ", CreatedAt: t1},
			{User: gh.User{Login: "reviewer"}, Body: "SSO はスコープ外で", CreatedAt: t2},
		},
		ReviewComments: []gh.ReviewComment{
			{User: gh.User{Login: "reviewer"}, Path: "internal/auth/login.go", Line: 42, Body: "ここ nil チェックが要る", CreatedAt: t1},
			{User: gh.User{Login: "reviewer"}, Path: "internal/auth/session.go", OriginalLine: 7, Body: "命名を揃えて", CreatedAt: t2},
			{User: gh.User{Login: "reviewer"}, Path: "README.md", Body: "行が特定できないコメント", CreatedAt: t2},
		},
		NewInputs:     []string{"@k-wa-wa:\nモックしてよい", " 追加の指示 "},
		PRNumber:      34,
		Gate:          "\n1. `npm run e2e` が通ること\n2. lint が通ること\n",
		GatePath:      ".agents/autopilot-gate.md",
		RetryCount:    2,
		MaxRetries:    5,
		CIHint:        "テストが 3 件落ちています",
		ProjectOwner:  "k-wa-wa",
		ProjectNumber: 1,
	}
}

// minimal は任意項目をすべて落とした Context。
// Project は config で必須なので、ここでも常に埋まっている前提にする。
func minimal() Context {
	return Context{
		Repo:          "k-wa-wa/example",
		Issue:         &gh.Issue{Number: 12, Title: "ログイン画面がほしい"},
		ProjectOwner:  "k-wa-wa",
		ProjectNumber: 1,
	}
}

func withIssueBody(c Context, body string) Context {
	issue := *c.Issue
	issue.Body = body
	c.Issue = &issue
	return c
}

// goldenCases は生成される全プロンプトの分岐を網羅する。
func goldenCases() map[string]func() string {
	// implement: CI ヒント無し。
	implementNoHint := base()
	implementNoHint.CIHint = ""

	// review: ゲート定義が空白のみ。
	reviewBlankGate := base()
	reviewBlankGate.Gate = "   \n  "

	return map[string]func() string{
		"refine_full":            func() string { return Refine(base()) },
		"refine_minimal":         func() string { return Refine(minimal()) },
		"refine_empty_body":      func() string { return Refine(withIssueBody(base(), "   ")) },
		"implement_full":         func() string { return Implement(base()) },
		"implement_no_ci_hint":   func() string { return Implement(implementNoHint) },
		"implement_minimal":      func() string { return Implement(minimal()) },
		"review_full":            func() string { return Review(base()) },
		"review_blank_gate":      func() string { return Review(reviewBlankGate) },
		"review_minimal":         func() string { return Review(minimal()) },
		"triage_review_full":     func() string { return Triage(base(), TriageReview) },
		"triage_review_minimal":  func() string { return Triage(minimal(), TriageReview) },
		"triage_blocked_full":    func() string { return Triage(base(), TriageBlocked) },
		"triage_blocked_minimal": func() string { return Triage(minimal(), TriageBlocked) },
		"triage_unknown_mode":    func() string { return Triage(base(), TriageMode(99)) },

		// ワーカーが投稿する通知文。宛先が人間なので文面の崩れに気付きにくい。
		"notice_pr_not_found_implement": func() string {
			return PRNotFoundImplement(Notice{Repo: "k-wa-wa/example", Issue: 12})
		},
		"notice_pr_not_found_verify": func() string {
			return PRNotFoundVerify(Notice{Repo: "k-wa-wa/example", Issue: 12})
		},
	}
}

// TestGoldenPrompts は生成結果が testdata と 1 バイトも違わないことを確認する。
func TestGoldenPrompts(t *testing.T) {
	for name, gen := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			got := gen()
			path := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ゴールデンファイルが読めません（-update で生成してください）: %v", err)
			}
			if got != string(want) {
				t.Errorf("プロンプトがゴールデンと一致しません\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}
