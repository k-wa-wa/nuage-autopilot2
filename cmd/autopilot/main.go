// Command autopilot は GitHub Projects をステートマシンとする自動開発パイプラインの
// 常駐ワーカー。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/k-wa-wa/nuage-autopilot2/internal/config"
	"github.com/k-wa-wa/nuage-autopilot2/internal/engine"
)

const usage = `autopilot - 自動開発パイプラインの常駐ワーカー

使い方:
  autopilot <command> [flags]

コマンド:
  run       常駐してパイプラインを回す
  init      コールドスタートのシードを行う（現在を処理済みとして記録する）
  status    ローカル状態を一覧表示する
  doctor    設定と前提条件を検証して終了する

共通フラグ:
  -c, --config <path>   設定ファイル（既定: config.yaml）
  -v, --verbose         デバッグログを出力する
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		return errors.New("コマンドを指定してください")
	}
	cmd := os.Args[1]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Print(usage)
		return nil
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var cfgPath string
	var verbose bool
	fs.StringVar(&cfgPath, "config", "config.yaml", "設定ファイルのパス")
	fs.StringVar(&cfgPath, "c", "config.yaml", "設定ファイルのパス（短縮形）")
	fs.BoolVar(&verbose, "verbose", false, "デバッグログを出力する")
	fs.BoolVar(&verbose, "v", false, "デバッグログを出力する（短縮形）")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "run":
		return cmdRun(ctx, cfg, log)
	case "init":
		return cmdInit(ctx, cfg, log)
	case "status":
		return cmdStatus(ctx, cfg, log)
	case "doctor":
		return cmdDoctor(ctx, cfg, log)
	default:
		fmt.Print(usage)
		return fmt.Errorf("未知のコマンド: %s", cmd)
	}
}

func cmdRun(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	e, err := engine.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer e.Close()
	return e.Run(ctx)
}

func cmdInit(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	e, err := engine.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer e.Close()
	return e.Init(ctx)
}

func cmdStatus(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	e, err := engine.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer e.Close()

	items, err := e.Items()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tISSUE\tSTATUS\tPR\tBRANCH\tRETRY\tTERMINAL\tUPDATED")
	for _, it := range items {
		pr := "-"
		if it.PRNumber != 0 {
			pr = fmt.Sprintf("#%d", it.PRNumber)
		}
		branch := it.Branch
		if branch == "" {
			branch = "-"
		}
		updated := "-"
		if !it.UpdatedAt.IsZero() {
			updated = it.UpdatedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s\t#%d\t%s\t%s\t%s\t%d\t%v\t%s\n",
			it.Repo, it.IssueNumber, it.LastStatus, pr, branch, it.RetryCount, it.Terminal, updated)
	}
	return w.Flush()
}

func cmdDoctor(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	// engine.New が Project の解決と Status 名の検証まで行う。
	e, err := engine.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer e.Close()

	fmt.Printf("✓ 認証ユーザー: %s\n", e.Login())
	fmt.Printf("✓ Project: %s/%d (Status フィールドの選択肢は設定と一致)\n",
		cfg.Project.Owner, cfg.Project.Number)
	fmt.Printf("✓ DB: %s\n", cfg.Database)
	fmt.Printf("✓ ワークスペース: %s\n", cfg.Workspace)

	for _, kind := range []string{config.AgentRefine, config.AgentImplement, config.AgentReview, config.AgentTriage} {
		a := cfg.AgentFor(kind)
		if _, err := exec.LookPath(a.Command); err != nil {
			fmt.Printf("✗ エージェント %s: コマンド %q が PATH にありません\n", kind, a.Command)
			continue
		}
		fmt.Printf("✓ エージェント %s: %s %v (timeout %s)\n", kind, a.Command, a.Args, a.Timeout)
	}

	repos := make([]string, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		repos = append(repos, r.String())
	}
	fmt.Printf("… リポジトリを同期しています: %v\n", repos)
	if err := e.EnsureRepos(ctx); err != nil {
		return err
	}
	fmt.Printf("✓ リポジトリの clone を確認しました\n")

	fmt.Println()
	fmt.Println("次の前提条件は API から検証できないため、手動で確認してください:")
	fmt.Println("  - Project の Auto-add workflow が有効（Issue が自動でカード化される）")
	fmt.Println("  - Project の組み込みワークフロー「Item closed → Status: Done」が有効")
	fmt.Println("  - トークンに project スコープ（classic PAT）があること")
	return nil
}
