// Package agent はコーディングエージェント CLI（claude 等）を起動する。
//
// どのエージェントを使うかは設定ファイルで差し替えられる。プロンプトは stdin
// で渡し、判断結果は出力中のマーカー行（KEY: VALUE）で受け取る。
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

	"nuage-autopilot2/internal/config"
)

// Runner はエージェントプロセスを起動する。
type Runner struct {
	logDir string
	// BaseEnv は全エージェントに渡す環境変数（os.Environ() ベース）。
	BaseEnv []string
}

// New は Runner を作る。
func New(logDir string, baseEnv []string) *Runner {
	return &Runner{logDir: logDir, BaseEnv: baseEnv}
}

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
// workDir でプロセスを動かし、prompt を stdin へ書き込む。タイムアウトすると
// プロセスを終了させ、TimedOut を立てた Result を返す（error も返す）。
func (r *Runner) Run(ctx context.Context, a config.Agent, phase, workDir, prompt string) (*Result, error) {
	if a.Command == "" {
		return nil, fmt.Errorf("エージェントのコマンドが未設定です (phase: %s)", phase)
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, a.Command, a.Args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)

	env := r.BaseEnv
	if env == nil {
		env = os.Environ()
	}
	for k, v := range a.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	logPath, logFile := r.openLog(phase, workDir)
	if logFile != nil {
		defer logFile.Close()
		fmt.Fprintf(logFile, "=== phase=%s cmd=%s %s dir=%s at=%s ===\n--- prompt ---\n%s\n--- output ---\n",
			phase, a.Command, strings.Join(a.Args, " "), workDir, time.Now().Format(time.RFC3339), prompt)
	}

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
