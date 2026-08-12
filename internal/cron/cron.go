// Package cron は 5 フィールドの cron 式（分 時 日 月 曜日）を解釈し、
// 次回の実行時刻を求める。
//
// 外部ライブラリを入れていないのは、必要なのが定時起動の判定だけであり、
// 依存を 1 つ増やすと nix の vendorHash 更新まで巻き込むためである。
// 秒フィールド・`@daily` などの別名・`JAN` / `MON` のような名前指定は扱わない。
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule は解釈済みの cron 式。
type Schedule struct {
	spec   string
	minute [60]bool
	hour   [24]bool
	dom    [32]bool // 1..31
	month  [13]bool // 1..12
	dow    [7]bool  // 0..6（0 = 日曜）
	// domRestricted / dowRestricted は「日」「曜日」が * 以外で指定されたかどうか。
	// 両方が指定された場合、cron の慣習に従って **どちらかに一致すれば実行** とする。
	domRestricted bool
	dowRestricted bool
}

// String は元の cron 式を返す。
func (s *Schedule) String() string { return s.spec }

type field struct {
	name string
	min  int
	max  int
}

var fields = []field{
	{"分", 0, 59},
	{"時", 0, 23},
	{"日", 1, 31},
	{"月", 1, 12},
	{"曜日", 0, 7}, // 7 も日曜として受け付ける
}

// Parse は cron 式を解釈する。
//
// 各フィールドは `*` / `数値` / `a-b` / `*/n` / `a-b/n` と、それらのカンマ区切りを扱う。
func Parse(spec string) (*Schedule, error) {
	parts := strings.Fields(spec)
	if len(parts) != len(fields) {
		return nil, fmt.Errorf("cron 式は「分 時 日 月 曜日」の 5 フィールドで指定してください: %q", spec)
	}
	s := &Schedule{spec: strings.Join(parts, " ")}
	sets := make([][]int, len(fields))
	for i, f := range fields {
		v, err := parseField(parts[i], f)
		if err != nil {
			return nil, fmt.Errorf("cron 式 %q の%sフィールド: %w", spec, f.name, err)
		}
		sets[i] = v
	}
	for _, v := range sets[0] {
		s.minute[v] = true
	}
	for _, v := range sets[1] {
		s.hour[v] = true
	}
	for _, v := range sets[2] {
		s.dom[v] = true
	}
	for _, v := range sets[3] {
		s.month[v] = true
	}
	for _, v := range sets[4] {
		s.dow[v%7] = true // 7（日曜）を 0 に畳む
	}
	s.domRestricted = parts[2] != "*"
	s.dowRestricted = parts[4] != "*"
	return s, nil
}

func parseField(expr string, f field) ([]int, error) {
	var out []int
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("空の項目があります")
		}
		rangeExpr, stepExpr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepExpr)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("間隔 %q が不正です", stepExpr)
			}
			step = n
		}

		lo, hi := f.min, f.max
		if rangeExpr != "*" {
			a, b, isRange := strings.Cut(rangeExpr, "-")
			n, err := strconv.Atoi(strings.TrimSpace(a))
			if err != nil {
				return nil, fmt.Errorf("数値として読めません: %q", a)
			}
			lo, hi = n, n
			if isRange {
				m, err := strconv.Atoi(strings.TrimSpace(b))
				if err != nil {
					return nil, fmt.Errorf("数値として読めません: %q", b)
				}
				hi = m
			} else if hasStep {
				// `5/10` は「5 から上限まで 10 刻み」を意味する。
				hi = f.max
			}
		}
		if lo < f.min || hi > f.max || lo > hi {
			return nil, fmt.Errorf("%d-%d は範囲 %d-%d の外です", lo, hi, f.min, f.max)
		}
		for v := lo; v <= hi; v += step {
			out = append(out, v)
		}
	}
	return out, nil
}

// searchLimit は Next が探索を打ち切るまでの期間。
//
// 2/30 のように永久に来ない指定を無限ループにしないための保険である。
// うるう年を跨ぐため 5 年としている。
const searchLimit = 5 * 365 * 24 * time.Hour

// Next は t より後の最初の実行時刻を返す。
//
// t のタイムゾーンで解釈する。該当する時刻が見つからない場合はゼロ値を返す。
func (s *Schedule) Next(t time.Time) time.Time {
	// 秒以下を落として次の分から探す（同じ分に二度当たらないようにする）。
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(searchLimit)
	for t.Before(limit) {
		if !s.matchDay(t) {
			t = startOfDay(t).AddDate(0, 0, 1)
			continue
		}
		if !s.hour[t.Hour()] {
			t = startOfHour(t).Add(time.Hour)
			continue
		}
		if !s.minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// matchDay は月・日・曜日の一致を判定する。
//
// 日と曜日の両方が指定されている場合は、cron の慣習どおり OR で判定する
// （例: `0 9 1 * 1` は「毎月 1 日」と「毎週月曜」の両方で実行される）。
func (s *Schedule) matchDay(t time.Time) bool {
	if !s.month[int(t.Month())] {
		return false
	}
	dom := s.dom[t.Day()]
	dow := s.dow[int(t.Weekday())]
	if s.domRestricted && s.dowRestricted {
		return dom || dow
	}
	return dom && dow
}

// startOfDay / startOfHour は時刻を切り下げる。
// Truncate は絶対時刻を基準にするため、UTC からのオフセットが 1 時間未満の
// タイムゾーンでは日・時の境界とずれる。ここでは暦上の境界が要るので time.Date で作る。
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func startOfHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}
