// Package gh は GitHub REST / GraphQL API の最小クライアントを提供する。
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultRestBase   = "https://api.github.com"
	defaultGraphQLURL = "https://api.github.com/graphql"
	userAgent         = "autopilot"
	apiVersion        = "2022-11-28"
)

// Client は GitHub API クライアント。
type Client struct {
	token string
	http  *http.Client
	// Login は認証しているアカウントのログイン名。自分の発言を無視するために使う。
	Login string
	// baseURL / graphqlURL は空なら本番のエンドポイントを使う。テストで差し替える。
	baseURL    string
	graphqlURL string
}

// New はトークンを環境変数（GH_TOKEN / GITHUB_TOKEN）から取得してクライアントを作る。
func New() (*Client, error) {
	token := firstNonEmpty(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("GH_TOKEN または GITHUB_TOKEN が未設定です（Projects v2 を操作するため classic PAT の project スコープが必要）")
	}
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// NewForTest はエンドポイントを差し替えた Client を作る。テストからのみ使う。
func NewForTest(token, baseURL, gqlURL string) *Client {
	return &Client{
		token:      token,
		http:       &http.Client{Timeout: 10 * time.Second},
		baseURL:    baseURL,
		graphqlURL: gqlURL,
	}
}

func (c *Client) restBaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultRestBase
}

func (c *Client) gqlURL() string {
	if c.graphqlURL != "" {
		return c.graphqlURL
	}
	return defaultGraphQLURL
}

// Token は保持しているトークンを返す。子プロセス（gh / git）へ引き渡すために使う。
func (c *Client) Token() string { return c.token }

// ResolveLogin は認証ユーザーのログイン名を取得して保持する。
func (c *Client) ResolveLogin(ctx context.Context) error {
	var v struct {
		Login string `json:"login"`
	}
	if err := c.rest(ctx, http.MethodGet, "/user", nil, &v); err != nil {
		return err
	}
	c.Login = v.Login
	return nil
}

// rest は REST API を呼ぶ。path は "/user" のように先頭スラッシュ付き、
// または "https://" で始まる絶対 URL を受け付ける。
func (c *Client) rest(ctx context.Context, method, path string, body any, out any) error {
	_, err := c.restWithHeader(ctx, method, path, body, out)
	return err
}

// restWithHeader は REST API を呼び、レスポンスヘッダも返す（Link ヘッダのページングに使う）。
func (c *Client) restWithHeader(ctx context.Context, method, path string, body any, out any) (http.Header, error) {
	url := path
	if !strings.HasPrefix(path, "http") {
		url = c.restBaseURL() + path
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return resp.Header, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Header, fmt.Errorf("GitHub REST %s %s: %s: %s", method, path, resp.Status, truncate(string(b), 400))
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.Header, fmt.Errorf("レスポンスの解析に失敗 (%s %s): %w", method, path, err)
		}
	}
	return resp.Header, nil
}

// graphql は GraphQL API を呼ぶ。
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	payload := map[string]any{"query": query}
	if vars != nil {
		payload["variables"] = vars
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gqlURL(), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub GraphQL: %s: %s", resp.Status, truncate(string(raw), 400))
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("GraphQL レスポンスの解析に失敗: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("GraphQL エラー: %s", strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}

// restPaged は Link ヘッダを辿って全ページを取得する。
//
// コメントやレビューは 100 件を超えることがあり、1 ページ目だけを見ていると
// 新しい発言を取りこぼす（古い順に返るため、新しいものほど後ろのページに来る）。
func restPaged[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	next := path
	for i := 0; next != "" && i < 100; i++ {
		var page []T
		hdr, err := c.restWithHeader(ctx, http.MethodGet, next, nil, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = ""
		if m := linkNextRe.FindStringSubmatch(hdr.Get("Link")); m != nil {
			next = m[1]
		}
	}
	return all, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
