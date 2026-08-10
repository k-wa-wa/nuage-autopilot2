package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runAndLog は sh でエージェントを模して 1 回実行し、書かれたログのパスを返す。
func runAndLog(t *testing.T, prompt, script string) string {
	t.Helper()
	dir := t.TempDir()
	r := New(filepath.Join(dir, "logs"), os.Environ())
	spec := Spec{Command: "sh", ExtraArgs: []string{"-c", script}, Timeout: 30 * time.Second}
	res, err := r.Run(context.Background(), spec, "implement", dir, prompt)
	if err != nil {
		t.Fatalf("Run に失敗: %v", err)
	}
	if res.LogPath == "" {
		t.Fatal("ログが書かれていません")
	}
	return res.LogPath
}

func TestReadLogSplitsPromptAndOutput(t *testing.T) {
	prompt := "実装してください。\n\nAUTOPILOT_ACTION: <PR_READY | BLOCKED>"
	path := runAndLog(t, prompt, `printf 'やりました\nAUTOPILOT_ACTION: PR_READY\n'`)

	v, err := ReadLog(path, 0, 0)
	if err != nil {
		t.Fatalf("ReadLog に失敗: %v", err)
	}
	if !strings.HasPrefix(v.Header, "=== phase=implement") {
		t.Errorf("ヘッダが取れていません: %q", v.Header)
	}
	if v.Prompt != prompt {
		t.Errorf("プロンプトが往復で壊れています:\n got: %q\nwant: %q", v.Prompt, prompt)
	}
	if !strings.Contains(v.Output, "AUTOPILOT_ACTION: PR_READY") {
		t.Errorf("出力が取れていません: %q", v.Output)
	}
	// プロンプト側の区切り行が出力に混ざっていないこと。
	if strings.Contains(v.Output, LogOutputSep) {
		t.Errorf("区切り行が出力に混入しています: %q", v.Output)
	}
	if v.PromptTruncated || v.OutputTruncated {
		t.Errorf("小さなログが打ち切り扱いになっています: %+v", v)
	}
}

// 実行中はまだ出力が無い。その状態でもプロンプトだけは読めなければならない。
func TestReadLogBeforeOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "running.log")
	content := "=== phase=implement ===\n" + LogPromptSep + "\nこれがプロンプト\n" + LogOutputSep + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := ReadLog(path, 0, 0)
	if err != nil {
		t.Fatalf("ReadLog に失敗: %v", err)
	}
	if v.Prompt != "これがプロンプト" {
		t.Errorf("プロンプト = %q", v.Prompt)
	}
	if v.Output != "" {
		t.Errorf("出力が空ではありません: %q", v.Output)
	}
}

func TestReadLogTailsLargeOutput(t *testing.T) {
	path := runAndLog(t, "p", `i=0; while [ $i -lt 4000 ]; do echo "line $i"; i=$((i+1)); done`)

	v, err := ReadLog(path, 0, 1024)
	if err != nil {
		t.Fatalf("ReadLog に失敗: %v", err)
	}
	if !v.OutputTruncated {
		t.Fatal("上限を超えているのに打ち切りになっていません")
	}
	if int64(len(v.Output)) > 1024 {
		t.Errorf("上限を超えて読んでいます: %d バイト", len(v.Output))
	}
	if !strings.HasSuffix(strings.TrimRight(v.Output, "\n"), "line 3999") {
		t.Errorf("末尾が取れていません: %q", v.Output[max(0, len(v.Output)-40):])
	}
}

func TestReadLogFromFollowsAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := ReadLogFrom(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Data != "first\n" || c.Next != 6 {
		t.Fatalf("初回読み出しが不正: %+v", c)
	}

	// 追記が無ければ空で返り、offset は動かない。
	c, err = ReadLogFrom(path, c.Next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Data != "" || c.Next != 6 {
		t.Fatalf("追記が無いのに読み出しています: %+v", c)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("second\n")
	f.Close()

	c, err = ReadLogFrom(path, c.Next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Data != "second\n" {
		t.Errorf("追記分だけが返っていません: %q", c.Data)
	}
}

// ログが作り直されて短くなった場合は先頭から読み直す。
func TestReadLogFromResetsOnShrink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("xy"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := ReadLogFrom(path, 999, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Data != "xy" {
		t.Errorf("先頭から読み直していません: %q", c.Data)
	}
}

// 末尾を切り出すと多バイト文字の途中から始まることがある。
func TestTrimPartialRune(t *testing.T) {
	b := []byte("あい")
	got := string(trimPartialRune(b[1:]))
	if got != "い" {
		t.Errorf("断片が落ちていません: %q", got)
	}
	if got := string(trimPartialRune([]byte("ok"))); got != "ok" {
		t.Errorf("正常な先頭を削っています: %q", got)
	}
}

func TestOnStartReceivesLogPath(t *testing.T) {
	dir := t.TempDir()
	r := New(filepath.Join(dir, "logs"), os.Environ())

	var got RunInfo
	calls := 0
	r.OnStart = func(info RunInfo) {
		calls++
		got = info
		// プロセス起動前にプロンプトが読めていること。
		v, err := ReadLog(info.LogPath, 0, 0)
		if err != nil {
			t.Errorf("起動時点でログを読めません: %v", err)
			return
		}
		if v.Prompt != "hello" {
			t.Errorf("起動時点のプロンプト = %q", v.Prompt)
		}
	}

	spec := Spec{Command: "sh", ExtraArgs: []string{"-c", "echo done"}, Timeout: 30 * time.Second}
	res, err := r.Run(context.Background(), spec, "review", dir, "hello")
	if err != nil {
		t.Fatalf("Run に失敗: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnStart の呼び出し回数 = %d, want 1", calls)
	}
	if got.LogPath != res.LogPath {
		t.Errorf("OnStart のログパス = %q, Result = %q", got.LogPath, res.LogPath)
	}
	if got.Phase != "review" || got.StartedAt.IsZero() {
		t.Errorf("起動情報が不正: %+v", got)
	}
}

// OnStart が nil でも従来どおり動くこと。
func TestRunWithoutOnStart(t *testing.T) {
	dir := t.TempDir()
	r := New(filepath.Join(dir, "logs"), os.Environ())
	spec := Spec{Command: "sh", ExtraArgs: []string{"-c", "echo AUTOPILOT_VERDICT: PASS"}, Timeout: 30 * time.Second}
	res, err := r.Run(context.Background(), spec, "review", dir, "p")
	if err != nil {
		t.Fatalf("Run に失敗: %v", err)
	}
	if res.Marker(MarkerVerdict) != "PASS" {
		t.Errorf("マーカーが取れていません: %+v", res.Markers)
	}
}
