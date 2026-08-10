package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/web"
)

// waitFor は cond が真になるまで待つ。実行中の状態を覗くための補助。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s を待ちましたがタイムアウトしました", what)
}

// TestWebShowsRunningAgent は、エージェントの実行中にプロンプトまで参照できることを確かめる。
//
// print モードのエージェント CLI は完了まで出力しないため、実行中に見えるのは
// プロンプトと経過時間だけである。そこが壊れていないことがこの UI の生命線になる。
func TestWebShowsRunningAgent(t *testing.T) {
	fake := &fakeGitHub{prState: "OPEN", prCheckState: "SUCCESS", linkedPRNum: 2}

	// 合図のファイルが現れるまで終わらないエージェント。
	script := `#!/bin/sh
cat > /dev/null
while [ ! -f "$AUTOPILOT_TEST_GATE" ]; do sleep 0.02; done
echo "AUTOPILOT_ACTION: DONE"
`
	e, tmpDir, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	gate := filepath.Join(tmpDir, "gate")
	e.env = append(os.Environ(), "AUTOPILOT_TEST_GATE="+gate)
	e.runner.BaseEnv = e.env
	// engine.New と同じ配線をする。
	e.runner.OnStart = e.onAgentStart

	if err := e.st.Upsert(&store.Item{
		Repo: "owner/repo", IssueNumber: 1, ProjectItemID: "item_1", LastStatus: "📥 Inbox",
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(web.New(e, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()

	// 実行前は待機中。
	if e.Active() != nil {
		t.Fatalf("開始前から実行中になっています: %+v", e.Active())
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.runJob(context.Background(), Job{Phase: PhaseRefine, Repo: "owner/repo", Issue: 1})
	}()

	// エージェントプロセスが起動し、ログのパスが確定するまで待つ。
	waitFor(t, "エージェントの起動", func() bool {
		a := e.Active()
		return a != nil && a.LogPath != ""
	})

	active := e.Active()
	if active.Phase != PhaseRefine || active.Repo != "owner/repo" || active.Issue != 1 {
		t.Errorf("実行中ジョブの内容が不正: %+v", active)
	}
	if active.RunID == 0 {
		t.Error("run と紐づいていません")
	}
	if active.StartedAt.IsZero() || active.AgentStartedAt.IsZero() {
		t.Errorf("時刻が入っていません: %+v", active)
	}
	if !strings.HasPrefix(active.LogPath, filepath.Join(tmpDir, "logs")) {
		t.Errorf("ログのパスが想定外: %s", active.LogPath)
	}

	// ログのパスが runs にも書き戻されていること（実行後に辿れるようにするため）。
	run, err := e.GetRun(active.RunID)
	if err != nil || run == nil {
		t.Fatalf("run を読めません: %v", err)
	}
	if run.LogPath != active.LogPath {
		t.Errorf("runs に記録されたログのパス = %q, want %q", run.LogPath, active.LogPath)
	}

	// 実行中でもプロンプトが読めること。
	var got struct {
		Run struct {
			Running bool `json:"running"`
			Phase   string
		} `json:"run"`
		Log *struct {
			Prompt string `json:"prompt"`
			Output string `json:"output"`
		} `json:"log"`
		LogError string `json:"log_error"`
	}
	res, err := http.Get(srv.URL + "/api/run?id=" + strconv.FormatInt(active.RunID, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	if got.LogError != "" {
		t.Fatalf("実行中のログを読めません: %s", got.LogError)
	}
	if got.Log == nil || !strings.Contains(got.Log.Prompt, "owner/repo") {
		t.Errorf("実行中のプロンプトが取れていません: %+v", got.Log)
	}
	if !got.Run.Running {
		t.Error("実行中と判定されていません")
	}
	// print モードの CLI は完了まで出力しない。
	if got.Log.Output != "" {
		t.Logf("完了前に出力があります（CLI 依存）: %q", got.Log.Output)
	}

	// 一覧にも実行中として現れること。
	var state struct {
		QueueDepth int `json:"queue_depth"`
		Active     *struct {
			Issue int `json:"issue"`
		} `json:"active"`
		Items []struct {
			Issue   int  `json:"issue"`
			Running bool `json:"running"`
		} `json:"items"`
	}
	res, err = http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(res.Body).Decode(&state)
	res.Body.Close()
	if state.Active == nil || state.Active.Issue != 1 {
		t.Errorf("一覧に実行中が反映されていません: %+v", state.Active)
	}
	if len(state.Items) != 1 || !state.Items[0].Running {
		t.Errorf("item に実行中の印が付いていません: %+v", state.Items)
	}

	// エージェントを終わらせる。
	if err := os.WriteFile(gate, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	<-done

	if e.Active() != nil {
		t.Errorf("完了後も実行中のままです: %+v", e.Active())
	}
	// 完了後は出力まで読めること。
	res, err = http.Get(srv.URL + "/api/run?id=" + strconv.FormatInt(active.RunID, 10))
	if err != nil {
		t.Fatal(err)
	}
	got.Log = nil
	json.NewDecoder(res.Body).Decode(&got)
	res.Body.Close()
	if got.Log == nil || !strings.Contains(got.Log.Output, "AUTOPILOT_ACTION: DONE") {
		t.Errorf("完了後の出力が読めません: %+v", got.Log)
	}
	if got.Run.Running {
		t.Error("完了後も実行中と判定されています")
	}
}

// QueueDepth は投入済みで未処理のジョブ数を返す。
func TestQueueDepth(t *testing.T) {
	e := &Engine{jobs: make(chan Job, 8)}
	if e.QueueDepth() != 0 {
		t.Fatalf("初期値 = %d", e.QueueDepth())
	}
	e.jobs <- Job{Phase: PhaseRefine}
	e.jobs <- Job{Phase: PhaseReview}
	if e.QueueDepth() != 2 {
		t.Errorf("QueueDepth = %d, want 2", e.QueueDepth())
	}
}
