package agent

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestAdapterAgainstRealCLI は実際の CLI に対してアダプタの組み立てを検証する。
//
// 既定ではスキップする。アダプタを変更したときや新しい CLI に対応したときに、
// 実物へ通ることを確かめるために使う。
//
//	AGENT_SMOKE=agy go test ./internal/agent/ -run RealCLI -v
//	AGENT_SMOKE=claude AGENT_SMOKE_BIN=$HOME/.local/bin/claude go test ./internal/agent/ -run RealCLI -v
func TestAdapterAgainstRealCLI(t *testing.T) {
	name := os.Getenv("AGENT_SMOKE")
	if name == "" {
		t.Skip("AGENT_SMOKE にコマンド名（claude / agy）を指定すると実行する")
	}

	command := os.Getenv("AGENT_SMOKE_BIN")
	if command == "" {
		// シェルの alias は使えないので、PATH とホーム配下の既定位置を見る。
		if p, err := exec.LookPath(name); err == nil {
			command = p
		} else {
			command = os.Getenv("HOME") + "/.local/bin/" + name
		}
	}
	if _, err := os.Stat(command); err != nil {
		t.Skipf("%s が見つかりません: %v", command, err)
	}

	spec := Spec{Command: command, Timeout: 5 * time.Minute}
	t.Logf("command=%s adapter=%s", command, spec.Adapter().Name())
	prompt := "Reply with exactly these two lines and nothing else:\n" +
		MarkerVerdict + ": PASS\n" + MarkerReason + ": smoke test"

	res, err := New("", os.Environ()).Run(context.Background(), spec, "review", t.TempDir(), prompt)
	if err != nil {
		t.Fatalf("Run: %v (出力末尾: %s)", err, res.Tail(500))
	}
	t.Logf("duration=%s stdout=%q", res.Duration.Round(time.Second), res.Stdout)

	if got := res.Marker(MarkerVerdict); got != "PASS" {
		t.Errorf("%s = %q, want PASS（プロンプトが CLI に届いていない可能性）", MarkerVerdict, got)
	}
	if res.Marker(MarkerReason) == "" {
		t.Errorf("%s が取れていません", MarkerReason)
	}
}
