// Command autopilot は GitHub Projects をステートマシンとする自動開発パイプラインの
// 常駐ワーカー。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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
  setup-project   GitHub Projects v2 を作成し、7 つの Status 選択肢を設定する

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

	// setup-project は設定ファイルなしで単独実行できる。
	if cmd == "setup-project" {
		return cmdSetupProject(ctx, os.Args[2:])
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

func cmdSetupProject(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("setup-project", flag.ExitOnError)
	var title, owner, ownerType, fieldName, cfgPath string
	var forceNew, verbose bool
	flags.StringVar(&cfgPath, "config", "config.yaml", "設定ファイルのパス")
	flags.StringVar(&cfgPath, "c", "config.yaml", "設定ファイルのパス（短縮形）")
	flags.StringVar(&title, "title", "Autopilot Board", "作成するプロジェクトのタイトル（新規作成時）")
	flags.StringVar(&owner, "owner", "", "GitHub オーナー名（未指定なら設定ファイルまたは認証ユーザー）")
	flags.StringVar(&ownerType, "owner-type", "", "オーナーの種別: user または organization")
	flags.StringVar(&fieldName, "field", "", "Status を管理するフィールド名")
	flags.BoolVar(&forceNew, "new", false, "設定ファイルの Project 番号を無視して新規作成する")
	flags.BoolVar(&verbose, "verbose", false, "解決したパラメータを表示する")
	flags.BoolVar(&verbose, "v", false, "解決したパラメータを表示する（短縮形）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if ownerType != "" && ownerType != "user" && ownerType != "organization" {
		return fmt.Errorf("--owner-type は user か organization を指定してください（指定値: %q）", ownerType)
	}

	client, err := gh.New()
	if err != nil {
		return err
	}

	// 設定ファイルは「無い」なら新規作成に進むが、「壊れている」なら止める。
	// 区別しないと、agents のキーを打ち間違えただけで既存 Project の修復のつもりが
	// 新しい Project の作成にすり替わってしまう。
	cfg, cfgErr := config.Load(cfgPath)
	if cfgErr != nil && !errors.Is(cfgErr, fs.ErrNotExist) && !forceNew {
		return fmt.Errorf("設定ファイル %s を読めません: %w\n"+
			"  設定を直すか、新しい Project を作るなら --new を付けてください", cfgPath, cfgErr)
	}
	if verbose {
		if cfgErr != nil {
			fmt.Printf("[verbose] 設定ファイル %s は見つかりませんでした\n", cfgPath)
		} else {
			fmt.Printf("[verbose] 設定ファイル %s を読み込みました (project: %s/%d)\n",
				cfgPath, cfg.Project.Owner, cfg.Project.Number)
		}
	}

	if cfgErr == nil && !forceNew && cfg.Project.Number > 0 {
		// --- 既存 Project の修復モード ---
		if owner == "" {
			owner = cfg.Project.Owner
		}
		if ownerType == "" {
			ownerType = cfg.Project.OwnerType
		}
		if fieldName == "" {
			fieldName = cfg.Project.StatusField
		}
		fmt.Printf("既存の Project %s/%d の Status 選択肢を修復・設定しています...\n", owner, cfg.Project.Number)
		projectID, err := client.GetProjectID(ctx, ownerType, owner, cfg.Project.Number)
		if err != nil {
			return fmt.Errorf("Project の取得に失敗: %w", err)
		}
		if err := client.ConfigureProjectStatuses(ctx, projectID, fieldName, nil); err != nil {
			return err
		}
		fmt.Printf("\n✨ Project %s/%d の Status 選択肢（7ステータス）を正常に設定・修復しました！\n", owner, cfg.Project.Number)
		return nil
	}

	// --- 新規作成モード ---
	if owner == "" {
		if cfgErr == nil && cfg.Project.Owner != "" {
			owner = cfg.Project.Owner
		} else {
			if err := client.ResolveLogin(ctx); err != nil {
				return fmt.Errorf("認証ユーザーの取得に失敗: %w", err)
			}
			owner = client.Login
		}
	}
	if ownerType == "" {
		if cfgErr == nil && cfg.Project.OwnerType != "" {
			ownerType = cfg.Project.OwnerType
		} else {
			ownerType = "user"
		}
	}
	if fieldName == "" {
		fieldName = "Status"
	}

	fmt.Printf("GitHub Projects v2 を新規作成しています (owner: %s, type: %s, title: %q)...\n", owner, ownerType, title)
	info, err := client.CreateProjectWithStatuses(ctx, ownerType, owner, title, fieldName, nil)
	if err != nil {
		// Project 自体は作成済みのことがある。番号を伝えないと、リトライのたびに
		// 空の Project が増え続けてしまう。
		if info != nil && info.Number > 0 {
			fmt.Printf("\n⚠️  Project #%d は作成されましたが、Status 選択肢の設定に失敗しました。\n", info.Number)
			fmt.Printf("    URL: %s\n", info.URL)
			fmt.Printf("    修復するには config.yaml に number: %d を設定して `autopilot setup-project` を再実行してください。\n", info.Number)
			fmt.Printf("    （--new を付けると別の Project が新規作成されます）\n\n")
		}
		return err
	}

	fmt.Printf("\n✨ GitHub Projects v2 のセットアップが完了しました！\n\n")
	fmt.Printf("  プロジェクト番号: #%d\n", info.Number)
	fmt.Printf("  URL:             %s\n\n", info.URL)
	fmt.Printf("以下の設定を config.yaml の project セクションに貼り付けてください:\n\n")
	fmt.Printf("```yaml\nproject:\n  owner: \"%s\"\n  number: %d\n  owner_type: \"%s\"\n  status_field: \"%s\"\n```\n",
		owner, info.Number, ownerType, fieldName)
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
