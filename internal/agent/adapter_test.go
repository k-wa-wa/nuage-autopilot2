package agent

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func build(t *testing.T, command string, req Request) Invocation {
	t.Helper()
	inv, err := AdapterForCommand(command).Build(req)
	if err != nil {
		t.Fatalf("Build(%q): %v", command, err)
	}
	return inv
}

// claude はプロンプトを stdin で受け取る。
func TestClaudeAdapterPassesPromptOnStdin(t *testing.T) {
	inv := build(t, AdapterClaude, Request{Prompt: "やってください", Timeout: time.Hour})

	if inv.Stdin != "やってください" {
		t.Errorf("stdin = %q", inv.Stdin)
	}
	if slices.Contains(inv.Args, "やってください") {
		t.Error("プロンプトが引数にも渡っています")
	}
	if !slices.Contains(inv.Args, "-p") || !slices.Contains(inv.Args, "--dangerously-skip-permissions") {
		t.Errorf("必須フラグが足りません: %v", inv.Args)
	}
}

// agy はプロンプトを --print の引数で受け取る（stdin は読まない）。
func TestAgyAdapterPassesPromptAsArgument(t *testing.T) {
	inv := build(t, AdapterAgy, Request{Prompt: "やってください", Timeout: time.Hour})

	if inv.Stdin != "" {
		t.Errorf("agy に stdin を渡しています: %q", inv.Stdin)
	}
	i := slices.Index(inv.Args, "--print")
	if i < 0 || i+1 >= len(inv.Args) {
		t.Fatalf("--print がありません: %v", inv.Args)
	}
	if inv.Args[i+1] != "やってください" {
		t.Errorf("--print の引数 = %q", inv.Args[i+1])
	}
	if !slices.Contains(inv.Args, "--dangerously-skip-permissions") {
		t.Errorf("権限スキップが足りません: %v", inv.Args)
	}
	// Issue 本文の "/..." がスラッシュコマンドとして展開されないようにする。
	if !slices.Contains(inv.Args, "--disable-slash-commands") {
		t.Errorf("--disable-slash-commands がありません: %v", inv.Args)
	}
}

// agy の print モードは既定 5 分で打ち切られるため、ワーカーの上限に合わせる。
func TestAgyAdapterAlignsPrintTimeout(t *testing.T) {
	inv := build(t, AdapterAgy, Request{Prompt: "x", Timeout: 2 * time.Hour})
	i := slices.Index(inv.Args, "--print-timeout")
	if i < 0 || i+1 >= len(inv.Args) {
		t.Fatalf("--print-timeout がありません: %v", inv.Args)
	}
	got, err := time.ParseDuration(inv.Args[i+1])
	if err != nil {
		t.Fatalf("解釈できない値 %q: %v", inv.Args[i+1], err)
	}
	// ワーカーが強制終了するより先に agy 自身に終わらせる。
	if got >= 2*time.Hour {
		t.Errorf("print-timeout = %v, ワーカーの上限より短くありません", got)
	}
	if got != 2*time.Hour-agyTimeoutMargin {
		t.Errorf("print-timeout = %v, want %v", got, 2*time.Hour-agyTimeoutMargin)
	}
}

// 極端に短い上限ではマージンを引かない（0 以下になるのを防ぐ）。
func TestAgyPrintTimeoutStaysPositive(t *testing.T) {
	for _, d := range []time.Duration{time.Second, 10 * time.Second, agyTimeoutMargin} {
		if got := agyPrintTimeout(d); got <= 0 {
			t.Errorf("agyPrintTimeout(%v) = %v", d, got)
		}
	}
	if got := agyPrintTimeout(0); got != 0 {
		t.Errorf("上限なしのとき = %v, want 0", got)
	}
}

func TestModelIsPassedWhenSet(t *testing.T) {
	for _, kind := range []string{AdapterClaude, AdapterAgy} {
		inv := build(t, kind, Request{Prompt: "x", Model: "some-model", Timeout: time.Minute})
		i := slices.Index(inv.Args, "--model")
		if i < 0 || inv.Args[i+1] != "some-model" {
			t.Errorf("%s: model が渡っていません: %v", kind, inv.Args)
		}
		// 未指定なら --model 自体を付けない（CLI の既定に任せる）。
		plain := build(t, kind, Request{Prompt: "x", Timeout: time.Minute})
		if slices.Contains(plain.Args, "--model") {
			t.Errorf("%s: model 未指定なのに --model が付いています: %v", kind, plain.Args)
		}
	}
}

func TestExtraArgsComeLast(t *testing.T) {
	for _, kind := range []string{AdapterClaude, AdapterAgy} {
		inv := build(t, kind, Request{Prompt: "x", ExtraArgs: []string{"--foo", "bar"}, Timeout: time.Minute})
		n := len(inv.Args)
		if n < 2 || inv.Args[n-2] != "--foo" || inv.Args[n-1] != "bar" {
			t.Errorf("%s: 追加引数が末尾にありません: %v", kind, inv.Args)
		}
	}
}

// exec は args をそのまま使い、プロンプトは stdin。
func TestExecAdapterIsPassthrough(t *testing.T) {
	inv := build(t, AdapterExec, Request{Prompt: "p", ExtraArgs: []string{"--print"}, Timeout: time.Minute})
	if inv.Stdin != "p" {
		t.Errorf("stdin = %q", inv.Stdin)
	}
	if len(inv.Args) != 1 || inv.Args[0] != "--print" {
		t.Errorf("args = %v", inv.Args)
	}
}

// Linux の MAX_ARG_STRLEN を超える前に、分かりやすいエラーで止める。
func TestAgyRejectsOversizedPrompt(t *testing.T) {
	_, err := AdapterForCommand(AdapterAgy).Build(Request{Prompt: strings.Repeat("あ", maxArgvPromptBytes), Timeout: time.Minute})
	if err == nil {
		t.Fatal("巨大なプロンプトがエラーになりません")
	}
	if !strings.Contains(err.Error(), "context_comments") {
		t.Errorf("対処方法が示されていません: %v", err)
	}
	// stdin で渡す claude 側は制限を受けない。
	if _, err := build2(AdapterClaude, Request{Prompt: strings.Repeat("あ", maxArgvPromptBytes), Timeout: time.Minute}); err != nil {
		t.Errorf("claude が不要に制限されています: %v", err)
	}
}

func build2(command string, req Request) (Invocation, error) {
	return AdapterForCommand(command).Build(req)
}

func TestDisplayArgsHidesPrompt(t *testing.T) {
	inv := build(t, AdapterAgy, Request{Prompt: "秘密の長いプロンプト", Timeout: time.Minute})
	shown := strings.Join(inv.DisplayArgs(), " ")
	if strings.Contains(shown, "秘密の長いプロンプト") {
		t.Errorf("ログ表示にプロンプトが残っています: %s", shown)
	}
	if !strings.Contains(shown, "<prompt>") {
		t.Errorf("プレースホルダがありません: %s", shown)
	}
	// stdin 渡しのアダプタは引数にプロンプトが無いので、そのまま表示される。
	c := build(t, AdapterClaude, Request{Prompt: "x", Timeout: time.Minute})
	if strings.Join(c.DisplayArgs(), " ") != strings.Join(c.Args, " ") {
		t.Error("claude の引数が不要に伏せられています")
	}
}

// command からアダプタを解決する。パス指定でもコマンド名で判定する。
func TestAdapterForCommand(t *testing.T) {
	cases := map[string]string{
		"claude":                     AdapterClaude,
		"agy":                        AdapterAgy,
		"/home/nixos/.local/bin/agy": AdapterAgy,
		"/usr/local/bin/claude":      AdapterClaude,
		"copilot":                    AdapterExec,
		"":                           AdapterExec,
	}
	for command, want := range cases {
		if got := AdapterForCommand(command).Name(); got != want {
			t.Errorf("AdapterForCommand(%q) = %s, want %s", command, got, want)
		}
	}
}

// command 未指定なら claude を使う。
func TestSpecDefaultsToClaude(t *testing.T) {
	s := Spec{}
	if got := s.ResolvedCommand(); got != DefaultCommand {
		t.Errorf("command 省略時 = %q, want %q", got, DefaultCommand)
	}
	if got := s.Adapter().Name(); got != AdapterClaude {
		t.Errorf("既定のアダプタ = %s, want %s", got, AdapterClaude)
	}
	if got := (Spec{Command: "/opt/agy"}).ResolvedCommand(); got != "/opt/agy" {
		t.Errorf("command 指定時 = %q", got)
	}
	if got := (Spec{Command: "/opt/agy"}).Adapter().Name(); got != AdapterAgy {
		t.Errorf("パス指定のアダプタ = %s, want %s", got, AdapterAgy)
	}
}
