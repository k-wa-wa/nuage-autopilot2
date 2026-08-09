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
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"nuage-autopilot2/internal/agent"
	"nuage-autopilot2/internal/config"
	"nuage-autopilot2/internal/engine"
	"nuage-autopilot2/internal/gh"
	"nuage-autopilot2/internal/store"
	"nuage-autopilot2/internal/workspace"
)

const usage = `autopilot - 自動開発パイプラインの常駐ワーカー

使い方:
  autopilot <command> [flags]

コマンド:
  run             常駐してパイプラインを回す
  init            コールドスタートのシードを行う（現在を処理済みとして記録する）
  status          ローカル状態を一覧表示する
  doctor          設定と前提条件を検証して終了する
  setup-project   GitHub Projects v2 に 7 つの Status 選択肢を設定・修復する

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	switch cmd {
	case "run":
		return cmdRun(ctx, cfg, log)
	case "init":
		return cmdInit(ctx, cfg, log)
	case "status":
		return cmdStatus(ctx, cfg, log)
	case "doctor":
		return cmdDoctor(ctx, cfg, log)
	case "setup-project":
		return cmdSetupProject(ctx, cfg, log)
	default:
		fmt.Print(usage)
		return fmt.Errorf("未知のコマンド: %s", cmd)
	}
}

func cmdSetupProject(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	client, err := gh.New()
	if err != nil {
		return err
	}

	owner := cfg.Project.Owner
	ownerType := cfg.Project.OwnerType
	number := cfg.Project.Number
	fieldName := cfg.Project.StatusField

	fmt.Printf("Project %s/%d の Status 選択肢（7ステータス）を設定・修復しています...\n", owner, number)
	projectID, err := client.GetProjectID(ctx, ownerType, owner, number)
	if err != nil {
		return fmt.Errorf("Project の取得に失敗: %w", err)
	}
	if err := client.ConfigureProjectStatuses(ctx, projectID, fieldName, nil); err != nil {
		return err
	}
	fmt.Printf("\n✨ Project %s/%d の Status 選択肢（7ステータス）を正常に設定・修復しました！\n", owner, number)
	return nil
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
	hasError := false

	client, err := gh.New()
	if err != nil {
		fmt.Printf("✗ トークン: %v\n", err)
		return err
	}
	if err := client.ResolveLogin(ctx); err != nil {
		fmt.Printf("✗ 認証ユーザーの取得に失敗: %v\n", err)
		return err
	}
	fmt.Printf("✓ 認証ユーザー: %s\n", client.Login)

	project, err := client.LoadProject(ctx, cfg.Project.OwnerType, cfg.Project.Owner,
		cfg.Project.Number, cfg.Project.StatusField)
	if err != nil {
		fmt.Printf("✗ Project %s/%d: %v\n", cfg.Project.Owner, cfg.Project.Number, err)
		fmt.Println("  ➔ 💡 対処法: `autopilot setup-project` を実行すると自動で修復・設定できます")
		hasError = true
	} else {
		// 各 Status 名が存在するか検証
		var missing []string
		for _, s := range []string{
			cfg.Statuses.Inbox, cfg.Statuses.Ready, cfg.Statuses.InProgress,
			cfg.Statuses.Verifying, cfg.Statuses.InReview, cfg.Statuses.Blocked, cfg.Statuses.Done,
		} {
			if _, err := project.OptionID(s); err != nil {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			fmt.Printf("✗ Project %s/%d: Status 選択肢が不足しています: %v\n",
				cfg.Project.Owner, cfg.Project.Number, missing)
			fmt.Println("  ➔ 💡 対処法: `autopilot setup-project` を実行すると自動で選択肢を追加・修復できます")
			hasError = true
		} else {
			fmt.Printf("✓ Project: %s/%d (Status フィールドの選択肢は設定と一致)\n",
				cfg.Project.Owner, cfg.Project.Number)
		}
	}

	// 実際に開いてスキーマ適用まで通す。書き込めないパスや壊れた DB を
	// 「✓」で通してしまうと、doctor の存在意義が無くなる。
	if st, err := store.Open(cfg.Database); err != nil {
		fmt.Printf("✗ DB: %s: %v\n", cfg.Database, err)
		hasError = true
	} else {
		st.Close()
		fmt.Printf("✓ DB: %s\n", cfg.Database)
	}
	// ワークスペースは下のリポジトリ同期で検証する。
	fmt.Printf("… ワークスペース: %s\n", cfg.Workspace)

	for _, use := range config.AgentUses {
		spec := cfg.AgentFor(use).Spec()
		command := spec.ResolvedCommand()
		adapter := spec.Adapter()

		inv, err := adapter.Build(agent.Request{
			Prompt: "<prompt>", Model: spec.Model, ExtraArgs: spec.ExtraArgs, Timeout: spec.Timeout,
		})
		if err != nil {
			fmt.Printf("✗ エージェント %s: 起動引数を組み立てられません: %v\n", use, err)
			hasError = true
			continue
		}
		mark := "✓"
		if _, err := exec.LookPath(command); err != nil {
			mark = "✗"
			hasError = true
		}
		fmt.Printf("%s エージェント %s (adapter: %s, timeout %s)\n    %s %s\n",
			mark, use, adapter.Name(), spec.Timeout, command, strings.Join(inv.DisplayArgs(), " "))
		if mark == "✗" {
			fmt.Printf("    コマンド %q が PATH にありません\n", command)
		}
		if adapter.Name() == agent.AdapterExec {
			fmt.Printf("    ※ 専用アダプタが無いため、非対話モード等のフラグは args に自分で指定する必要があります\n")
		}
	}

	ws := workspace.New(cfg.Workspace, "GH_TOKEN", "", "")
	repos := make([]string, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		repos = append(repos, r.String())
	}
	fmt.Printf("… リポジトリを同期しています: %v\n", repos)
	if err := ws.EnsureAll(ctx, repos); err != nil {
		fmt.Printf("✗ リポジトリの同期に失敗: %v\n", err)
		hasError = true
	} else {
		fmt.Printf("✓ リポジトリの clone を確認しました\n")
	}

	fmt.Println()
	fmt.Println("次の前提条件は API から検証できないため、手動で確認してください:")
	fmt.Println("  - Project の Auto-add workflow が有効（Issue が自動でカード化される）")
	fmt.Println("  - Project の組み込みワークフロー「Item closed → Status: Done」が有効")
	fmt.Println("  - トークンに project スコープ（classic PAT）があること")

	if hasError {
		return errors.New("一部の前提条件が満たされていません")
	}
	return nil
}
