// Package web はローカル状態を参照するための HTTP サーバを提供する。
//
// 参照専用であり、状態を変える経路は一切持たない（GET と HEAD 以外は拒否する）。
// 表示に使うのは SQLite のキャッシュとエージェントのログファイルだけで、
// GitHub API は呼ばない。オフラインでも常駐プロセスの中身が見える。
//
// engine への依存を持たないよう、必要なデータは Source インターフェース越しに受け取る。
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/store"
)

//go:embed assets
var assetsFS embed.FS

// Active は agent-worker が今処理しているジョブ。
//
// engine 側の型をそのまま使うと import が循環するため、受け渡し用にここで定義する。
type Active struct {
	RunID     int64     `json:"run_id"`
	Phase     string    `json:"phase"`
	Repo      string    `json:"repo"`
	Issue     int       `json:"issue"`
	StartedAt time.Time `json:"started_at"`
	// AgentStartedAt はワークツリー準備とプロンプト生成を終え、実際に
	// エージェントプロセスを起動した時刻。起動前はゼロ値。
	AgentStartedAt time.Time `json:"agent_started_at"`
	// LogPath はプロセス起動後に埋まる。ログが無効な場合は空のまま。
	LogPath string `json:"-"`
}

// AgentInfo は 1 つの用途に割り当てられたエージェント CLI の設定。
type AgentInfo struct {
	Use     string `json:"use"`
	Command string `json:"command"`
	Model   string `json:"model"`
	Timeout string `json:"timeout"`
}

// Meta は起動中はほぼ変化しない情報。画面の見出しとレーンの並びに使う。
type Meta struct {
	Login         string      `json:"login"`
	ProjectOwner  string      `json:"project_owner"`
	ProjectNumber int         `json:"project_number"`
	Repos         []string    `json:"repos"`
	Statuses      []string    `json:"statuses"`
	Agents        []AgentInfo `json:"agents"`
}

// Source は表示に必要なデータを供給する。engine が実装する。
type Source interface {
	Meta() Meta
	Items() ([]*store.Item, error)
	LatestRuns() ([]*store.Run, error)
	IssueRuns(repo string, issue int, limit int) ([]*store.Run, error)
	GetRun(id int64) (*store.Run, error)
	Active() *Active
	QueueDepth() int
	// LogDir はログファイルの置き場。この配下以外は読み出さない。
	LogDir() string
}

// Server は参照専用の HTTP サーバ。
type Server struct {
	src Source
	log *slog.Logger
	mux *http.ServeMux
}

// New は Server を組み立てる。
func New(src Source, log *slog.Logger) *Server {
	s := &Server{src: src, log: log, mux: http.NewServeMux()}

	static, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic("web: 埋め込み資産を開けません: " + err.Error())
	}
	s.mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(static))))
	s.mux.HandleFunc("/api/state", s.jsonHandler(s.handleState))
	s.mux.HandleFunc("/api/item", s.jsonHandler(s.handleItem))
	s.mux.HandleFunc("/api/run", s.jsonHandler(s.handleRun))
	s.mux.HandleFunc("/api/run/log", s.jsonHandler(s.handleRunLog))
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// 画面はハッシュルーティングなので、それ以外はすべて index を返す。
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := assetsFS.ReadFile("assets/index.html")
		if err != nil {
			http.Error(w, "index を読めません", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	return s
}

// ServeHTTP は参照専用であることを強制したうえでルーティングする。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "この UI は参照専用である", http.StatusMethodNotAllowed)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// Serve は addr で待ち受け、ctx のキャンセルで停止する。
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web の待ち受けに失敗: %w", err)
	}
	s.log.Info("参照 UI を起動しました", "addr", "http://"+ln.Addr().String())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// jsonHandler はハンドラの戻り値を JSON にして返す共通処理。
func (s *Server) jsonHandler(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// 状態は刻々と変わるため、中間キャッシュに残されると困る。
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			var he httpError
			code := http.StatusInternalServerError
			if errors.As(err, &he) {
				code = he.code
			} else {
				s.log.Error("参照 UI のリクエスト処理に失敗", "path", r.URL.Path, "err", err)
			}
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if err := json.NewEncoder(w).Encode(v); err != nil {
			s.log.Error("参照 UI の応答書き込みに失敗", "path", r.URL.Path, "err", err)
		}
	}
}

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return httpError{code: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

func notFound(format string, a ...any) error {
	return httpError{code: http.StatusNotFound, msg: fmt.Sprintf(format, a...)}
}

// itemView は items の 1 行に、直近の実行と実行中フラグを添えたもの。
type itemView struct {
	Repo         string     `json:"repo"`
	Issue        int        `json:"issue"`
	Status       string     `json:"status"`
	PRNumber     int        `json:"pr_number"`
	Branch       string     `json:"branch"`
	RetryCount   int        `json:"retry_count"`
	LeaseUntil   *time.Time `json:"lease_until"`
	VerifySince  *time.Time `json:"verify_since"`
	Terminal     bool       `json:"terminal"`
	UpdatedAt    *time.Time `json:"updated_at"`
	ReconciledAt *time.Time `json:"reconciled_at"`
	IssueURL     string     `json:"issue_url"`
	PRURL        string     `json:"pr_url"`
	LastRun      *runView   `json:"last_run"`
	Running      bool       `json:"running"`
}

// runView は runs の 1 行。ログの有無まで含めて返す。
type runView struct {
	ID        int64      `json:"id"`
	Repo      string     `json:"repo"`
	Issue     int        `json:"issue"`
	Phase     string     `json:"phase"`
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Result    string     `json:"result"`
	HasLog    bool       `json:"has_log"`
	// Running は常駐プロセスが今まさに実行しているかどうか。
	//
	// ended_at が空でも、前回の異常終了で取り残された行の可能性があるため、
	// メモリ上の実行中ジョブと照合した結果をここに入れる。
	Running bool `json:"running"`
}

type stateResponse struct {
	GeneratedAt  time.Time  `json:"generated_at"`
	Meta         Meta       `json:"meta"`
	QueueDepth   int        `json:"queue_depth"`
	Active       *Active    `json:"active"`
	ActiveHasLog bool       `json:"active_has_log"`
	Items        []itemView `json:"items"`
}

func (s *Server) handleState(*http.Request) (any, error) { return s.state() }

func (s *Server) state() (stateResponse, error) {
	items, err := s.src.Items()
	if err != nil {
		return stateResponse{}, err
	}
	latest, err := s.src.LatestRuns()
	if err != nil {
		return stateResponse{}, err
	}
	active := s.src.Active()

	byItem := make(map[string]*store.Run, len(latest))
	for _, run := range latest {
		byItem[itemKey(run.Repo, run.IssueNumber)] = run
	}

	views := make([]itemView, 0, len(items))
	for _, it := range items {
		running := active != nil && active.Repo == it.Repo && active.Issue == it.IssueNumber
		v := itemView{
			Repo:         it.Repo,
			Issue:        it.IssueNumber,
			Status:       it.LastStatus,
			PRNumber:     it.PRNumber,
			Branch:       it.Branch,
			RetryCount:   it.RetryCount,
			LeaseUntil:   timePtr(it.LeaseUntil),
			VerifySince:  timePtr(it.VerifySince),
			Terminal:     it.Terminal,
			UpdatedAt:    timePtr(it.UpdatedAt),
			ReconciledAt: timePtr(it.ReconciledAt),
			IssueURL:     fmt.Sprintf("https://github.com/%s/issues/%d", it.Repo, it.IssueNumber),
			Running:      running,
		}
		if it.PRNumber != 0 {
			v.PRURL = fmt.Sprintf("https://github.com/%s/pull/%d", it.Repo, it.PRNumber)
		}
		if run := byItem[itemKey(it.Repo, it.IssueNumber)]; run != nil {
			v.LastRun = s.toRunView(run, active)
		}
		views = append(views, v)
	}

	return stateResponse{
		GeneratedAt:  time.Now(),
		Meta:         s.src.Meta(),
		QueueDepth:   s.src.QueueDepth(),
		Active:       active,
		ActiveHasLog: active != nil && active.LogPath != "",
		Items:        views,
	}, nil
}

type itemResponse struct {
	Item *itemView  `json:"item"`
	Runs []*runView `json:"runs"`
}

// itemRunLimit は Issue 詳細で遡る実行履歴の件数。
//
// runs は消さない方針なので、表示側で頭打ちにする。
const itemRunLimit = 100

func (s *Server) handleItem(r *http.Request) (any, error) {
	repo := r.URL.Query().Get("repo")
	issue, err := strconv.Atoi(r.URL.Query().Get("issue"))
	if repo == "" || err != nil {
		return nil, badRequest("repo と issue を指定してください")
	}

	state, err := s.state()
	if err != nil {
		return nil, err
	}
	var found *itemView
	for i := range state.Items {
		if state.Items[i].Repo == repo && state.Items[i].Issue == issue {
			found = &state.Items[i]
			break
		}
	}
	if found == nil {
		return nil, notFound("%s#%d はローカルに記録がありません", repo, issue)
	}

	runs, err := s.src.IssueRuns(repo, issue, itemRunLimit)
	if err != nil {
		return nil, err
	}
	active := s.src.Active()
	views := make([]*runView, 0, len(runs))
	for _, run := range runs {
		views = append(views, s.toRunView(run, active))
	}
	return itemResponse{Item: found, Runs: views}, nil
}

type runResponse struct {
	Run *runView `json:"run"`
	Log *logView `json:"log"`
	// LogError はログを読めなかった理由。ファイルが消えている場合など。
	LogError string `json:"log_error"`
}

type logView struct {
	Header          string `json:"header"`
	Prompt          string `json:"prompt"`
	PromptTruncated bool   `json:"prompt_truncated"`
	Output          string `json:"output"`
	OutputTruncated bool   `json:"output_truncated"`
	Size            int64  `json:"size"`
}

func (s *Server) handleRun(r *http.Request) (any, error) {
	run, err := s.lookupRun(r)
	if err != nil {
		return nil, err
	}
	resp := runResponse{Run: s.toRunView(run, s.src.Active())}
	path, err := s.logPath(run)
	if err != nil {
		resp.LogError = err.Error()
		return resp, nil
	}
	if path == "" {
		return resp, nil
	}
	v, err := agent.ReadLog(path, 0, 0)
	if err != nil {
		resp.LogError = fmt.Sprintf("ログを読めません: %v", err)
		return resp, nil
	}
	resp.Log = &logView{
		Header:          v.Header,
		Prompt:          v.Prompt,
		PromptTruncated: v.PromptTruncated,
		Output:          v.Output,
		OutputTruncated: v.OutputTruncated,
		Size:            v.Size,
	}
	return resp, nil
}

type logChunkResponse struct {
	Data    string `json:"data"`
	Next    int64  `json:"next"`
	Size    int64  `json:"size"`
	Skipped bool   `json:"skipped"`
	// Running は追従を続けるべきかを画面に伝える。
	//
	// これが無いと、実行中かどうかを確かめるためだけに毎回 /api/run を
	// 引き直すことになり、巨大なプロンプトと出力を数秒おきに転送してしまう。
	Running bool `json:"running"`
}

func (s *Server) handleRunLog(r *http.Request) (any, error) {
	run, err := s.lookupRun(r)
	if err != nil {
		return nil, err
	}
	path, err := s.logPath(run)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, notFound("この実行にはログがありません")
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	c, err := agent.ReadLogFrom(path, offset, 0)
	if err != nil {
		return nil, err
	}
	// 実行中かどうかは追記を読んだ後に判定する。先に見てしまうと、
	// 判定と読み出しの間に終了した場合に最後の追記を取りこぼす。
	active := s.src.Active()
	return logChunkResponse{
		Data: c.Data, Next: c.Next, Size: c.Size, Skipped: c.Skipped,
		Running: active != nil && active.RunID == run.ID,
	}, nil
}

func (s *Server) lookupRun(r *http.Request) (*store.Run, error) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		return nil, badRequest("id を指定してください")
	}
	run, err := s.src.GetRun(id)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound("実行 %d は見つかりません", id)
	}
	return run, nil
}

// logPath は run に対応するログファイルのパスを検証して返す。
//
// パスはクライアントからではなく DB から取るが、DB が壊れていても
// ログディレクトリの外を読ませないよう、ここでも確認する。
func (s *Server) logPath(run *store.Run) (string, error) {
	path := run.LogPath
	// 実行中の run は、プロセス起動時に DB へ書き戻すまでの間だけパスが空になる。
	if path == "" {
		if a := s.src.Active(); a != nil && a.RunID == run.ID {
			path = a.LogPath
		}
	}
	if path == "" {
		return "", nil
	}
	dir := s.src.LogDir()
	if dir == "" {
		return "", errors.New("ログの保存が無効になっています")
	}
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, filepath.Clean(dir)+string(filepath.Separator)) {
		return "", errors.New("ログの保存先の外を指しています")
	}
	if _, err := os.Stat(clean); err != nil {
		return "", fmt.Errorf("ログファイルがありません: %s", filepath.Base(clean))
	}
	return clean, nil
}

func (s *Server) toRunView(run *store.Run, active *Active) *runView {
	return &runView{
		ID:        run.ID,
		Repo:      run.Repo,
		Issue:     run.IssueNumber,
		Phase:     run.Phase,
		StartedAt: timePtr(run.StartedAt),
		EndedAt:   timePtr(run.EndedAt),
		Result:    run.Result,
		HasLog:    run.LogPath != "" || (active != nil && active.RunID == run.ID && active.LogPath != ""),
		Running:   active != nil && active.RunID == run.ID,
	}
}

func itemKey(repo string, issue int) string { return repo + "#" + strconv.Itoa(issue) }

// timePtr はゼロ値の時刻を JSON の null にするためにポインタへ変換する。
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
