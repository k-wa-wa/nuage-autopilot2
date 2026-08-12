// Package summary は「人間がやるべきこと」をまとめたレポートの型と、
// エージェント出力からの取り出しを提供する。
//
// エージェントに Markdown ではなく JSON を書かせているのは、UI 側で
// 優先度の並べ替えや Issue へのリンクを機械的に扱えるようにするためである。
// 表示の都合を人間が読む文章に混ぜると、レイアウトの変更のたびに
// プロンプトを直すことになる。
package summary

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 緊急度。エージェントにはこの 3 値のいずれかを出力させる。
const (
	UrgencyHigh   = "high"
	UrgencyMedium = "medium"
	UrgencyLow    = "low"
)

// Todo は人間が対応すべき 1 件。
type Todo struct {
	// Repo は "owner/name"。パイプラインに紐づかない指摘では空でもよい。
	Repo  string `json:"repo"`
	Issue int    `json:"issue"`
	// Title は Issue のタイトルではなく、やるべきことの見出し。
	Title string `json:"title"`
	// Status は対象カードの現在のレーン。
	Status  string `json:"status"`
	Urgency string `json:"urgency"`
	// Why はなぜ人間の関与が要るのか。
	Why string `json:"why"`
	// Action は推奨する対応。
	Action string `json:"action"`
}

// Report は 1 回の生成結果。
type Report struct {
	// Headline は全体を 1 行で言い表したもの。
	Headline string `json:"headline"`
	Todos    []Todo `json:"todos"`
	// Notes は TODO には至らないが伝えておきたいこと。
	Notes string `json:"notes"`
}

// maxTodos は 1 回のレポートに載せる TODO の上限。
//
// 「簡潔にまとめる」ことが目的なので、エージェントが列挙し過ぎた場合は切り捨てる。
const maxTodos = 20

// Parse はエージェントの出力からレポートを取り出す。
//
// CLI によっては JSON の前後に説明文が付くため、```json フェンス、
// 次いで最も外側の {...} の順に探す。
func Parse(out string) (*Report, error) {
	raw, err := extractJSON(out)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("サマリの JSON を解釈できません: %w", err)
	}
	r.normalize()
	return &r, nil
}

// normalize は表示に耐える形へ整える。
func (r *Report) normalize() {
	r.Headline = strings.TrimSpace(r.Headline)
	r.Notes = strings.TrimSpace(r.Notes)
	todos := make([]Todo, 0, len(r.Todos))
	for _, t := range r.Todos {
		t.Repo = strings.TrimSpace(t.Repo)
		t.Title = strings.TrimSpace(t.Title)
		t.Status = strings.TrimSpace(t.Status)
		t.Why = strings.TrimSpace(t.Why)
		t.Action = strings.TrimSpace(t.Action)
		switch strings.ToLower(strings.TrimSpace(t.Urgency)) {
		case UrgencyHigh:
			t.Urgency = UrgencyHigh
		case UrgencyLow:
			t.Urgency = UrgencyLow
		default:
			// 未知の値は中位に寄せる。並べ替えの基準を壊さないため。
			t.Urgency = UrgencyMedium
		}
		if t.Title == "" && t.Action == "" {
			continue
		}
		todos = append(todos, t)
		if len(todos) == maxTodos {
			break
		}
	}
	r.Todos = todos
}

// extractJSON は出力から JSON オブジェクトの文字列を切り出す。
func extractJSON(out string) (string, error) {
	if fenced, ok := lastFencedJSON(out); ok {
		return fenced, nil
	}
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("出力に JSON が含まれていません")
	}
	return out[start : end+1], nil
}

// lastFencedJSON は最後の ```json フェンスの中身を返す。
//
// 最後を採るのは、プロンプトの引用として書式例を復唱してから本番の JSON を
// 出力する CLI があるためで、マーカーの扱い（最後の出力を採用する）と揃える。
func lastFencedJSON(out string) (string, bool) {
	const fence = "```"
	rest := out
	found := ""
	for {
		i := strings.Index(rest, fence+"json")
		if i < 0 {
			break
		}
		body := rest[i+len(fence)+len("json"):]
		j := strings.Index(body, fence)
		if j < 0 {
			break
		}
		found = strings.TrimSpace(body[:j])
		rest = body[j+len(fence):]
	}
	return found, found != ""
}
