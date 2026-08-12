package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, spec string) *Schedule {
	t.Helper()
	s, err := Parse(spec)
	if err != nil {
		t.Fatalf("Parse(%q) が失敗しました: %v", spec, err)
	}
	return s
}

func TestNext(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 30, 15, 0, time.UTC)
	tests := []struct {
		spec string
		want time.Time
	}{
		// 毎時 0 分。
		{"0 * * * *", time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)},
		// 毎日 9 時（今日は過ぎているので翌日）。
		{"0 9 * * *", time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)},
		// 15 分刻み。
		{"*/15 * * * *", time.Date(2026, 8, 12, 10, 45, 0, 0, time.UTC)},
		// 複数指定のうち直近のもの。
		{"0 9,13,18 * * *", time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)},
		// 平日のみ（2026-08-12 は水曜、次の月曜は 8/17）。
		{"0 8 * * 1", time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)},
		// 月をまたぐ。
		{"30 6 1 * *", time.Date(2026, 9, 1, 6, 30, 0, 0, time.UTC)},
		// 曜日の 7 は日曜として扱う（8/16 が日曜）。
		{"0 0 * * 7", time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)},
		// 範囲と刻みの組み合わせ。
		{"0 9-18/3 * * *", time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		got := mustParse(t, tt.spec).Next(base)
		if !got.Equal(tt.want) {
			t.Errorf("%q の次回実行が %s、期待は %s", tt.spec, got, tt.want)
		}
	}
}

// 日と曜日の両方が指定された場合、cron の慣習では OR になる。
func TestNextDayOfMonthOrWeekday(t *testing.T) {
	s := mustParse(t, "0 0 15 * 1") // 毎月 15 日、または毎週月曜
	base := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	// 8/17 が月曜、8/15 は土曜。日付側が先に来る。
	want := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	if got := s.Next(base); !got.Equal(want) {
		t.Errorf("次回実行が %s、期待は %s", got, want)
	}
}

// ちょうど一致する時刻に呼ばれても、同じ分を返して二重起動させない。
func TestNextSkipsCurrentMinute(t *testing.T) {
	s := mustParse(t, "0 9 * * *")
	base := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	if got := s.Next(base); !got.Equal(want) {
		t.Errorf("次回実行が %s、期待は %s", got, want)
	}
}

// 到達し得ない指定は無限ループにせずゼロ値を返す。
func TestNextUnreachable(t *testing.T) {
	s := mustParse(t, "0 0 30 2 *") // 2 月 30 日
	if got := s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !got.IsZero() {
		t.Errorf("到達しない指定でゼロ値以外が返りました: %s", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, spec := range []string{
		"",
		"0 9 * *",      // フィールド不足
		"0 9 * * * *",  // フィールド過多
		"60 * * * *",   // 範囲外
		"* 24 * * *",   // 範囲外
		"0 0 0 * *",    // 日は 1 から
		"*/0 * * * *",  // 刻みが 0
		"a * * * *",    // 数値でない
		"0 18-9 * * *", // 逆順の範囲
		"0 0 * * MON",  // 名前指定は非対応
		"0 0 1,, * *",  // 空の項目
	} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) はエラーになるべきです", spec)
		}
	}
}

// タイムゾーンは与えた時刻のものを使う（サーバのローカル時刻で書ける）。
func TestNextKeepsLocation(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	s := mustParse(t, "0 9 * * *")
	got := s.Next(time.Date(2026, 8, 12, 10, 0, 0, 0, jst))
	want := time.Date(2026, 8, 13, 9, 0, 0, 0, jst)
	if !got.Equal(want) {
		t.Errorf("次回実行が %s、期待は %s", got, want)
	}
	if got.Location() != jst {
		t.Errorf("タイムゾーンが %s になっています", got.Location())
	}
}
