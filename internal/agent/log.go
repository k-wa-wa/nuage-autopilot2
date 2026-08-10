package agent

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// ログファイルの区切り行。Run が書き、参照側が読む。
//
// 書式を知っているのはこのパッケージだけにしたいので、切り出しもここに置く。
const (
	LogPromptSep = "--- prompt ---"
	LogOutputSep = "--- output ---"
)

// LogView はログファイルを表示向けに読み出した結果。
type LogView struct {
	// Header は 1 行目の "=== phase=... ===" の行。
	Header string
	// Prompt はエージェントに渡したプロンプト全文。
	Prompt string
	// PromptTruncated は Prompt が上限で打ち切られたかどうか。
	PromptTruncated bool
	// Output はエージェントの出力。上限を超える場合は末尾のみ。
	Output string
	// OutputTruncated は Output が末尾だけの切り出しかどうか。
	OutputTruncated bool
	// Size はファイル全体のバイト数。追記取得の起点に使う。
	Size int64
}

// ログ読み出しの既定の上限。
//
// implement フェーズのログは数十 MB になり得るため、丸ごとは読まない。
const (
	// DefaultPromptMax はヘッダとプロンプトの読み取り上限。
	// プロンプトには Issue 本文とコメントが入るが、1 MiB を超えることはまず無い。
	DefaultPromptMax int64 = 1 << 20
	// DefaultOutputMax は出力の読み取り上限。既定では末尾だけを返す。
	DefaultOutputMax int64 = 256 << 10
)

// ReadLog はログファイルを読み、ヘッダ・プロンプト・出力に分けて返す。
//
// promptMax / outputMax が 0 以下なら既定値を使う。まだ出力が始まっていない
// 実行中のログでも、ヘッダとプロンプトだけを返せる。
func ReadLog(path string, promptMax, outputMax int64) (*LogView, error) {
	if promptMax <= 0 {
		promptMax = DefaultPromptMax
	}
	if outputMax <= 0 {
		outputMax = DefaultOutputMax
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	v := &LogView{Size: fi.Size()}

	head := make([]byte, min(promptMax, fi.Size()))
	if _, err := io.ReadFull(f, head); err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	// 出力の開始位置を求める。区切りが見つからなければ、まだ出力段階に
	// 入っていないか、上限内に収まらなかったかのどちらか。
	//
	// 最初の出現を採る。プロンプトには Issue 本文とコメントがそのまま入るため、
	// そこに区切りと同じ行があると出力の開始位置を早く見誤るが、表示が崩れるだけで
	// 実行には影響しない。書式にオフセットを持たせてまで厳密にはしない。
	outputStart := int64(-1)
	if i := strings.Index(string(head), "\n"+LogOutputSep+"\n"); i >= 0 {
		outputStart = int64(i) + int64(len(LogOutputSep)) + 2
	}

	headStr := string(head)
	if outputStart >= 0 {
		headStr = headStr[:outputStart]
	} else if int64(len(head)) < fi.Size() {
		v.PromptTruncated = true
	}
	v.Header, v.Prompt = splitPrompt(headStr)

	if outputStart < 0 {
		return v, nil
	}
	if outputStart >= fi.Size() {
		return v, nil // 出力はまだ 1 バイトも無い。
	}

	from := outputStart
	if fi.Size()-outputStart > outputMax {
		from = fi.Size() - outputMax
		v.OutputTruncated = true
	}
	out := make([]byte, fi.Size()-from)
	if _, err := f.ReadAt(out, from); err != nil && err != io.EOF {
		return nil, err
	}
	if v.OutputTruncated {
		out = trimPartialRune(out)
	}
	v.Output = string(out)
	return v, nil
}

// LogChunk はログの追記分を読み出した結果。
type LogChunk struct {
	Data string
	// Next は次回に渡すべき offset。
	Next int64
	// Size はファイル全体のバイト数。
	Size int64
	// Skipped は上限を超えたため途中を読み飛ばしたかどうか。
	Skipped bool
}

// ReadLogFrom は offset 以降の追記分を読む。実行中のログの追従に使う。
//
// offset がファイル長を超えている場合（ログが作り直された等）は先頭から読み直す。
func ReadLogFrom(path string, offset, max int64) (*LogChunk, error) {
	if max <= 0 {
		max = DefaultOutputMax
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	c := &LogChunk{Size: fi.Size(), Next: fi.Size()}
	if offset < 0 || offset > c.Size {
		offset = 0
	}
	if offset == c.Size {
		return c, nil
	}

	n := c.Size - offset
	if n > max {
		offset = c.Size - max
		n = max
		c.Skipped = true
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, err
	}
	if c.Skipped {
		buf = trimPartialRune(buf)
	}
	c.Data = string(buf)
	return c, nil
}

// splitPrompt はヘッダ部分をヘッダ行とプロンプト本文に分ける。
func splitPrompt(s string) (header, prompt string) {
	sep := "\n" + LogPromptSep + "\n"
	i := strings.Index(s, sep)
	if i < 0 {
		return strings.TrimRight(s, "\n"), ""
	}
	header = strings.TrimRight(s[:i], "\n")
	prompt = s[i+len(sep):]
	// 出力の区切り行の直前に入る改行を落とす。
	prompt = strings.TrimSuffix(prompt, "\n"+LogOutputSep+"\n")
	return header, strings.TrimRight(prompt, "\n")
}

// trimPartialRune は先頭に残った UTF-8 の断片を捨てる。
//
// 末尾から一定バイト数を切り出すと多バイト文字の途中から始まることがあり、
// そのまま返すと文字化けする。
func trimPartialRune(b []byte) []byte {
	for i := 0; i < len(b) && i < utf8.UTFMax; i++ {
		if r, _ := utf8.DecodeRune(b[i:]); r != utf8.RuneError {
			return b[i:]
		}
	}
	return b
}
