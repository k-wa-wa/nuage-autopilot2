// Package agent はコーディングエージェント CLI（claude / agy 等）を起動する。
//
// CLI ごとの起動方法の違い（プロンプトを stdin で渡すか argv で渡すか、
// CLI 側の内部タイムアウトを揃える必要があるか）は Adapter が吸収する。
// 判断結果は出力中のマーカー行（KEY: VALUE）で受け取る。
//
// このパッケージは他の内部パッケージに依存しない。
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Runner はエージェントプロセスを起動する。
type Runner struct {
	logDir string
	// BaseEnv は全エージェントに渡す環境変数（os.Environ() ベース）。
	BaseEnv []string
	// OnStart はエージェントプロセスの起動直前に呼ばれる。nil なら何もしない。
	//
	// 実行中のプロンプトを参照 UI から読めるようにするためのフック。
	// 起動を待たせないよう、実装は即座に返ること。
	OnStart func(RunInfo)
}

// RunInfo は起動しようとしているエージェントプロセスの情報。
type RunInfo struct {
	Phase string
	// LogPath は出力先のログファイル。ログを取れない場合は空。
	//
	// このパスは openLog を呼ぶまで決まらないため、実行を開始した側は
	// このフックを経由しないと実行中のログに辿り着けない。
	LogPath   string
	StartedAt time.Time
}

// New は Runner を作る。
func New(logDir string, baseEnv []string) *Runner {
	return &Runner{logDir: logDir, BaseEnv: baseEnv}
}

// LogDir はログの出力先ディレクトリを返す。ログ無効なら空。
func (r *Runner) LogDir() string { return r.logDir }

// Result はエージェント実行の結果。
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Markers はエージェントが出力した判断マーカー（AUTOPILOT_* 行）。
	Markers map[string]string
	// LogPath は全出力を保存したファイルのパス。
	LogPath  string
	Duration time.Duration
	TimedOut bool
}

// Marker はマーカー値を返す。
func (r *Result) Marker(key string) string { return r.Markers[key] }

// Tail は出力の末尾 n 文字を返す。Blocked コメントに載せる用。
func (r *Result) Tail(n int) string {
	s := strings.TrimSpace(r.Stdout)
	if s == "" {
		s = strings.TrimSpace(r.Stderr)
	}
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// マーカーのキー。エージェントにはプロンプトでこの形式の出力を要求する。
const (
	// MarkerVerdict は検証結果。PASS / FAIL。
	MarkerVerdict = "AUTOPILOT_VERDICT"
	// MarkerAction は次に取るべき行動。ハンドラごとに解釈が異なる。
	MarkerAction = "AUTOPILOT_ACTION"
	// MarkerReason は判断理由（1 行）。
	MarkerReason = "AUTOPILOT_REASON"
)

var markerRe = regexp.MustCompile(`(?m)^\s*(AUTOPILOT_[A-Z_]+)\s*:\s*(.+?)\s*$`)

// Run はエージェントを起動し、完了を待つ。
//
// workDir でプロセスを動かし、プロンプトの渡し方はアダプタに委ねる。
// タイムアウトするとプロセスを終了させ、TimedOut を立てた Result を返す（error も返す）。
func (r *Runner) Run(ctx context.Context, s Spec, phase, workDir, prompt string) (*Result, error) {
	command := s.ResolvedCommand()
	adapter := s.Adapter()
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	inv, err := adapter.Build(Request{
		Prompt:    prompt,
		Model:     s.Model,
		ExtraArgs: s.ExtraArgs,
		Timeout:   timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("エージェントの起動引数を組み立てられません (phase: %s): %w", phase, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, command, inv.Args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(inv.Stdin)

	env := r.BaseEnv
	if env == nil {
		env = os.Environ()
	}
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	logPath, logFile := r.openLog(phase, workDir)
	if logFile != nil {
		defer logFile.Close()
		// プロンプトは別途まとめて出すため、引数側では伏せる。
		fmt.Fprintf(logFile, "=== phase=%s adapter=%s cmd=%s %s dir=%s at=%s ===\n%s\n%s\n%s\n",
			phase, adapter.Name(), command, strings.Join(inv.DisplayArgs(), " "),
			workDir, time.Now().Format(time.RFC3339), LogPromptSep, prompt, LogOutputSep)
	}

	// ヘッダとプロンプトを書き終えてから通知する。参照側が読んだ時点で
	// 少なくともプロンプトは揃っていることを保証するため。
	if r.OnStart != nil {
		r.OnStart(RunInfo{Phase: phase, LogPath: logPath, StartedAt: time.Now()})
	}

	// ログへの書き込みはバッファを挟まないが、それでも出力が逐次現れるとは限らない。
	//
	// claude と agy はいずれも print / 非対話モードでは、完了の瞬間まで標準出力に
	// 1 バイトも書かない（ツール実行の途中経過も出ない）。実測で確認済みである。
	// したがって実行中のログはヘッダとプロンプトだけで、出力は終了時に一括で現れる。
	//
	// 逐次表示が要る場合は --output-format stream-json を使うことになるが、
	// そうするとマーカー行が JSON 文字列の中に入り、parseMarkers の行頭一致から
	// 外れて判断機構が壊れる。切り替えるなら、ここで JSON を受けて従来と同じ
	// text に再構成し、Result.Stdout の中身を変えないようにする必要がある。
	var stdout, stderr bytes.Buffer
	if logFile != nil {
		cmd.Stdout = io.MultiWriter(&stdout, logFile)
		cmd.Stderr = io.MultiWriter(&stderr, logFile)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	start := time.Now()
	runErr := cmd.Run()
	res := &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		LogPath:  logPath,
		Duration: time.Since(start),
		Markers:  parseMarkers(stdout.String()),
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		return res, fmt.Errorf("エージェントが %s でタイムアウトしました (phase: %s)", timeout, phase)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
		}
		return res, fmt.Errorf("エージェントが異常終了しました (phase: %s, exit: %d): %w", phase, res.ExitCode, runErr)
	}
	return res, nil
}

func (r *Runner) openLog(phase, workDir string) (string, *os.File) {
	if r.logDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(r.logDir, 0o755); err != nil {
		return "", nil
	}
	name := fmt.Sprintf("%s-%s-%s.log", time.Now().Format("20060102-150405"), phase, filepath.Base(workDir))
	path := filepath.Join(r.logDir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", nil
	}
	return path, f
}

func parseMarkers(s string) map[string]string {
	out := map[string]string{}
	for _, m := range markerRe.FindAllStringSubmatch(s, -1) {
		// 同じキーが複数ある場合は最後の出力を採用する。
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}
