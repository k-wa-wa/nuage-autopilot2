package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/store"
)

// fakeSource は Source をメモリ上で満たすテスト用の実装。
type fakeSource struct {
	items  []*store.Item
	runs   []*store.Run
	active *Active
	logDir string
	queue  int
}

func (f *fakeSource) Meta() Meta {
	return Meta{
		Login:         "test-bot",
		ProjectOwner:  "o",
		ProjectNumber: 1,
		Repos:         []string{"o/r"},
		Statuses: []string{"📥 Inbox", "🎯 Ready", "🚧 In Progress", "🔍 Verifying",
			"👀 In Review", "⏸ Blocked", "✅ Done"},
	}
}
func (f *fakeSource) Items() ([]*store.Item, error) { return f.items, nil }
func (f *fakeSource) LatestRuns() ([]*store.Run, error) {
	latest := map[string]*store.Run{}
	for _, r := range f.runs {
		k := itemKey(r.Repo, r.IssueNumber)
		if cur, ok := latest[k]; !ok || r.ID > cur.ID {
			latest[k] = r
		}
	}
	out := make([]*store.Run, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeSource) IssueRuns(repo string, issue, limit int) ([]*store.Run, error) {
	var out []*store.Run
	for _, r := range f.runs {
		if r.Repo == repo && r.IssueNumber == issue {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeSource) GetRun(id int64) (*store.Run, error) {
	for _, r := range f.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}
func (f *fakeSource) Active() *Active { return f.active }
func (f *fakeSource) QueueDepth() int { return f.queue }
func (f *fakeSource) LogDir() string  { return f.logDir }

func newTestServer(t *testing.T) (*httptest.Server, *fakeSource) {
	t.Helper()
	logDir := t.TempDir()
	src := &fakeSource{
		logDir: logDir,
		items: []*store.Item{
			{Repo: "o/r", IssueNumber: 1, LastStatus: "🚧 In Progress", PRNumber: 7,
				Branch: "feat/x", RetryCount: 2, UpdatedAt: time.Now()},
			{Repo: "o/r", IssueNumber: 2, LastStatus: "✅ Done", Terminal: true},
		},
		runs: []*store.Run{
			{ID: 1, Repo: "o/r", IssueNumber: 1, Phase: "implement",
				StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now().Add(-time.Minute), Result: "ok"},
			{ID: 2, Repo: "o/r", IssueNumber: 1, Phase: "review", StartedAt: time.Now()},
		},
	}
	srv := httptest.NewServer(New(src, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)
	return srv, src
}

func get(t *testing.T, srv *httptest.Server, path string, into any) int {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	if into != nil {
		if err := json.NewDecoder(res.Body).Decode(into); err != nil {
			t.Fatalf("GET %s の JSON 解析に失敗: %v", path, err)
		}
	}
	return res.StatusCode
}

// 参照専用であることは UI の約束なので、更新系のメソッドは必ず弾く。
func TestRejectsMutatingMethods(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, srv.URL+"/api/state", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s が %d で通っています", method, res.StatusCode)
		}
	}
}

func TestStateGroupsItemsAndRuns(t *testing.T) {
	srv, src := newTestServer(t)
	src.queue = 3
	src.active = &Active{RunID: 2, Phase: "review", Repo: "o/r", Issue: 1, StartedAt: time.Now()}

	var got stateResponse
	if code := get(t, srv, "/api/state", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(got.Items) != 2 {
		t.Fatalf("item 数 = %d", len(got.Items))
	}
	if got.QueueDepth != 3 || got.Active == nil || got.Active.RunID != 2 {
		t.Errorf("実行中の情報が反映されていません: %+v", got)
	}

	var first *itemView
	for i := range got.Items {
		if got.Items[i].Issue == 1 {
			first = &got.Items[i]
		}
	}
	if first == nil {
		t.Fatal("Issue 1 がありません")
	}
	if !first.Running {
		t.Error("実行中の item に印が付いていません")
	}
	// 最新の run（review）が選ばれること。
	if first.LastRun == nil || first.LastRun.Phase != "review" || !first.LastRun.Running {
		t.Errorf("直近の run が不正: %+v", first.LastRun)
	}
	if first.IssueURL != "https://github.com/o/r/issues/1" || first.PRURL != "https://github.com/o/r/pull/7" {
		t.Errorf("GitHub への URL が不正: %+v", first)
	}
	// ゼロ値の時刻は null になること。
	if first.LeaseUntil != nil {
		t.Errorf("未設定の時刻が null になっていません: %v", first.LeaseUntil)
	}
}

func TestItemEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	var got itemResponse
	if code := get(t, srv, "/api/item?repo=o%2Fr&issue=1", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.Item == nil || got.Item.Issue != 1 || got.Item.Branch != "feat/x" {
		t.Errorf("item が不正: %+v", got.Item)
	}
	if len(got.Runs) != 2 {
		t.Errorf("履歴が %d 件", len(got.Runs))
	}

	if code := get(t, srv, "/api/item?repo=o%2Fr&issue=99", nil); code != http.StatusNotFound {
		t.Errorf("存在しない Issue が %d", code)
	}
	if code := get(t, srv, "/api/item?repo=o%2Fr", nil); code != http.StatusBadRequest {
		t.Errorf("issue 未指定が %d", code)
	}
}

func TestRunEndpointReadsLog(t *testing.T) {
	srv, src := newTestServer(t)

	path := filepath.Join(src.logDir, "run.log")
	content := "=== phase=implement ===\n" + agent.LogPromptSep + "\nプロンプト本文\n" +
		agent.LogOutputSep + "\n出力本文\nAUTOPILOT_ACTION: PR_READY\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src.runs[0].LogPath = path

	var got runResponse
	if code := get(t, srv, "/api/run?id=1", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.LogError != "" {
		t.Fatalf("ログの読み出しに失敗: %s", got.LogError)
	}
	if got.Log == nil || got.Log.Prompt != "プロンプト本文" {
		t.Errorf("プロンプトが不正: %+v", got.Log)
	}
	if !strings.Contains(got.Log.Output, "AUTOPILOT_ACTION: PR_READY") {
		t.Errorf("出力が不正: %+v", got.Log)
	}
	if !got.Run.HasLog {
		t.Error("ログありの印が付いていません")
	}

	// 追記の取得。
	var chunk logChunkResponse
	if code := get(t, srv, "/api/run/log?id=1&offset=0", &chunk); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if chunk.Data != content || chunk.Next != int64(len(content)) {
		t.Errorf("追記取得が不正: next=%d len=%d", chunk.Next, len(chunk.Data))
	}
	// 終了済みの run では追従を止めさせる。
	if chunk.Running {
		t.Error("終了済みの run が実行中と返っています")
	}

	// 実行中なら追従を続けさせる。これが無いと画面が /api/run を引き直すことになる。
	src.active = &Active{RunID: 1, Repo: "o/r", Issue: 1, LogPath: path}
	chunk = logChunkResponse{}
	get(t, srv, "/api/run/log?id=1&offset=0", &chunk)
	if !chunk.Running {
		t.Error("実行中の run が実行中と返っていません")
	}
}

// ログの無い run と、存在しない run。
func TestRunEndpointWithoutLog(t *testing.T) {
	srv, _ := newTestServer(t)

	var got runResponse
	if code := get(t, srv, "/api/run?id=2", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.Log != nil || got.LogError != "" {
		t.Errorf("ログが無い run で何か返っています: %+v", got)
	}
	if code := get(t, srv, "/api/run?id=404", nil); code != http.StatusNotFound {
		t.Errorf("存在しない run が %d", code)
	}
	if code := get(t, srv, "/api/run/log?id=2", nil); code != http.StatusNotFound {
		t.Errorf("ログの無い run の追記取得が %d", code)
	}
}

// DB が壊れてログディレクトリの外を指していても読み出さない。
func TestLogPathStaysInsideLogDir(t *testing.T) {
	srv, src := newTestServer(t)

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("これは漏れてはいけない"), 0o644); err != nil {
		t.Fatal(err)
	}
	src.runs[0].LogPath = outside

	var got runResponse
	get(t, srv, "/api/run?id=1", &got)
	if got.Log != nil {
		t.Fatalf("外部のファイルを読み出しています: %+v", got.Log)
	}
	if !strings.Contains(got.LogError, "外") {
		t.Errorf("拒否の理由が示されていません: %q", got.LogError)
	}

	// 相対パスによる脱出も同様に弾く。
	src.runs[0].LogPath = filepath.Join(src.logDir, "..", filepath.Base(outside))
	got = runResponse{}
	get(t, srv, "/api/run?id=1", &got)
	if got.Log != nil {
		t.Errorf("相対パスで脱出できています: %+v", got.Log)
	}
}

// 実行中は DB にパスが書き戻される前でも、メモリ上の情報からログを読める。
func TestRunningLogFallsBackToActive(t *testing.T) {
	srv, src := newTestServer(t)

	path := filepath.Join(src.logDir, "live.log")
	content := "=== phase=review ===\n" + agent.LogPromptSep + "\n実行中のプロンプト\n" + agent.LogOutputSep + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src.active = &Active{RunID: 2, Phase: "review", Repo: "o/r", Issue: 1, LogPath: path, StartedAt: time.Now()}

	var got runResponse
	if code := get(t, srv, "/api/run?id=2", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.Log == nil || got.Log.Prompt != "実行中のプロンプト" {
		t.Fatalf("実行中のプロンプトを読めません: %+v", got)
	}
	if !got.Run.Running {
		t.Error("実行中の印が付いていません")
	}
	if got.Log.Output != "" {
		t.Errorf("出力がまだ無いはずです: %q", got.Log.Output)
	}
}

func TestServesIndexAndAssets(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, path := range []string{"/", "/assets/app.js", "/assets/style.css"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s が %d (%d バイト)", path, res.StatusCode, len(body))
		}
	}
	if code := get(t, srv, "/does-not-exist", nil); code != http.StatusNotFound {
		t.Errorf("未知のパスが %d", code)
	}
}
