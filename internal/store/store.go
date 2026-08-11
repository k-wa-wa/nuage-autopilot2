// Package store は SQLite による状態キャッシュを提供する。
//
// この DB は GitHub の権威あるコピーではなく、差分検出のためのキャッシュ・
// カーソル・リトライ回数の置き場である。削除しても Init でシードし直せば復旧できる。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store は SQLite への接続を保持する。
type Store struct {
	db *sql.DB
}

// Item は 1 件の Issue に対するローカル状態。
type Item struct {
	Repo          string
	IssueNumber   int
	ProjectItemID string
	LastStatus    string
	LastCommentID int64
	PRNumber      int
	Branch        string
	RetryCount    int
	LeaseUntil    time.Time
	VerifySince   time.Time
	Terminal      bool
	UpdatedAt     time.Time
	// ReconciledAt は pollProjectOnce が Project API でこの Item を最後に確認した時刻。
	// ゼロ値は「まだ一度も確認されていない（機能追加前のレコード）」を表す。
	ReconciledAt time.Time
}

// Run は 1 回のエージェント実行の記録。
//
// EndedAt がゼロ値なら実行中か、ワーカーが異常終了して取り残されたかのどちらか。
// 参照側はこの 2 つを区別できないため、実行中かどうかは常駐プロセスの
// メモリ上の情報で判断する。
type Run struct {
	ID          int64
	Repo        string
	IssueNumber int
	Phase       string
	StartedAt   time.Time
	EndedAt     time.Time
	Result      string
	LogPath     string
}

const schema = `
CREATE TABLE IF NOT EXISTS items (
  repo             TEXT    NOT NULL,
  issue_number     INTEGER NOT NULL,
  project_item_id  TEXT    NOT NULL DEFAULT '',
  last_status      TEXT    NOT NULL DEFAULT '',
  last_comment_id  INTEGER NOT NULL DEFAULT 0,
  pr_number        INTEGER NOT NULL DEFAULT 0,
  branch           TEXT    NOT NULL DEFAULT '',
  retry_count      INTEGER NOT NULL DEFAULT 0,
  lease_until      INTEGER NOT NULL DEFAULT 0,
  verify_since     INTEGER NOT NULL DEFAULT 0,
  terminal         INTEGER NOT NULL DEFAULT 0,
  updated_at       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (repo, issue_number)
);

CREATE TABLE IF NOT EXISTS cursors (
  name  TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  repo         TEXT    NOT NULL,
  issue_number INTEGER NOT NULL,
  phase        TEXT    NOT NULL,
  started_at   INTEGER NOT NULL,
  ended_at     INTEGER NOT NULL DEFAULT 0,
  result       TEXT    NOT NULL DEFAULT '',
  log_path     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_runs_item ON runs(repo, issue_number);
CREATE INDEX IF NOT EXISTS idx_items_status ON items(last_status);
`

// Open は DB を開き、スキーマを適用する。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite は並行書き込みに弱いため、接続を 1 本に制限する。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("スキーマ適用に失敗: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate は CREATE TABLE IF NOT EXISTS では追随できない列追加を補う。
//
// 既存の DB は runs に log_path を持たないが、この DB はキャッシュなので
// 作り直しても復旧できる。とはいえ実行履歴まで捨てる必要はないため、
// 不足している列だけを足す。
func migrate(db *sql.DB) error {
	has, err := hasColumn(db, "runs", "log_path")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN log_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("runs.log_path の追加に失敗: %w", err)
		}
	}
	// reconciled_at は pollProjectOnce が Project API で確認した最終時刻。
	// 機能追加前の既存レコードは 0（ゼロ値 = 未確認）として扱う。
	has, err = hasColumn(db, "items", "reconciled_at")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE items ADD COLUMN reconciled_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("items.reconciled_at の追加に失敗: %w", err)
		}
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

// Close は DB を閉じる。
func (s *Store) Close() error { return s.db.Close() }

// IsEmpty は items が 1 件も無いかを返す。コールドスタート判定に使う。
func (s *Store) IsEmpty() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// Get は 1 件取得する。存在しない場合は (nil, nil)。
func (s *Store) Get(repo string, issue int) (*Item, error) {
	row := s.db.QueryRow(`
		SELECT repo, issue_number, project_item_id, last_status, last_comment_id,
		       pr_number, branch, retry_count, lease_until, verify_since, terminal, updated_at,
		       reconciled_at
		FROM items WHERE repo = ? AND issue_number = ?`, repo, issue)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return it, err
}

// List は全件返す。
func (s *Store) List() ([]*Item, error) {
	rows, err := s.db.Query(`
		SELECT repo, issue_number, project_item_id, last_status, last_comment_id,
		       pr_number, branch, retry_count, lease_until, verify_since, terminal, updated_at,
		       reconciled_at
		FROM items ORDER BY repo, issue_number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListByStatus は指定 Status かつ未終端の item を返す。
func (s *Store) ListByStatus(status string) ([]*Item, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []*Item
	for _, it := range all {
		if it.LastStatus == status && !it.Terminal {
			out = append(out, it)
		}
	}
	return out, nil
}

// Upsert は item を丸ごと書き込む。
func (s *Store) Upsert(it *Item) error {
	it.UpdatedAt = time.Now()
	_, err := s.db.Exec(`
		INSERT INTO items (repo, issue_number, project_item_id, last_status, last_comment_id,
		                   pr_number, branch, retry_count, lease_until, verify_since, terminal, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo, issue_number) DO UPDATE SET
			project_item_id = excluded.project_item_id,
			last_status     = excluded.last_status,
			last_comment_id = excluded.last_comment_id,
			pr_number       = excluded.pr_number,
			branch          = excluded.branch,
			retry_count     = excluded.retry_count,
			lease_until     = excluded.lease_until,
			verify_since    = excluded.verify_since,
			terminal        = excluded.terminal,
			updated_at      = excluded.updated_at`,
		it.Repo, it.IssueNumber, it.ProjectItemID, it.LastStatus, it.LastCommentID,
		it.PRNumber, it.Branch, it.RetryCount, unixOrZero(it.LeaseUntil), unixOrZero(it.VerifySince),
		boolToInt(it.Terminal), it.UpdatedAt.Unix())
	return err
}

// TouchReconciled は items.reconciled_at を現在時刻で更新する。
//
// pollProjectOnce が Project API からこの Item を確認できた際に呼び出す。
// Upsert とは分離しているのは、確認時刻をイベントハンドラからではなく
// ポーリングループ側でのみ更新したいためである。
func (s *Store) TouchReconciled(repo string, issue int) error {
	_, err := s.db.Exec(`UPDATE items SET reconciled_at = ? WHERE repo = ? AND issue_number = ?`,
		time.Now().Unix(), repo, issue)
	return err
}

// Cursor はカーソル値を返す。未設定なら空文字。
func (s *Store) Cursor(name string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM cursors WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetCursor はカーソル値を保存する。
func (s *Store) SetCursor(name, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO cursors (name, value) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value`, name, value)
	return err
}

// StartRun は実行ログを開始し、その ID を返す。
func (s *Store) StartRun(repo string, issue int, phase string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO runs (repo, issue_number, phase, started_at) VALUES (?, ?, ?, ?)`,
		repo, issue, phase, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// EndRun は実行ログを終了させる。
func (s *Store) EndRun(id int64, result string) error {
	_, err := s.db.Exec(`UPDATE runs SET ended_at = ?, result = ? WHERE id = ?`,
		time.Now().Unix(), result, id)
	return err
}

// SetRunLog は実行ログにエージェントの出力ファイルのパスを記録する。
//
// パスはエージェントプロセスの起動時に初めて確定するため、StartRun では埋められない。
func (s *Store) SetRunLog(id int64, path string) error {
	_, err := s.db.Exec(`UPDATE runs SET log_path = ? WHERE id = ?`, path, id)
	return err
}

const runColumns = `id, repo, issue_number, phase, started_at, ended_at, result, log_path`

// GetRun は 1 件取得する。存在しない場合は (nil, nil)。
func (s *Store) GetRun(id int64) (*Run, error) {
	row := s.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id = ?`, id)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// ListRuns は 1 件の Issue の実行履歴を新しい順に返す。
func (s *Store) ListRuns(repo string, issue int, limit int) ([]*Run, error) {
	return s.queryRuns(`SELECT `+runColumns+` FROM runs
		WHERE repo = ? AND issue_number = ?
		ORDER BY started_at DESC, id DESC LIMIT ?`, repo, issue, limit)
}

// LatestRuns は Issue ごとの最新の実行を 1 件ずつ返す。
//
// 一覧画面で「この Issue で最後に何が起きたか」を出すために使う。
// 直近 N 件を取って絞る方式だと、しばらく動いていない Issue が窓から
// 溢れて欠落するため、Issue ごとに最大 id を引く。
func (s *Store) LatestRuns() ([]*Run, error) {
	return s.queryRuns(`SELECT ` + runColumns + ` FROM runs
		WHERE id IN (SELECT MAX(id) FROM runs GROUP BY repo, issue_number)`)
}

func (s *Store) queryRuns(query string, args ...any) ([]*Run, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRun(r scanner) (*Run, error) {
	var run Run
	var started, ended int64
	if err := r.Scan(&run.ID, &run.Repo, &run.IssueNumber, &run.Phase,
		&started, &ended, &run.Result, &run.LogPath); err != nil {
		return nil, err
	}
	run.StartedAt = timeOrZero(started)
	run.EndedAt = timeOrZero(ended)
	return &run, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanItem(r scanner) (*Item, error) {
	var it Item
	var lease, verify, updated, reconciled int64
	var terminal int
	err := r.Scan(&it.Repo, &it.IssueNumber, &it.ProjectItemID, &it.LastStatus, &it.LastCommentID,
		&it.PRNumber, &it.Branch, &it.RetryCount, &lease, &verify, &terminal, &updated,
		&reconciled)
	if err != nil {
		return nil, err
	}
	it.LeaseUntil = timeOrZero(lease)
	it.VerifySince = timeOrZero(verify)
	it.Terminal = terminal != 0
	it.UpdatedAt = timeOrZero(updated)
	it.ReconciledAt = timeOrZero(reconciled)
	return &it, nil
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
