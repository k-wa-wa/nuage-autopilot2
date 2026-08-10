package prompt

import (
	"strings"
	"testing"
	"time"

	"nuage-autopilot2/internal/gh"
)

func sample() Context {
	return Context{
		Repo: "k-wa-wa/example",
		Issue: &gh.Issue{
			Number: 12,
			Title:  "ログイン画面がほしい",
			Body:   "なんかいい感じのログイン画面。",
		},
		Comments: []gh.Comment{
			{User: gh.User{Login: "k-wa-wa"}, Body: "OAuth も対応して", CreatedAt: time.Now()},
		},
		PRNumber:             34,
		Gate:                 "1. `npm run e2e` が通ること",
		GatePath:             ".agents/autopilot-gate.md",
		MaxRetries:           5,
		ProjectOwner:         "k-wa-wa",
		ProjectNumber:        1,
		ProjectID:            "PVT_kwDOA12345",
		ProjectStatusFieldID: "PVTSSF_67890",
		ProjectInboxOptionID: "opt_inbox123",
		StatusInbox:          "📥 Inbox",
	}
}

// すべてのプロンプトが Status を触らないよう明示していること。
func TestAllPromptsForbidStatusChanges(t *testing.T) {
	c := sample()
	prompts := map[string]string{
		"refine":         Refine(c),
		"implement":      Implement(c),
		"review":         Review(c),
		"triage-review":  Triage(c, TriageReview),
		"triage-blocked": Triage(c, TriageBlocked),
	}
	for name, p := range prompts {
		if !strings.Contains(p, "Status フィールドは絶対に変更しないでください") {
			t.Errorf("%s: Status 変更の禁止が含まれていません", name)
		}
		if !strings.Contains(p, "#12") {
			t.Errorf("%s: Issue 番号が含まれていません", name)
		}
	}
}

func TestRefineAsksForMarker(t *testing.T) {
	p := Refine(sample())
	for _, want := range []string{"AUTOPILOT_ACTION", "READY_FOR_HUMAN", "QUESTION_POSTED", "SPLIT", "受け入れ条件"} {
		if !strings.Contains(p, want) {
			t.Errorf("refine プロンプトに %q がありません", want)
		}
	}
}

func TestRefineSplitsWithProjectAndInbox(t *testing.T) {
	c := sample()
	p := Refine(c)
	for _, want := range []string{
		"gh project item-add 1 --owner k-wa-wa",
		"gh project item-edit",
		"PVT_kwDOA12345",
		"PVTSSF_67890",
		"opt_inbox123",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("refine プロンプトに %q が含まれていません", want)
		}
	}

	// Project ID などが未解決の場合のフォールバック
	cFallback := sample()
	cFallback.ProjectID = ""
	cFallback.ProjectStatusFieldID = ""
	cFallback.ProjectInboxOptionID = ""
	pFallback := Refine(cFallback)
	if !strings.Contains(pFallback, "gh project item-add 1 --owner k-wa-wa") || !strings.Contains(pFallback, "📥 Inbox") {
		t.Errorf("フォールバック時の refine プロンプトが不正です:\n%s", pFallback)
	}
}

func TestImplementRequiresClosesKeyword(t *testing.T) {
	p := Implement(sample())
	if !strings.Contains(p, "Closes #12") {
		t.Error("implement プロンプトに `Closes #12` の指示がありません")
	}
	for _, want := range []string{"AUTOPILOT_ACTION", "PR_READY", "BLOCKED"} {
		if !strings.Contains(p, want) {
			t.Errorf("implement プロンプトに %q がありません", want)
		}
	}
}

func TestImplementIncludesCIHintOnRetry(t *testing.T) {
	c := sample()
	c.CIHint = "テストが 3 件落ちています"
	c.RetryCount = 2
	p := Implement(c)
	if !strings.Contains(p, "テストが 3 件落ちています") {
		t.Error("CI の失敗情報が含まれていません")
	}
	if !strings.Contains(p, "2/5 回目") {
		t.Error("リトライ回数が含まれていません")
	}
	// ヒントが無いときは節ごと出さない。
	if strings.Contains(Implement(sample()), "前回の検証で失敗した内容") {
		t.Error("ヒントが無いのに失敗情報の節が出ています")
	}
}

func TestReviewIncludesGateAndPR(t *testing.T) {
	p := Review(sample())
	if !strings.Contains(p, "npm run e2e") {
		t.Error("品質ゲート定義が含まれていません")
	}
	if !strings.Contains(p, ".agents/autopilot-gate.md") {
		t.Error("品質ゲートのパスが含まれていません")
	}
	if !strings.Contains(p, "gh pr diff 34") {
		t.Error("PR 番号が含まれていません")
	}
	if !strings.Contains(p, "AUTOPILOT_VERDICT") {
		t.Error("判定マーカーの指示がありません")
	}
	// ゲート未定義なら節ごと出さない（本文中の言及と区別するため見出しで判定する）。
	c := sample()
	c.Gate = ""
	if strings.Contains(Review(c), "## 品質ゲート定義") {
		t.Error("ゲート未定義なのに節が出ています")
	}
}

func TestTriageModesDifferInAllowedActions(t *testing.T) {
	c := sample()
	review := Triage(c, TriageReview)
	blocked := Triage(c, TriageBlocked)

	if !strings.Contains(review, "ANSWERED") || !strings.Contains(review, "NEEDS_FIX") {
		t.Error("review モードの選択肢が不足しています")
	}
	if strings.Contains(review, "RESPEC") {
		t.Error("review モードに blocked 用の選択肢が混入しています")
	}
	if !strings.Contains(blocked, "RESUME") || !strings.Contains(blocked, "RESPEC") {
		t.Error("blocked モードの選択肢が不足しています")
	}
	if strings.Contains(blocked, "NEEDS_FIX") {
		t.Error("blocked モードに review 用の選択肢が混入しています")
	}
}

func TestNewInputsAreIncluded(t *testing.T) {
	c := sample()
	c.NewInputs = []string{"@k-wa-wa:\nモックしてよい"}
	p := Triage(c, TriageBlocked)
	if !strings.Contains(p, "今回あなたを起動した人間の発言") || !strings.Contains(p, "モックしてよい") {
		t.Error("起床要因となった発言が含まれていません")
	}
	if strings.Contains(Triage(sample(), TriageBlocked), "今回あなたを起動した人間の発言") {
		t.Error("発言が無いのに節が出ています")
	}
}

func TestEmptyBodyIsHandled(t *testing.T) {
	c := sample()
	c.Issue.Body = ""
	if !strings.Contains(Refine(c), "(本文なし)") {
		t.Error("本文が空のときのプレースホルダがありません")
	}
}
