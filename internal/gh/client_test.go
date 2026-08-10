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

// TestClientDoWithBodyRetry は POST リクエスト（ボディ付き）でも安全にリトライできることを確認する。
func TestClientDoWithBodyRetry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		_, _ = io.ReadAll(r.Body)
		if count == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack not supported", http.StatusInternalServerError)
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
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
	if err := client.graphql(ctx, "test-query", map[string]any{"key": "value"}, &out); err != nil {
		t.Fatalf("graphql failed: %v", err)
	}

	if out.Echo != "success" {
		t.Errorf("got echo %q, want %q", out.Echo, "success")
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// TestClientDoContextCanceled はコンテキストがキャンセルされている場合はリトライしないことを確認する。
func TestClientDoContextCanceled(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
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
