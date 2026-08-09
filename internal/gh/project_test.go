package gh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 既存の選択肢と同名のものには id を引き継ぐ。引き継がないとカードの Status が消える。
func TestMergeOptionsPreservesExistingIDs(t *testing.T) {
	existing := []SingleSelectOption{
		{ID: "opt_ready", Name: "🎯 Ready", Color: "BLUE", Description: "old"},
		{ID: "opt_done", Name: "✅ Done", Color: "GREEN", Description: "old"},
	}
	want := []SingleSelectOptionInput{
		{Name: "📥 Inbox", Color: "GRAY", Description: "new"},
		{Name: "🎯 Ready", Color: "BLUE", Description: "new"},
		{Name: "✅ Done", Color: "GREEN", Description: "new"},
	}

	got := mergeOptions(existing, want)
	if len(got) != 3 {
		t.Fatalf("件数 = %d, want 3: %+v", len(got), got)
	}
	byName := map[string]SingleSelectOptionInput{}
	for _, o := range got {
		byName[o.Name] = o
	}
	if byName["🎯 Ready"].ID != "opt_ready" || byName["✅ Done"].ID != "opt_done" {
		t.Errorf("既存の id が引き継がれていません: %+v", got)
	}
	if byName["📥 Inbox"].ID != "" {
		t.Errorf("新規の選択肢に id が付いています: %+v", byName["📥 Inbox"])
	}
	// 説明や色は要求側の値で更新する。
	if byName["🎯 Ready"].Description != "new" {
		t.Errorf("説明が更新されていません: %+v", byName["🎯 Ready"])
	}
}

// 定義外の既存選択肢も残す。消すとその値が入っていたカードが空になる。
func TestMergeOptionsKeepsUnknownExistingOptions(t *testing.T) {
	existing := []SingleSelectOption{
		{ID: "opt_todo", Name: "Todo", Color: "GRAY", Description: "利用者が足したもの"},
		{ID: "opt_inbox", Name: "📥 Inbox", Color: "GRAY", Description: "old"},
	}
	want := []SingleSelectOptionInput{{Name: "📥 Inbox", Color: "GRAY", Description: "new"}}

	got := mergeOptions(existing, want)
	if len(got) != 2 {
		t.Fatalf("定義外の選択肢が消えています: %+v", got)
	}
	// 要求分が先、定義外は末尾。
	if got[0].Name != "📥 Inbox" || got[1].Name != "Todo" {
		t.Errorf("順序が想定と違います: %+v", got)
	}
	if got[1].ID != "opt_todo" || got[1].Description != "利用者が足したもの" {
		t.Errorf("定義外の選択肢がそのまま保持されていません: %+v", got[1])
	}
}

func TestMergeOptionsWithNoExisting(t *testing.T) {
	got := mergeOptions(nil, DefaultStatuses)
	if len(got) != len(DefaultStatuses) {
		t.Fatalf("件数 = %d, want %d", len(got), len(DefaultStatuses))
	}
	for _, o := range got {
		if o.ID != "" {
			t.Errorf("既存が無いのに id が付いています: %+v", o)
		}
	}
}

// fakeGraphQL は GraphQL リクエストを記録して定型のレスポンスを返す。
type fakeGraphQL struct {
	requests []map[string]any
	fieldID  string
	options  []SingleSelectOption
}

func (f *fakeGraphQL) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("リクエストが JSON ではありません: %v", err)
		}
		f.requests = append(f.requests, req)

		query, _ := req["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(query, "fields(first:"):
			resp := map[string]any{"data": map[string]any{"node": map[string]any{
				"fields": map[string]any{"nodes": []map[string]any{
					{"id": f.fieldID, "name": "Status", "options": f.options},
				}},
			}}}
			json.NewEncoder(w).Encode(resp)
		default:
			io.WriteString(w, `{"data":{}}`)
		}
	}))
}

func (f *fakeGraphQL) lastQuery() string {
	if len(f.requests) == 0 {
		return ""
	}
	q, _ := f.requests[len(f.requests)-1]["query"].(string)
	return q
}

func (f *fakeGraphQL) lastOptions(t *testing.T) []map[string]any {
	t.Helper()
	if len(f.requests) == 0 {
		t.Fatal("リクエストがありません")
	}
	vars, ok := f.requests[len(f.requests)-1]["variables"].(map[string]any)
	if !ok {
		t.Fatal("variables がありません")
	}
	raw, ok := vars["options"].([]any)
	if !ok {
		t.Fatalf("options がありません: %+v", vars)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, o := range raw {
		m, _ := o.(map[string]any)
		out = append(out, m)
	}
	return out
}

// ミューテーションが実在する入力型を使っていること。
// 過去に存在しない型名を書いていて setup-project が常に失敗していた。
func TestConfigureProjectStatusesUsesRealInputType(t *testing.T) {
	f := &fakeGraphQL{fieldID: "field_status"}
	srv := f.server(t)
	defer srv.Close()

	c := NewForTest("t", srv.URL, srv.URL+"/graphql")
	if err := c.ConfigureProjectStatuses(context.Background(), "proj_1", "Status", nil); err != nil {
		t.Fatalf("ConfigureProjectStatuses: %v", err)
	}

	q := f.lastQuery()
	if !strings.Contains(q, "ProjectV2SingleSelectFieldOptionInput") {
		t.Errorf("入力型名が正しくありません:\n%s", q)
	}
	if strings.Contains(q, "[ProjectV2SingleSelectOptionInput") {
		t.Error("GitHub のスキーマに存在しない型名を使っています")
	}
}

// 修復時は既存選択肢の id を送る（カードの Status を消さないため）。
func TestConfigureProjectStatusesSendsExistingIDs(t *testing.T) {
	f := &fakeGraphQL{
		fieldID: "field_status",
		options: []SingleSelectOption{
			{ID: "opt_inbox", Name: "📥 Inbox", Color: "GRAY", Description: "old"},
			{ID: "opt_custom", Name: "Icebox", Color: "PINK", Description: "利用者定義"},
		},
	}
	srv := f.server(t)
	defer srv.Close()

	c := NewForTest("t", srv.URL, srv.URL+"/graphql")
	if err := c.ConfigureProjectStatuses(context.Background(), "proj_1", "Status", nil); err != nil {
		t.Fatalf("ConfigureProjectStatuses: %v", err)
	}

	opts := f.lastOptions(t)
	byName := map[string]map[string]any{}
	for _, o := range opts {
		name, _ := o["name"].(string)
		byName[name] = o
	}
	if got := byName["📥 Inbox"]["id"]; got != "opt_inbox" {
		t.Errorf("既存 Inbox の id が送られていません: %v", got)
	}
	if _, ok := byName["Icebox"]; !ok {
		t.Error("定義外の選択肢が削除されています")
	}
	if got := byName["Icebox"]["id"]; got != "opt_custom" {
		t.Errorf("定義外の選択肢の id が送られていません: %v", got)
	}
	// 新規に足す選択肢には id を付けない。
	if _, ok := byName["🎯 Ready"]["id"]; ok {
		t.Errorf("新規の選択肢に id が付いています: %+v", byName["🎯 Ready"])
	}
	// description は必須なので、常に送る。
	for name, o := range byName {
		if _, ok := o["description"]; !ok {
			t.Errorf("%s に description がありません（スキーマ上必須）", name)
		}
	}
}

// フィールドが無ければ作成に回る。
func TestConfigureProjectStatusesCreatesMissingField(t *testing.T) {
	f := &fakeGraphQL{} // fieldID 空 = Status フィールドなし
	srv := f.server(t)
	defer srv.Close()

	c := NewForTest("t", srv.URL, srv.URL+"/graphql")
	if err := c.ConfigureProjectStatuses(context.Background(), "proj_1", "Status", nil); err != nil {
		t.Fatalf("ConfigureProjectStatuses: %v", err)
	}
	q := f.lastQuery()
	if !strings.Contains(q, "createProjectV2Field") {
		t.Errorf("フィールド作成が呼ばれていません:\n%s", q)
	}
	if !strings.Contains(q, "ProjectV2SingleSelectFieldOptionInput") {
		t.Errorf("作成側の入力型名が正しくありません:\n%s", q)
	}
}

// フィールドが 50 個を超えて 2 ページ目に Status がある Project でも見つける。
// 取りこぼすとフィールド作成に回り、"name already in use" で失敗する。
func TestFindSingleSelectFieldPaginates(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		vars, _ := req["variables"].(map[string]any)
		w.Header().Set("Content-Type", "application/json")

		if _, hasCursor := vars["cursor"]; !hasCursor {
			pages++
			// 1 ページ目には目的のフィールドが無い。
			io.WriteString(w, `{"data":{"node":{"fields":{
				"pageInfo":{"hasNextPage":true,"endCursor":"CUR"},
				"nodes":[{"id":"f_other","name":"Priority","options":[]}]}}}}`)
			return
		}
		pages++
		if vars["cursor"] != "CUR" {
			t.Errorf("カーソルが渡っていません: %v", vars["cursor"])
		}
		io.WriteString(w, `{"data":{"node":{"fields":{
			"pageInfo":{"hasNextPage":false,"endCursor":""},
			"nodes":[{"id":"f_status","name":"Status","options":[
				{"id":"opt_a","name":"📥 Inbox","color":"GRAY","description":"d"}]}]}}}}`)
	}))
	defer srv.Close()

	c := NewForTest("t", srv.URL, srv.URL+"/graphql")
	id, opts, err := c.findSingleSelectField(context.Background(), "proj_1", "Status")
	if err != nil {
		t.Fatalf("findSingleSelectField: %v", err)
	}
	if id != "f_status" {
		t.Errorf("フィールド ID = %q, want f_status", id)
	}
	if len(opts) != 1 || opts[0].ID != "opt_a" {
		t.Errorf("選択肢が取れていません: %+v", opts)
	}
	if pages != 2 {
		t.Errorf("ページ数 = %d, want 2", pages)
	}
}

// 最後まで見て無ければ空を返す（呼び出し側がフィールドを作る）。
func TestFindSingleSelectFieldNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"node":{"fields":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}`)
	}))
	defer srv.Close()

	c := NewForTest("t", srv.URL, srv.URL+"/graphql")
	id, _, err := c.findSingleSelectField(context.Background(), "proj_1", "Status")
	if err != nil || id != "" {
		t.Errorf("id=%q err=%v, want 空", id, err)
	}
}
