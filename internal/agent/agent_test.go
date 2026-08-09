package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"nuage-autopilot2/internal/config"
)

func TestParseMarkers(t *testing.T) {
	out := `実装を進めました。
いくつかテストを追加しています。

AUTOPILOT_ACTION: PR_READY
AUTOPILOT_REASON: ログイン画面の文言を修正した
`
	m := parseMarkers(out)
	if m[MarkerAction] != "PR_READY" {
		t.Errorf("action = %q, want PR_READY", m[MarkerAction])
	}
	if m[MarkerReason] != "ログイン画面の文言を修正した" {
		t.Errorf("reason = %q", m[MarkerReason])
	}
}

func TestParseMarkersTakesLastOccurrence(t *testing.T) {
	// プロンプトの引用などで先に現れた値ではなく、最後の出力を採用する。
	out := "AUTOPILOT_VERDICT: <PASS | FAIL>\n...\nAUTOPILOT_VERDICT: PASS\n"
	if got := parseMarkers(out)[MarkerVerdict]; got != "PASS" {
		t.Errorf("verdict = %q, want PASS", got)
	}
}

func TestParseMarkersIgnoresInlineMentions(t *testing.T) {
	// 行頭でないマーカーは拾わない。
	out := "この行には AUTOPILOT_ACTION: BLOCKED という文字列が含まれるが行頭ではない\n"
	if got := parseMarkers(out)[MarkerAction]; got != "" {
		t.Errorf("行中のマーカーを拾っています: %q", got)
	}
}

func TestRunCapturesOutputAndMarkers(t *testing.T) {
	r := New(t.TempDir(), nil)
	a := config.Agent{
		Command: "sh",
		Args:    []string{"-c", `cat > /dev/null; echo "done"; echo "AUTOPILOT_VERDICT: PASS"`},
		Timeout: 10 * time.Second,
	}
	res, err := r.Run(context.Background(), a, "review", t.TempDir(), "テストプロンプト")
	if err != nil {
		t.Fatalf("Run に失敗: %v", err)
	}
	if !strings.Contains(res.Stdout, "done") {
		t.Errorf("stdout が取れていません: %q", res.Stdout)
	}
	if res.Marker(MarkerVerdict) != "PASS" {
		t.Errorf("verdict = %q", res.Marker(MarkerVerdict))
	}
	if res.LogPath == "" {
		t.Error("ログファイルが作られていません")
	}
}

func TestRunPassesPromptOnStdin(t *testing.T) {
	r := New("", nil)
	a := config.Agent{Command: "cat", Timeout: 10 * time.Second}
	res, err := r.Run(context.Background(), a, "refine", t.TempDir(), "こんにちは")
	if err != nil {
		t.Fatalf("Run に失敗: %v", err)
	}
	if !strings.Contains(res.Stdout, "こんにちは") {
		t.Errorf("プロンプトが stdin に渡っていません: %q", res.Stdout)
	}
}

func TestRunReportsExitFailure(t *testing.T) {
	r := New("", nil)
	a := config.Agent{Command: "sh", Args: []string{"-c", "exit 3"}, Timeout: 10 * time.Second}
	res, err := r.Run(context.Background(), a, "implement", t.TempDir(), "")
	if err == nil {
		t.Fatal("異常終了がエラーになりません")
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}
}

func TestRunTimesOut(t *testing.T) {
	r := New("", nil)
	a := config.Agent{Command: "sleep", Args: []string{"5"}, Timeout: 200 * time.Millisecond}
	res, err := r.Run(context.Background(), a, "implement", t.TempDir(), "")
	if err == nil {
		t.Fatal("タイムアウトがエラーになりません")
	}
	if !res.TimedOut {
		t.Error("TimedOut が立っていません")
	}
}
