package summary

import "testing"

func TestParseFenced(t *testing.T) {
	out := `考えた結果をまとめます。

` + "```json" + `
{
  "headline": "レビュー待ちが 2 件あります",
  "todos": [
    {"repo": "o/r", "issue": 12, "title": "PR をレビューする", "status": "👀 In Review",
     "urgency": "HIGH", "why": "検証は通っている", "action": "Approve & Merge する"}
  ],
  "notes": "その他は自走中です"
}
` + "```" + `
`
	r, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Headline != "レビュー待ちが 2 件あります" {
		t.Errorf("headline が %q", r.Headline)
	}
	if len(r.Todos) != 1 {
		t.Fatalf("todos が %d 件", len(r.Todos))
	}
	if r.Todos[0].Urgency != UrgencyHigh {
		t.Errorf("urgency が %q（大文字は正規化されるべき）", r.Todos[0].Urgency)
	}
}

// 書式例を復唱してから本番を出す CLI があるため、最後のフェンスを採る。
func TestParseUsesLastFence(t *testing.T) {
	out := "```json\n{\"headline\": \"例\"}\n```\n本番:\n```json\n{\"headline\": \"本番\"}\n```"
	r, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Headline != "本番" {
		t.Errorf("headline が %q", r.Headline)
	}
}

// フェンスなしでも前後に文章があるだけなら拾える。
func TestParseBare(t *testing.T) {
	r, err := Parse(`結果は次のとおりです。 {"headline": "静か", "todos": []} 以上。`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Headline != "静か" || len(r.Todos) != 0 {
		t.Errorf("想定外の解釈: %+v", r)
	}
}

func TestParseErrors(t *testing.T) {
	for _, out := range []string{"", "JSON はありません", "```json\n{壊れている\n```"} {
		if _, err := Parse(out); err == nil {
			t.Errorf("Parse(%q) はエラーになるべきです", out)
		}
	}
}

// 中身の無い TODO は落とし、上限で打ち切る。
func TestNormalizeDropsAndCaps(t *testing.T) {
	r := &Report{Todos: []Todo{{Why: "説明だけで指示がない"}}}
	r.normalize()
	if len(r.Todos) != 0 {
		t.Errorf("空の TODO が残っています: %+v", r.Todos)
	}

	r = &Report{}
	for i := 0; i < maxTodos+5; i++ {
		r.Todos = append(r.Todos, Todo{Title: "t"})
	}
	r.normalize()
	if len(r.Todos) != maxTodos {
		t.Errorf("上限で切られていません: %d 件", len(r.Todos))
	}
}
