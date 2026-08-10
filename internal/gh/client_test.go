package gh

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewTransport は常駐プロセス用の Transport にゾンビ接続防止のタイムアウトが設定されていることを確認する。
func TestNewTransport(t *testing.T) {
	tr := newTransport()
	if tr.IdleConnTimeout != 30*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 30s", tr.IdleConnTimeout)
	}
	if tr.ResponseHeaderTimeout != 15*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 15s", tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 10s", tr.TLSHandshakeTimeout)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Errorf("ForceAttemptHTTP2 = false, want true")
	}
}

// TestClientDoRetryOnNetworkError は通信切断時にゾンビ接続を破棄して 1 回リトライし、成功することを確認する。
func TestClientDoRetryOnNetworkError(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count == 1 {
			// 1 回目は接続を強制切断してネットワークエラーを模倣する。
			killConn(t, w)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewForTest("dummy-token", server.URL, server.URL+"/graphql")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out map[string]string
	if err := client.rest(ctx, http.MethodGet, "/test", nil, &out); err != nil {
		t.Fatalf("rest failed: %v", err)
	}

	if out["status"] != "ok" {
		t.Errorf("got status %q, want %q", out["status"], "ok")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// TestClientDoRetriesGraphQLQuery は GraphQL の読み取り（ボディ付き POST）が
// 再送されることを確認する。Project のポーリングはこの経路を通る。
func TestClientDoRetriesGraphQLQuery(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		_, _ = io.ReadAll(r.Body)
		if count == 1 {
			killConn(t, w)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"echo":"success"}}`))
	}))
	defer server.Close()

	client := NewForTest("dummy-token", server.URL, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out struct {
		Echo string `json:"echo"`
	}
	if err := client.graphql(ctx, "query($id: ID!) { node(id: $id) { id } }", map[string]any{"id": "x"}, &out); err != nil {
		t.Fatalf("graphql failed: %v", err)
	}

	if out.Echo != "success" {
		t.Errorf("got echo %q, want %q", out.Echo, "success")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// TestClientDoDoesNotRetryGraphQLMutation は書き込みを再送しないことを確認する。
//
// サーバが処理を終えてから応答を落とした場合、再送は Status の二重更新や
// フィールドの二重作成になる。通信エラーはそのまま呼び出し側へ返す。
func TestClientDoDoesNotRetryGraphQLMutation(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		_, _ = io.ReadAll(r.Body)
		killConn(t, w)
	}))
	defer server.Close()

	client := NewForTest("dummy-token", server.URL, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.graphql(ctx, setStatusMutation, map[string]any{"item": "x"}, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1（mutation は再送しない）", got)
	}
}

// TestClientDoDoesNotRetryRESTPost はコメント投稿のような REST の書き込みを
// 再送しないことを確認する。再送するとコメントが二重に投稿されうる。
func TestClientDoDoesNotRetryRESTPost(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		_, _ = io.ReadAll(r.Body)
		killConn(t, w)
	}))
	defer server.Close()

	client := NewForTest("dummy-token", server.URL, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.AddComment(ctx, "owner/repo", 1, "本文")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1（POST は再送しない）", got)
	}
}

func TestIsGraphQLMutation(t *testing.T) {
	tests := []struct {
		op   string
		want bool
	}{
		{setStatusMutation, true},
		{"mutation($id: ID!) { x }", true},
		{"\n  mutation { x }", true},
		{"query($id: ID!) { node(id: $id) { id } }", false},
		// キーワードを省略した短縮形は常に query。
		{"{ viewer { login } }", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isGraphQLMutation(tt.op); got != tt.want {
			t.Errorf("isGraphQLMutation(%.30q) = %v, want %v", tt.op, got, tt.want)
		}
	}
}

// killConn は応答を返さずに接続を切り、通信エラーを模倣する。
func killConn(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conn.Close()
}

// TestClientDoContextCanceled はコンテキストがキャンセルされている場合はリトライしないことを確認する。
func TestClientDoContextCanceled(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		killConn(t, w)
	}))
	defer server.Close()

	client := NewForTest("dummy-token", server.URL, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 即座にキャンセル

	var out map[string]string
	err := client.rest(ctx, http.MethodGet, "/test", nil, &out)
	if err == nil {
		t.Fatal("want error, got nil")
	}

	if got := atomic.LoadInt32(&attempts); got > 1 {
		t.Errorf("attempts = %d, want <= 1", got)
	}
}
