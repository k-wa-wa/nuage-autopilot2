package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nuage-autopilot2/internal/cron"
	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/summary"
)

// seedSummaryItems は各レーンにカードを 1 枚ずつ置く。
func seedSummaryItems(t *testing.T, e *Engine) {
	t.Helper()
	items := []*store.Item{
		{Repo: "owner/repo", IssueNumber: 1, LastStatus: "📥 Inbox"},
		{Repo: "owner/repo", IssueNumber: 2, LastStatus: "⏸ Blocked", RetryCount: 5},
		{Repo: "owner/repo", IssueNumber: 3, LastStatus: "👀 In Review", PRNumber: 9},
		{Repo: "owner/repo", IssueNumber: 4, LastStatus: "✅ Done", Terminal: true},
	}
	for _, it := range items {
		if err := e.st.Upsert(it); err != nil {
			t.Fatal(err)
		}
	}
}

// サマリ生成は GitHub を触らず、結果を DB に置くだけである。
func TestSummarize(t *testing.T) {
	fake := &fakeGitHub{}
	// フェンス付きの出力は summary パッケージ側で検証しているので、ここでは素の JSON を返す。
	script := `#!/bin/sh
cat > /dev/null
echo '調査しました。'
echo '{"headline":"対応待ち 2 件","todos":[{"repo":"owner/repo","issue":2,"title":"助言する","status":"⏸ Blocked","urgency":"HIGH","why":"CI が直らない","action":"方針をコメントする"}],"notes":"他は自走中"}'
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()
	seedSummaryItems(t, e)

	if err := e.runSummary(context.Background(), Job{Phase: PhaseSummarize}); err != nil {
		t.Fatalf("サマリ生成に失敗: %v", err)
	}

	sums, err := e.st.ListSummaries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("保存件数が %d", len(sums))
	}
	var report summary.Report
	if err := json.Unmarshal([]byte(sums[0].Payload), &report); err != nil {
		t.Fatalf("payload が JSON ではありません: %v", err)
	}
	if report.Headline != "対応待ち 2 件" || len(report.Todos) != 1 {
		t.Errorf("想定外の内容: %+v", report)
	}
	if report.Todos[0].Urgency != summary.UrgencyHigh {
		t.Errorf("urgency が %q", report.Todos[0].Urgency)
	}

	// 実行ログは Issue に紐づかない行として残り、参照 UI から辿れる。
	runs, err := e.st.ListRuns("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Phase != PhaseSummarize || runs[0].Result != "ok" {
		t.Fatalf("実行ログが期待どおりではありません: %+v", runs)
	}
	if sums[0].RunID != runs[0].ID {
		t.Errorf("サマリが実行ログに紐づいていません: %d != %d", sums[0].RunID, runs[0].ID)
	}

	// GitHub への書き込みは一切行わない。
	if len(fake.comments) != 0 || len(fake.statusRecord) != 0 {
		t.Errorf("GitHub を更新しています: comments=%v statuses=%v", fake.comments, fake.statusRecord)
	}
}

// JSON として読めない出力でも、生成物を捨てずに残す。
func TestSummarizeKeepsUnparsableOutput(t *testing.T) {
	fake := &fakeGitHub{}
	script := `#!/bin/sh
cat > /dev/null
echo "うまく答えられませんでした"
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()

	if err := e.runSummary(context.Background(), Job{Phase: PhaseSummarize}); err != nil {
		t.Fatalf("サマリ生成に失敗: %v", err)
	}
	sums, err := e.st.ListSummaries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("保存件数が %d", len(sums))
	}
	if sums[0].Payload != "" {
		t.Errorf("解釈できない出力が payload に入っています: %q", sums[0].Payload)
	}
	if !strings.Contains(sums[0].Raw, "うまく答えられませんでした") {
		t.Errorf("生の出力が残っていません: %q", sums[0].Raw)
	}
}

// JSON にはなっているが Report ではない出力を「対応不要」として保存しない。
// 通してしまうと UI が「対応が必要な TODO はありません」と出し、生成の失敗が
// 静かな誤報に化ける。
func TestSummarizeRejectsReportShapedNoise(t *testing.T) {
	script := `#!/bin/sh
cat > /dev/null
echo '状況をまとめられませんでした。'
echo '{"error":"rate limited"}'
`
	e, _, cleanup := setupTestEngine(t, &fakeGitHub{}, script)
	defer cleanup()
	seedSummaryItems(t, e)

	if err := e.runSummary(context.Background(), Job{Phase: PhaseSummarize}); err != nil {
		t.Fatalf("サマリ生成に失敗: %v", err)
	}
	sums, err := e.st.ListSummaries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("保存件数が %d", len(sums))
	}
	if sums[0].Payload != "" {
		t.Errorf("Report でない JSON が payload に入っています: %q", sums[0].Payload)
	}
	if !strings.Contains(sums[0].Raw, "rate limited") {
		t.Errorf("生の出力が残っていません: %q", sums[0].Raw)
	}
}

// エージェントが失敗しても、パイプラインの状態は動かさない。
func TestSummarizeFailureDoesNotAffectPipeline(t *testing.T) {
	fake := &fakeGitHub{}
	script := `#!/bin/sh
cat > /dev/null
exit 1
`
	e, _, cleanup := setupTestEngine(t, fake, script)
	defer cleanup()
	seedSummaryItems(t, e)

	if err := e.runSummary(context.Background(), Job{Phase: PhaseSummarize}); err == nil {
		t.Fatal("失敗が報告されていません")
	}
	if len(fake.statusRecord) != 0 {
		t.Errorf("Status を変更しています: %v", fake.statusRecord)
	}
	it, err := e.st.Get("owner/repo", 2)
	if err != nil || it == nil {
		t.Fatal(err)
	}
	if it.LastStatus != "⏸ Blocked" || it.RetryCount != 5 {
		t.Errorf("カードの状態が変わっています: %+v", it)
	}
	sums, err := e.st.ListSummaries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Errorf("失敗した生成が保存されています: %+v", sums)
	}
}

// プロンプトには未終端のカードだけが、人間の関与点から順に載る。
func TestSummaryContextOrdersHumanGatesFirst(t *testing.T) {
	e, _, cleanup := setupTestEngine(t, &fakeGitHub{}, "#!/bin/sh\n")
	defer cleanup()
	seedSummaryItems(t, e)

	pc, err := e.summaryContext()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range pc.Items {
		got = append(got, it.Status)
	}
	want := []string{"⏸ Blocked", "👀 In Review", "📥 Inbox"}
	if len(got) != len(want) {
		t.Fatalf("件数が %d（終端のカードが混ざっている可能性）: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("並び順が %v、期待は %v", got, want)
		}
	}
}

// 保持件数を超えた古いサマリは削除する。
func TestTrimSummaries(t *testing.T) {
	e, _, cleanup := setupTestEngine(t, &fakeGitHub{}, "#!/bin/sh\n")
	defer cleanup()

	for i := 0; i < 5; i++ {
		if _, err := e.st.AddSummary(&store.Summary{Payload: `{"headline":"x"}`}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.st.TrimSummaries(2); err != nil {
		t.Fatal(err)
	}
	sums, err := e.st.ListSummaries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("残った件数が %d", len(sums))
	}
	// 新しい順に返る。
	if sums[0].ID < sums[1].ID {
		t.Errorf("並び順が古い順になっています: %d, %d", sums[0].ID, sums[1].ID)
	}
}

// schedule 未設定なら次回予定を持たない（参照 UI がその旨を出せる）。
func TestSummaryScheduleDisabled(t *testing.T) {
	e, _, cleanup := setupTestEngine(t, &fakeGitHub{}, "#!/bin/sh\n")
	defer cleanup()

	spec, next := e.SummarySchedule()
	if spec != "" || !next.IsZero() {
		t.Errorf("無効なはずのスケジュールが %q / %s", spec, next)
	}
}

// スケジューラは待機に入る前に次回予定を公開する（UI が「次回 03:00」を出せる）。
func TestSummarySchedulePublishesNextTime(t *testing.T) {
	e, _, cleanup := setupTestEngine(t, &fakeGitHub{}, "#!/bin/sh\n")
	defer cleanup()

	sched, err := cron.Parse("0 9 * * *")
	if err != nil {
		t.Fatal(err)
	}
	e.summaryCron = sched

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.scheduleSummaries(ctx)

	waitFor(t, "次回予定の公開", func() bool {
		_, next := e.SummarySchedule()
		return !next.IsZero()
	})
	spec, next := e.SummarySchedule()
	if spec != "0 9 * * *" {
		t.Errorf("cron 式が %q", spec)
	}
	if !next.After(time.Now()) {
		t.Errorf("次回予定が過去になっています: %s", next)
	}
}
