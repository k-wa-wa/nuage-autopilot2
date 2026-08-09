package agent

import (
	"fmt"
	"path/filepath"
	"time"
)

// アダプタ名。設定の command から解決した結果を表示・ログに使う。
const (
	// AdapterClaude は Claude Code CLI 用。プロンプトを標準入力で渡す。
	AdapterClaude = "claude"
	// AdapterAgy は agy CLI 用。プロンプトを --print の引数で渡す。
	AdapterAgy = "agy"
	// AdapterExec は専用アダプタが無いコマンド用。プロンプトを標準入力で渡す。
	AdapterExec = "exec"
)

// DefaultCommand は command 未指定時に使うコマンド。
const DefaultCommand = "claude"

// Spec は 1 つの用途で使うエージェント CLI の起動設定。
type Spec struct {
	// Command は実行するコマンド。パスでもよい。空なら DefaultCommand。
	Command string
	// Model は使用するモデル。空なら CLI の既定に任せる。
	Model string
	// ExtraArgs はアダプタが組み立てた引数の後ろに付く追加引数。
	ExtraArgs []string
	// Env は追加の環境変数。
	Env map[string]string
	// Timeout はワーカーが課す 1 回あたりの上限。
	Timeout time.Duration
}

// ResolvedCommand は実際に起動するコマンドを返す。
func (s Spec) ResolvedCommand() string {
	if s.Command != "" {
		return s.Command
	}
	return DefaultCommand
}

// Adapter は起動方法を決めるアダプタを返す。
func (s Spec) Adapter() Adapter { return AdapterForCommand(s.ResolvedCommand()) }

// Request はアダプタへの起動要求。
type Request struct {
	Prompt    string
	Model     string
	ExtraArgs []string
	// Timeout はワーカー側の上限。CLI 側にも内部タイムアウトがある場合に揃えるために渡す。
	Timeout time.Duration
}

// Invocation はアダプタが組み立てた起動内容。
type Invocation struct {
	// Args はコマンドライン引数。
	Args []string
	// Stdin は標準入力に流す内容。argv でプロンプトを渡すアダプタでは空。
	Stdin string
	// promptArg はログ表示でプロンプト本体を伏せるために保持する。
	promptArg string
}

// DisplayArgs はログ表示用に、巨大なプロンプト引数を伏せた引数列を返す。
func (i Invocation) DisplayArgs() []string {
	out := make([]string, len(i.Args))
	for n, a := range i.Args {
		if i.promptArg != "" && a == i.promptArg {
			out[n] = "<prompt>"
			continue
		}
		out[n] = a
	}
	return out
}

// Adapter はエージェント CLI ごとの起動方法の違いを吸収する。
//
// 差異の本体は「プロンプトを標準入力で渡すか argv で渡すか」と、
// 「CLI 側が独自に持つ内部タイムアウトを揃える必要があるか」の 2 点。
type Adapter interface {
	// Name はアダプタ名を返す。
	Name() string
	// Build は 1 回の起動に使う引数と標準入力を組み立てる。
	Build(req Request) (Invocation, error)
}

// AdapterForCommand はコマンド名からアダプタを選ぶ。
//
// 判定はパスを除いたコマンド名で行うため、`/home/nixos/.local/bin/agy` のような
// 絶対パス指定でも agy として扱える。既知でないコマンドは exec にフォールバックし、
// 引数をそのまま渡してプロンプトを標準入力に流す。
func AdapterForCommand(command string) Adapter {
	switch filepath.Base(command) {
	case AdapterClaude:
		return claudeAdapter{}
	case AdapterAgy:
		return agyAdapter{}
	default:
		return execAdapter{}
	}
}

// claudeAdapter は Claude Code CLI 用。
//
// `claude -p` はプロンプトを標準入力から読む。CLI 側に独自のタイムアウトは無い。
type claudeAdapter struct{}

func (claudeAdapter) Name() string { return AdapterClaude }

func (claudeAdapter) Build(req Request) (Invocation, error) {
	args := []string{"-p", "--dangerously-skip-permissions"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, req.ExtraArgs...)
	return Invocation{Args: args, Stdin: req.Prompt}, nil
}

// agyAdapter は agy CLI 用。
//
// claude と違い、プロンプトは --print の引数として渡す（`agy -p` は引数必須で、
// 標準入力は読まない）。また agy は print モードに独自のタイムアウトを持ち、
// 既定が 5 分と短い。放置すると実装フェーズが途中で打ち切られるため、
// ワーカー側の上限に合わせて明示的に指定する。
type agyAdapter struct{}

func (agyAdapter) Name() string { return AdapterAgy }

// agyTimeoutMargin は agy の内部タイムアウトをワーカーの上限より少し手前に置く幅。
//
// ワーカーがプロセスを強制終了するより先に agy 自身に終わらせることで、
// 打ち切り時にも agy の出力を Blocked コメントに残せる。
const agyTimeoutMargin = 30 * time.Second

func (agyAdapter) Build(req Request) (Invocation, error) {
	if err := checkArgvPromptSize(req.Prompt); err != nil {
		return Invocation{}, err
	}
	args := []string{
		"--print", req.Prompt,
		"--dangerously-skip-permissions",
		// Issue 本文やコメントをそのまま埋め込むため、本文中の "/..." が
		// スラッシュコマンドとして展開されないようにする。
		"--disable-slash-commands",
	}
	if t := agyPrintTimeout(req.Timeout); t > 0 {
		args = append(args, "--print-timeout", t.String())
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, req.ExtraArgs...)
	return Invocation{Args: args, promptArg: req.Prompt}, nil
}

func agyPrintTimeout(workerTimeout time.Duration) time.Duration {
	if workerTimeout <= 0 {
		return 0
	}
	if workerTimeout > agyTimeoutMargin*2 {
		return workerTimeout - agyTimeoutMargin
	}
	return workerTimeout
}

// execAdapter は専用アダプタが無いコマンド用のフォールバック。
//
// 引数は設定ファイルの args をそのまま使い、プロンプトは標準入力に流す。
// 非対話モードのフラグなどは args に自分で書く必要がある。
type execAdapter struct{}

func (execAdapter) Name() string { return AdapterExec }

func (execAdapter) Build(req Request) (Invocation, error) {
	return Invocation{Args: append([]string(nil), req.ExtraArgs...), Stdin: req.Prompt}, nil
}

// maxArgvPromptBytes は argv でプロンプトを渡すアダプタの上限。
//
// Linux は argv 1 要素あたり MAX_ARG_STRLEN = 128KiB の制限があり、超えると
// exec が E2BIG で失敗する。原因の分かりにくいエラーになる前に弾く。
const maxArgvPromptBytes = 100 * 1024

func checkArgvPromptSize(prompt string) error {
	if len(prompt) <= maxArgvPromptBytes {
		return nil
	}
	return fmt.Errorf(
		"プロンプトが %d バイトあり、コマンドライン引数の上限（%d バイト）を超えています。"+
			"limits.context_comments を減らすか、プロンプトを標準入力で渡すコマンド（%s）を使ってください",
		len(prompt), maxArgvPromptBytes, AdapterClaude)
}
