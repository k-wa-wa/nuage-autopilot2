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

runs(id, issue_number, phase, started_at, ended_at, result)  -- 実行ログ
```

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

## 4. 未決定事項

以下は実装着手前、または実装しながら確定させる。

1. **エージェント実行契約**
   - ワークスペース管理（Issue ごとに clone か git worktree か。直列実行なら worktree 1本で足りる）
   - ブランチ命名規則、push に使うクレデンシャル
   - 権限モード、タイムアウト、コスト上限
   - 実装用エージェントと検証（セルフレビュー）用エージェントのプロンプト分離
2. **品質ゲート定義ファイルの規約**
   - 対象リポジトリ内の固定パス（例: `.nuage/gates.md`）とフォーマット
3. **PR コメントの検知範囲**
   - Issue comment on PR / inline review comment / review submission (APPROVE, REQUEST_CHANGES) はそれぞれ API が異なる。どれを起床トリガーとするか
4. **CLI のコマンド体系**
   - 常駐化の前に単発コマンド（1周だけポーリングして1件処理して終了）を用意し、常駐モードをその繰り返しとして実装する方針
5. **要検証: cross-reference の通知有無**
   - 子Issue から親Issue を参照した際に親へ通知が飛ぶ場合、親Issue が子の起票ごとに起床する。実機で確認する（起床しても「何もせず寝る」で済む想定）
