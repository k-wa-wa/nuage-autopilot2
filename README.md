# autopilot

GitHub Projects をステートマシンの単一の真実源として使う、自動開発パイプラインの常駐ワーカー。

人間が関わるのは 3 点だけ。**曖昧な要求を Issue に書く / Ready に動かす / PR をレビューしてマージする。**
要件精緻化・実装・テスト・セルフレビュー・検証は自律エージェントが行う。

- 状態機械の定義（What）: [DESIGN.md](./DESIGN.md)
- 実装アーキテクチャ（How）: [ARCHITECTURE.md](./ARCHITECTURE.md)

## レーン

```
Inbox ──[人間がドラッグ]──> Ready ──> In Progress ──> Verifying ──> In Review ──> Done
  ↑                                       ↑              │              │
  │                                       └──────────────┘              │
  └──────────────────────── Blocked <─────┴──────────────┘──────────────┘
```

人間が手動で Status を変えるのは **Inbox → Ready の 1 回だけ**。
それ以外の復帰（レビュー指摘、Blocked の助言、仕様質問への回答）は、
**コメントを書くだけ**でエージェントが起床し、自分で適切なレーンに移して自走する。

## セットアップ

### 1. GitHub 側の準備

1. **Project の作成**:
   GitHub の Web UI 等で Projects v2 を作成します（テンプレートは「Iterative development」や「Board」等を選択）。

2. **認証トークンの設定** (classic PAT: `repo`, `project` スコープが必要):
   ```sh
   export GH_TOKEN=ghp_xxxxxxxxxxxx
   ```

3. **設定ファイルの準備**:
   ```sh
   cp config.example.yaml config.yaml
   $EDITOR config.yaml   # project.owner / project.number / repos などを設定
   ```

4. **Status 選択肢のセットアップ**:
   `autopilot setup-project` を実行すると、`config.yaml` で指定した対象 Project に autopilot の 7 つのステータス（カラー・説明付き）が一括で設定・修復されます:
   ```sh
   go run ./cmd/autopilot setup-project
   ```

5. **Project の組み込みワークフローを有効化**:
   - Project の **Auto-add workflow** を有効にする（対象リポジトリの Issue が自動でカード化される）
   - Project の組み込みワークフロー **「Item closed → Status: Done」** を有効にする

### 2. 検証と起動

```sh
# 参照 UI は Go のバイナリに埋め込むが、生成物はリポジトリで追跡していない。
# 先にビルドしないと UI が「未ビルド」の案内ページになる（API と CLI は動く）。
cd web && npm ci && npm run build && cd ..

go build -o autopilot ./cmd/autopilot

./autopilot doctor     # トークン・Project・Status 名・エージェント・clone を検証
./autopilot init       # コールドスタートのシード（現在を処理済みとして記録）
./autopilot run        # 常駐開始
```

`init` を省いても `run` が DB の空を検出して自動でシードする。
**DB を消して作り直しても安全**で、過去のコメントを再生することはない。

## 品質ゲート

対象リポジトリに `.agents/autopilot-gate.md` を置くと、
セルフレビュー（`Verifying`）のプロンプトにそのまま埋め込まれる。書式は自由。

```markdown
# 品質ゲート

- `npm test` と `npm run lint` が通ること
- Preview URL（PR にコメントされる）を開き、対象画面が表示されることを確認すること
- DB マイグレーションを含む場合はロールバック手順を PR 本文に書くこと
```

## TODO の定期サマリ

`config.yaml` に cron 式を書くと、その時刻に読み取り専用のエージェントが起動し、
パイプラインの現況から **対応待ちの TODO** を抜き出して参照 UI の先頭に表示する。

## コマンド

| コマンド | 役割 |
|---|---|
| `autopilot setup-project` | GitHub Projects v2 に 7 つの Status 選択肢を設定・修復 |
| `autopilot run` | 常駐してパイプラインを回す |
| `autopilot init` | コールドスタートのシード |
| `autopilot status` | ローカル状態（Status / PR / ブランチ / リトライ回数）の一覧 |
| `autopilot summarize` | TODO サマリをその場で 1 回生成する |
| `autopilot doctor` | 設定と前提条件の検証 |

共通フラグ: `-c, --config <path>`（既定 `config.yaml`）、`-v, --verbose`

## エージェントの差し替え

用途（`refine` / `implement` / `review` / `triage` / `summarize`）ごとに CLI を選べる。
**`command` から起動方法を解決する**ので、指定するのはコマンド名だけでよい。
非対話モードや権限スキップなどの必須フラグは自動で付く。

| command | プロンプトの渡し方 |
|---|---|
| `claude` | 標準入力 |
| `agy` | `--print` の引数（`--print-timeout` も自動で合わせる） |
| それ以外 | 標準入力（`command` と `args` をそのまま起動） |

エージェントは判断結果を出力の行頭マーカーで返す（`AUTOPILOT_ACTION` / `AUTOPILOT_VERDICT` / `AUTOPILOT_REASON`）。
詳細は [ARCHITECTURE.md §4.5〜4.6](./ARCHITECTURE.md)。

## 設計上の約束

- **エージェントは中身を書き、ワーカーは Status を動かす。** Issue 本文・コメント・PR の操作は
  すべてエージェントが `gh` で行い、ワーカーは Project の Status と Blocked コメントだけを書く。
- **DB はキャッシュ。** GitHub が常に真実源で、DB が持つのは差分検出用のスナップショット、
  カーソル、リトライ回数だけ。
- **In Progress は 1 件ずつ。** ただし CI 待ち（Verifying）はエージェントを持たないため並列に存在でき、
  パイプラインを占有しない。

## 開発

```sh
go test ./...
go vet ./...
```

参照 UI（`web/`、React + Vite）は別立てである。

```sh
cd web
npm ci
npm run dev        # モック（MSW）で単体表示
npm run build      # internal/web/assets/dist へ出力。go build はこれを埋め込む
npm run storybook  # コンポーネント単体の確認
```

`nix build .` はフロントエンドのビルドを内包しているため、npm を先に走らせる必要はない。
