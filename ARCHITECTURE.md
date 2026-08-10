# 実装アーキテクチャ (v0.1)

[DESIGN.md](./DESIGN.md) で定義したステートマシンを、どう検知し・どう実行するかを定める。
DESIGN.md が「何が起きるか（What）」、本ドキュメントが「どう実現するか（How）」を担当する。

実装形態は **Go の CLI**（常駐ワーカーモードを持つ単一バイナリ）を想定する。

## 1. イベント検知

### 1.1 Webhook を使わない理由

Projects v2 のイベント（`projects_v2_item`）を受け取れるのは Organization 配下の Project に限られ、個人アカウント配下の Project では利用できない。
また Webhook の受信には公開エンドポイントの運用が必要になる。v0.1 ではローカル常駐のワーカーを前提とするため、**ポーリング方式を採用する**。

### 1.2 2つのポーリングループ

単一の API では全イベントを賄えないため、性質の異なる2つのループを併走させる。

| | **Loop A: Project poll** | **Loop B: Notification poll** |
|---|---|---|
| API | GraphQL `ProjectV2.items` | REST `GET /notifications` |
| 間隔 | 60秒 | 60秒 |
| 検知対象 | **Ready への移動**、新規カードの出現、人手による任意の Status 変更 | コメント投稿、レビュー提出、Issue クローズ |
| 検知方法 | DB に保存した `last_status` との差分 | `all=true` + `since` カーソル |

**Loop A が必要な理由**: Projects v2 のフィールド値変更（＝カードのドラッグ）は通知を生成しない。
DESIGN.md で唯一の人間の手動操作と定めた「Inbox → Ready」は、Loop A でしか検知できない。

**Loop A のコスト**: Status のみを射影して取得するため軽量。

```graphql
items(first: 100, after: $cursor) {
  nodes {
    id
    content { ... on Issue { number state stateReason } }
    fieldValueByName(name: "Status") {
      ... on ProjectV2ItemFieldSingleSelectValue { name }
    }
  }
}
```

Issue 本文もコメントも取得しないため、100件で1リクエスト（≒1ポイント / 上限 5,000ポイント毎時）。
60秒間隔でも 60ポイント毎時に収まる。`Done` のカードを Archive すれば item 数は一定に保たれる。

### 1.3 Notification poll の注意点

- **既読/未読を状態として使わない。** `all=true` と `since` カーソルで取得する。ユーザー自身の PAT を使う場合、通知インボックスは人間と共有されるため、人間が Web UI で既読にするとイベントを取りこぼす。
- **自分の行動は自分に通知されない。** これがエージェントの自己起動ループを防ぐ一次防御として機能する。ただし二重処理防止のため、DB 側でも `last_comment_id` を保持する。
- **スレッドは集約される。** 複数コメントが1スレッドにまとまるため、`latest_comment_url` だけを見ると中間のコメントを落とす。起床後は対象 Issue のコメントを `since` で引き直す。
- **Bot を別アカウントで運用する場合**、リポジトリを Watch (All Activity) に設定しないと未参加 Issue の通知が届かない。ただし新規 Issue の出現は Loop A が検知するため、実害は限定的。

### 1.4 リコンサイル（保険）

通知の取りこぼしを自己修復するため、5〜10分間隔で `GET /repos/{owner}/{repo}/issues?since=&sort=updated&state=all` による差分確認を行う。安価なので常時有効とする。

### 1.5 前提条件（セットアップ要件）

Loop A は Project に載っている item しか見ないため、以下がシステムの前提となる。

- **Project の Auto-add workflow を有効化**し、対象リポジトリの Issue が自動で Project に追加されること。
- **Project の組み込みワークフロー「Item closed → Status: Done」を有効化**すること（またはワーカー側で `state` を監視して Done を設定する）。
- 認証は **Projects v2 への書き込み権限**を持つトークンが必要（classic PAT の `project` スコープ、または Project 権限を付与した GitHub App）。Actions の `GITHUB_TOKEN` では Project を操作できない。

## 2. 状態管理（DB）

### 2.1 位置づけ

DB は **GitHub の権威あるコピーではなく、キャッシュ・カーソル・リトライ回数の置き場**とする。
GitHub 側が常に真実源であり、DB を削除しても（後述のコールドスタート規約に従えば）システムは復旧できる。

| データ | GitHub から復元 | 消失時の挙動 |
|---|---|---|
| `last_status` / `project_item_id` | 可（GraphQL） | 差分が一旦リセットされ、次の変更から追随する |
| notification カーソル | 可（実質的に） | 再スキャンで復旧 |
| `last_comment_id` | 可（Issue から） | コールドスタート規約で現在時刻をシード |
| **`retry_count`** | **不可** | 0 にリセットされ、再度最大5回まで試行する（fail-safe 側なので許容） |
| **`lease_until`** | **不可** | プロセス自体が消えているため意味を持たない |

### 2.2 スキーマ（SQLite）

```
items(
  issue_number     INTEGER PRIMARY KEY,
  project_item_id  TEXT,
  last_status      TEXT,     -- Loop A の差分検出用
  last_comment_id  INTEGER,  -- 二重処理防止
  retry_count      INTEGER,  -- Verifying → In Progress の往復回数
  lease_until      TIMESTAMP,-- In Progress のスタック検知用
  updated_at       TIMESTAMP
)

cursors(name TEXT PRIMARY KEY, value TEXT)  -- notifications since 等

runs(id, issue_number, phase, started_at, ended_at, result, log_path)  -- 実行ログ
```

`log_path` はエージェントの出力ファイルの位置で、参照 UI（§8）が実行後に
プロンプトと出力を辿るために使う。パスはプロセス起動時に初めて確定するため、
`started_at` の記録とは別のタイミングで書き込まれる。

### 2.3 コールドスタート規約

DB が空の状態で全 Issue の全コメント履歴を再生すると、過去の質問に今さら返信を始めてしまう。

> **コールドスタート時は「現在」をシードにする。**
> 各 open item の最新コメント ID を `last_comment_id` に書き込み、過去は処理済みとみなす。notification カーソルも現在時刻とする。

これにより「DB を消して作り直す」が安全な運用操作になる。CLI にも明示的なコマンドとして持たせる。

## 3. ディスパッチ

エージェントを起動するかどうかは、**Status 単体ではなく `(Status, イベント種別)` の組**で決まる。
たとえば `Inbox` は「新規要求（→精緻化する）」「Ready 待ち（→何もしない）」「回答待ち（→何もしない）」「回答が来た（→再精緻化）」を兼務しており、Status だけでは分岐できない。

| Status | イベント | アクション |
|---|---|---|
| Inbox | 新規 item 出現 | 仕様精緻化 |
| Inbox | コメント（人間） | 仕様精緻化（回答を取り込む） |
| Inbox | 変化なし | 何もしない（親Issue はここに静止する） |
| Ready | Status 変更検知 | In Progress へ移動し実装開始 |
| In Progress | lease 切れ | Blocked へエスカレーション |
| Verifying | 毎tick | CI 状態を確認して分岐 |
| In Review | コメント（人間） | 質問回答 or In Progress へ戻して修正 |
| Blocked | コメント（人間） | In Progress または Inbox へ復帰 |
| （全て） | Issue クローズ | 終端。以後起動しない |

### 3.1 直列実行と Verifying の非占有性

- **`In Progress` は同時に1件のみ**（ワークスペースを占有する長時間のエージェント実行）。
- **`Verifying` は何件並存してもよい**。エージェントは走っておらず、ワーカーは毎tick CI の状態を確認するだけ（数百ms）。CI 待ちがパイプライン全体をブロックしない。

### 3.2 異常検知

- `In Progress`: `lease_until` を超えても更新がなければ、プロセス異常終了とみなし `Blocked` へ。
- `Verifying`: CI 待ちが最大待機時間を超えたら `Blocked` へ。

## 4. エージェント実行契約

### 4.1 責務の分離

> **エージェントは中身を書き、ワーカーは Status を動かす。**

Issue 本文の更新・コメント投稿・子 Issue の起票・PR の作成はすべてエージェントが `gh` コマンドで行う。
ワーカーが GitHub に書き込むのは **Project の Status** と、**Blocked 時の理由コメント**だけ。
プロンプトではエージェントに対し Status フィールドを変更しないよう明示する。

### 4.2 ワークスペース

- 起動時に対象リポジトリを**すべて clone** する（`workspace` 配下に `owner/name` で配置）。
- エージェント実行の直前に `fetch --prune` → `reset --hard` → `clean -fd` で origin の最新へ巻き戻す。
  前回のクラッシュが残した未コミットの変更や未追跡ファイルはここで破棄される。
- `In Progress` は直列なので、1 リポジトリにつきワークツリーは 1 本で足りる（worktree 分割はしない）。

### 4.3 ブランチ

**ワーカーはブランチ名を指定しない。** エージェントが任意の名前で作る。
ワーカーは実装完了後に Issue の timeline（`CROSS_REFERENCED_EVENT` / `CONNECTED_EVENT`）から
紐づく PR を発見し、その `headRefName` を DB に記録する。
CI 失敗やレビュー指摘で `In Progress` に戻す際は、記録したブランチをチェックアウトしてから再開する。

この経路が成立する前提として、**PR 本文に `Closes #<Issue番号>` を含めること**を実装プロンプトで要求する。

PR 作成直後は timeline への反映にラグがあるため、数回リトライしてから「PR 無し」と判断する。

### 4.4 認証

`GH_TOKEN`（無ければ `GITHUB_TOKEN`）の classic PAT を全経路で流用する。

- GitHub API: `Authorization: Bearer`
- 子プロセス（`gh` / エージェント）: 環境変数として引き渡す
- `git push`: clone 済みリポジトリのローカル設定に credential helper を仕込み、
  環境変数からトークンを読ませる。**トークンをディスクに残さない**。

  ```
  credential.helper = !f() { echo "username=x-access-token"; echo "password=${GH_TOKEN}"; }; f
  ```

### 4.5 エージェントの起動

用途は 4 つあり、それぞれ独立に CLI を差し替えられる。

| 用途 | 役割 | タイムアウト既定 |
|---|---|---|
| `refine` | 仕様精緻化・質問・タスク分割 | 30m |
| `implement` | 実装・テスト・PR 作成 | 2h |
| `review` | セルフレビューと品質ゲート検証 | 30m |
| `triage` | レビュー指摘 / 助言コメントの判断 | 30m |

超過時はプロセスを終了し `Blocked` へ送る。

CLI ごとの起動方法の違い（プロンプトを標準入力で渡すか argv で渡すか、CLI 側の
内部タイムアウトを揃えるか）は、`command` から解決するアダプタが吸収する。
設定に書くのは `command` / `model` / `args` / `timeout` だけで、非対話モードや
権限スキップなどの必須フラグはアダプタが付ける。
`claude` と `agy` に専用アダプタがあり、それ以外は `command` と `args` をそのまま起動する。

実装とその理由は `internal/agent/adapter.go` を参照。

### 4.6 判断結果の受け取り（マーカー）

エージェントの判断は、出力の**行頭マーカー**で受け取る。行頭でない同名文字列は拾わない。
同じキーが複数回現れた場合は**最後の出力**を採用する（プロンプトの引用より実際の判断を優先するため）。

| フェーズ | マーカー | 値 | ワーカーの遷移 |
|---|---|---|---|
| refine | `AUTOPILOT_ACTION` | `READY_FOR_HUMAN` / `QUESTION_POSTED` / `SPLIT` | いずれも Inbox に留まる（Ready は人間の判断） |
| implement | `AUTOPILOT_ACTION` | `PR_READY` | PR を発見して `Verifying` |
| implement | | `BLOCKED` | `Blocked` |
| review | `AUTOPILOT_VERDICT` | `PASS` | `In Review`（retry_count をリセット） |
| review | | `FAIL` | `In Progress` へ差し戻し（retry_count++） |
| triage (In Review) | `AUTOPILOT_ACTION` | `ANSWERED` / `NEEDS_FIX` | 据え置き / `In Progress` |
| triage (Blocked) | `AUTOPILOT_ACTION` | `RESUME` / `RESPEC` | `In Progress` / `Inbox` |

`AUTOPILOT_REASON` は 1 行の理由。差し戻し時は次の実装プロンプトに、Blocked 時はコメントに載せる。

## 5. 品質ゲート定義

対象リポジトリの **`.agents/autopilot-gate.md`** を固定パスとする。
存在すれば `review` フェーズのプロンプトにそのまま埋め込む。フォーマットは自由記述（プロンプト）。

## 6. PR コメントの検知範囲

「問題なければ何も書かない」運用を前提に、人間が残した発言は Issue コメント・レビュー本文・
diff のインラインコメントを問わず、すべて行動の要求とみなして拾う。

同じレビューの本文と行コメントで二重に起床しないよう、レビュー単位に束ねて 1 入力にする。
判定の詳細（Approve や Dismiss の扱い、カーソルの持ち方）は `handleReview` のコメントを参照。

## 7. CLI とプロセス構成

単一バイナリ `autopilot`。

| コマンド | 役割 |
|---|---|
| `autopilot setup-project` | GitHub Projects v2 に 7 つの Status 選択肢を設定・修復 |
| `autopilot run` | 常駐してパイプラインを回す |
| `autopilot init` | コールドスタートのシード（現在を処理済みとして記録） |
| `autopilot status` | ローカル状態の一覧表示 |
| `autopilot doctor` | 設定・トークン・Status 名・エージェントコマンド・clone の検証 |

`run` は goroutine を分けた常駐構成をとる。ループごとに周期も API も異なるため、
単発コマンドの繰り返しではなく最初から並行構成にする。

| goroutine | 役割 |
|---|---|
| project-poller | Loop A。Status の差分検出 |
| notification-poller | Loop B。コメント / レビューの起床シグナル |
| tick-poller | `Verifying` の CI 確認と `In Progress` の lease 切れ検知 |
| reconciler | 通知の取りこぼしの自己修復 |
| dispatcher | イベントを受けて状態遷移とジョブ投入（短時間処理のみ） |
| agent-worker | **エージェント起動を直列に処理する唯一の goroutine** |
| web | 参照 UI の HTTP 待ち受け（§8）。パイプラインの判断には関与しない |

`In Progress` の直列性は agent-worker が 1 本であることで保証される。
`Verifying` の CI 確認は dispatcher 内で完結し、エージェントを起動しないためパイプラインを占有しない。
同一 item に対するジョブの二重投入は in-flight セットで防ぐ。

### 7.1 クラッシュ復帰

- 起動時に `In Progress` の item があれば、単一インスタンス前提により前回の残骸とみなし、
  lease を切らせて `Blocked` へ送る。
- 実行中の item に新しいコメントが来た場合は**カーソルを進めない**。
  リコンサイルが再検出し、レーンが空いた時点で処理される。

## 8. 参照 UI

`run` プロセスに HTTP サーバを 1 つ同居させ、ローカル状態をブラウザから見られるようにする。
`web.addr`（既定 `127.0.0.1:8787`、空文字で無効）で待ち受ける。

## 9. 残課題

- **cross-reference の通知有無**: 子 Issue から親 Issue を参照した際に親へ通知が飛ぶかは未検証。
  一旦「飛ばない」前提とする。飛ぶ場合でも親 Issue は Inbox で refine が走るだけなので致命的ではない。
- **コスト上限**: 現状はタイムアウトのみで、トークン量による打ち切りは持っていない。
- **複数インスタンス**: 単一インスタンス前提。起動時の孤児回収がこの前提に依存している。
