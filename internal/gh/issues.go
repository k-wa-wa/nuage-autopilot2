package gh

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Issue は Issue の要約。
type Issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	StateReason string    `json:"state_reason"`
	HTMLURL     string    `json:"html_url"`
	UpdatedAt   time.Time `json:"updated_at"`
	User        User      `json:"user"`
	Labels      []Label   `json:"labels"`
	PullRequest *struct{} `json:"pull_request"`
}

// IsPullRequest は当該 Issue が実際には PR かどうかを返す。
func (i Issue) IsPullRequest() bool { return i.PullRequest != nil }

// User は GitHub アカウント。
type User struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// IsBot は Bot アカウントかどうかを返す。
func (u User) IsBot() bool {
	return u.Type == "Bot" || strings.HasSuffix(u.Login, "[bot]")
}

// Label はラベル。
type Label struct {
	Name string `json:"name"`
}

// Comment は Issue コメント。
type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	HTMLURL   string    `json:"html_url"`
}

// Review は PR のレビュー提出。
type Review struct {
	ID          int64     `json:"id"`
	Body        string    `json:"body"`
	State       string    `json:"state"` // APPROVED / CHANGES_REQUESTED / COMMENTED / DISMISSED
	User        User      `json:"user"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"`
}

// ReviewComment は PR の diff に付いたインラインコメント。
//
// レビュー提出にまとめて含まれるものも、diff 上で単発に投稿されたものも同じ
// エンドポイントから返る（後者にも GitHub 側で state=COMMENTED のレビューが作られる）。
type ReviewComment struct {
	ID       int64 `json:"id"`
	ReviewID int64 `json:"pull_request_review_id"`
	// InReplyToID は既存スレッドへの返信の場合に元コメントの ID。
	InReplyToID int64  `json:"in_reply_to_id"`
	Body        string `json:"body"`
	Path        string `json:"path"`
	// Line は現在の diff 上の行番号。差分が古くなると null になるため、
	// その場合は OriginalLine（投稿時点の行番号）で補う。
	Line         int       `json:"line"`
	OriginalLine int       `json:"original_line"`
	User         User      `json:"user"`
	CreatedAt    time.Time `json:"created_at"`
	HTMLURL      string    `json:"html_url"`
}

// Location は "path:line" 形式で指摘箇所を返す。行が特定できなければパスのみ。
func (r ReviewComment) Location() string {
	line := r.Line
	if line == 0 {
		line = r.OriginalLine
	}
	if line == 0 {
		return r.Path
	}
	return fmt.Sprintf("%s:%d", r.Path, line)
}

// GetIssue は Issue を 1 件取得する。
func (c *Client) GetIssue(ctx context.Context, repo string, number int) (*Issue, error) {
	var out Issue
	if err := c.rest(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repo, number), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListComments は Issue のコメントを取得する。since が非ゼロならそれ以降に絞る。
func (c *Client) ListComments(ctx context.Context, repo string, number int, since time.Time) ([]Comment, error) {
	path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", repo, number)
	if !since.IsZero() {
		path += "&since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}
	return restPaged[Comment](ctx, c, path)
}

// LastComments は直近 n 件のコメントを取得する（プロンプトのコンテキスト用）。
func (c *Client) LastComments(ctx context.Context, repo string, number, n int) ([]Comment, error) {
	all, err := c.ListComments(ctx, repo, number, time.Time{})
	if err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// AddComment は Issue にコメントを投稿する。
func (c *Client) AddComment(ctx context.Context, repo string, number int, body string) error {
	return c.rest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number),
		map[string]string{"body": body}, nil)
}

// ListReviews は PR のレビュー提出を取得する。
func (c *Client) ListReviews(ctx context.Context, repo string, prNumber int) ([]Review, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", repo, prNumber)
	return restPaged[Review](ctx, c, path)
}

// ListReviewComments は PR の diff に付いたインラインコメントを取得する。
func (c *Client) ListReviewComments(ctx context.Context, repo string, prNumber int) ([]ReviewComment, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d/comments?per_page=100", repo, prNumber)
	return restPaged[ReviewComment](ctx, c, path)
}

// LastReviewComments は直近 n 件のインラインコメントを取得する（プロンプトのコンテキスト用）。
func (c *Client) LastReviewComments(ctx context.Context, repo string, prNumber, n int) ([]ReviewComment, error) {
	all, err := c.ListReviewComments(ctx, repo, prNumber)
	if err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// PullRequest は PR の状態。
type PullRequest struct {
	Number         int
	State          string // OPEN / CLOSED / MERGED
	IsDraft        bool
	Merged         bool
	HeadRefName    string
	ReviewDecision string // APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED / ""
	// CheckState は statusCheckRollup の値。チェックが 1 つも無い場合は空文字。
	CheckState string // SUCCESS / FAILURE / ERROR / PENDING / EXPECTED / ""
}

// CIStatus は CI の判定結果。
type CIStatus int

// CI の判定結果。
const (
	// CIPending は CI がまだ完了していない状態。
	CIPending CIStatus = iota
	// CISuccess は CI が成功した、または CI が設定されていない状態。
	CISuccess
	// CIFailure は CI が失敗した状態。
	CIFailure
)

// CI は CheckState を判定結果に変換する。チェック未設定は成功扱いとする。
func (p *PullRequest) CI() CIStatus {
	switch p.CheckState {
	case "SUCCESS", "":
		return CISuccess
	case "FAILURE", "ERROR":
		return CIFailure
	default:
		return CIPending
	}
}

const prQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number state isDraft merged headRefName reviewDecision
      commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
    }
  }
}`

// GetPullRequest は PR の状態と CI ロールアップを取得する。
func (c *Client) GetPullRequest(ctx context.Context, repo string, number int) (*PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Repository struct {
			PullRequest *struct {
				Number         int    `json:"number"`
				State          string `json:"state"`
				IsDraft        bool   `json:"isDraft"`
				Merged         bool   `json:"merged"`
				HeadRefName    string `json:"headRefName"`
				ReviewDecision string `json:"reviewDecision"`
				Commits        struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								State string `json:"state"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	err = c.graphql(ctx, prQuery, map[string]any{"owner": owner, "name": name, "number": number}, &resp)
	if err != nil {
		return nil, err
	}
	pr := resp.Repository.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("%s#%d は PR として見つかりません", repo, number)
	}
	out := &PullRequest{
		Number: pr.Number, State: pr.State, IsDraft: pr.IsDraft, Merged: pr.Merged,
		HeadRefName: pr.HeadRefName, ReviewDecision: pr.ReviewDecision,
	}
	if n := pr.Commits.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
		out.CheckState = n[0].Commit.StatusCheckRollup.State
	}
	return out, nil
}

const linkedPRQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      timelineItems(last: 50, itemTypes: [CROSS_REFERENCED_EVENT, CONNECTED_EVENT]) {
        nodes {
          __typename
          ... on CrossReferencedEvent {
            source { ... on PullRequest { number state merged headRefName } }
          }
          ... on ConnectedEvent {
            subject { ... on PullRequest { number state merged headRefName } }
          }
        }
      }
    }
  }
}`

// FindLinkedPR は Issue に紐づく PR を探す。open な PR を優先し、
// 無ければ最後に見つかった PR を返す。見つからない場合は (nil, nil)。
//
// ブランチ名をワーカー側で指定しない設計のため、実装エージェントが作った PR は
// この経路で発見する。
func (c *Client) FindLinkedPR(ctx context.Context, repo string, issueNumber int) (*PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	type prRef struct {
		Number      int    `json:"number"`
		State       string `json:"state"`
		Merged      bool   `json:"merged"`
		HeadRefName string `json:"headRefName"`
	}
	var resp struct {
		Repository struct {
			Issue *struct {
				TimelineItems struct {
					Nodes []struct {
						TypeName string `json:"__typename"`
						Source   *prRef `json:"source"`
						Subject  *prRef `json:"subject"`
					} `json:"nodes"`
				} `json:"timelineItems"`
			} `json:"issue"`
		} `json:"repository"`
	}
	err = c.graphql(ctx, linkedPRQuery, map[string]any{"owner": owner, "name": name, "number": issueNumber}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Repository.Issue == nil {
		return nil, nil
	}
	var fallback *prRef
	for _, n := range resp.Repository.Issue.TimelineItems.Nodes {
		ref := n.Source
		if ref == nil {
			ref = n.Subject
		}
		if ref == nil || ref.Number == 0 {
			continue
		}
		if ref.State == "OPEN" {
			return &PullRequest{Number: ref.Number, State: ref.State, Merged: ref.Merged, HeadRefName: ref.HeadRefName}, nil
		}
		fallback = ref
	}
	if fallback == nil {
		return nil, nil
	}
	return &PullRequest{Number: fallback.Number, State: fallback.State, Merged: fallback.Merged, HeadRefName: fallback.HeadRefName}, nil
}

// ListIssuesUpdatedSince はリコンサイル用に、更新された Issue を取得する。
func (c *Client) ListIssuesUpdatedSince(ctx context.Context, repo string, since time.Time) ([]Issue, error) {
	path := fmt.Sprintf("/repos/%s/issues?state=all&sort=updated&direction=desc&per_page=100&since=%s",
		repo, url.QueryEscape(since.UTC().Format(time.RFC3339)))
	var out []Issue
	if err := c.rest(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func splitRepo(repo string) (owner, name string, err error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("リポジトリ指定が不正: %q（owner/name 形式）", repo)
	}
	return parts[0], parts[1], nil
}
