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

### 1. GitHub 側 (Project の作成)

**`autopilot setup-project`** コマンドを使うと、7 つのステータス（カラー付き）を備えた Projects v2 を一発で作成できる:

```sh
# 認証トークンの設定 (classic PAT: repo, project スコープが必要)
export GH_TOKEN=ghp_xxxxxxxxxxxx

# Project の作成 (個人アカウントの場合)
go run ./cmd/autopilot setup-project --title "Autopilot Board"

# Organization 配下に作成する場合
# go run ./cmd/autopilot setup-project --owner my-org --owner-type organization --title "Autopilot Board"
```

コマンド完了時に表示される `project:` 設定スニペットを、`config.yaml` にそのまま貼り付ければ完了です。

- Project の **Auto-add workflow** を有効にする（Issue が自動でカード化される）
- Project の組み込みワークフロー **「Item closed → Status: Done」** を有効にする

### 2. 設定

```sh
cp config.example.yaml config.yaml
$EDITOR config.yaml   # project.owner / project.number / repos を書き換える
```

### 3. 検証と起動

```sh
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

## コマンド

| コマンド | 役割 |
|---|---|
| `autopilot run` | 常駐してパイプラインを回す |
| `autopilot init` | コールドスタートのシード |
| `autopilot status` | ローカル状態（Status / PR / ブランチ / リトライ回数）の一覧 |
| `autopilot doctor` | 設定と前提条件の検証 |

共通フラグ: `-c, --config <path>`（既定 `config.yaml`）、`-v, --verbose`

## エージェントの差し替え

用途（`refine` / `implement` / `review` / `triage`）ごとに CLI を選べる。
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
