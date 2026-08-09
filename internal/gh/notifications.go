package gh

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Notification は通知スレッド 1 件。
type Notification struct {
	ID         string    `json:"id"`
	Reason     string    `json:"reason"`
	UpdatedAt  time.Time `json:"updated_at"`
	Unread     bool      `json:"unread"`
	Subject    Subject   `json:"subject"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

// Subject は通知の対象。
type Subject struct {
	Title            string `json:"title"`
	URL              string `json:"url"`
	LatestCommentURL string `json:"latest_comment_url"`
	Type             string `json:"type"` // Issue / PullRequest / ...
}

// Repo は "owner/name" を返す。
func (n Notification) Repo() string {
	return n.Repository.Owner.Login + "/" + n.Repository.Name
}

var subjectNumberRe = regexp.MustCompile(`/(?:issues|pulls)/(\d+)$`)

// Number は subject.url から Issue / PR 番号を取り出す。取れない場合は 0。
func (n Notification) Number() int {
	m := subjectNumberRe.FindStringSubmatch(n.Subject.URL)
	if m == nil {
		return 0
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return v
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// ListNotifications は since 以降の通知を取得する。
//
// 既読/未読を状態として使わないため、常に all=true で取得しカーソル（since）だけを信じる。
// 人間が Web UI で既読にしてもイベントを取りこぼさない。
// 第 2 戻り値はサーバが指定するポーリング間隔（X-Poll-Interval）。
func (c *Client) ListNotifications(ctx context.Context, since time.Time) ([]Notification, time.Duration, error) {
	path := "/notifications?all=true&per_page=50"
	if !since.IsZero() {
		path += "&since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}

	var all []Notification
	var pollInterval time.Duration
	next := path
	for next != "" {
		var page []Notification
		hdr, err := c.restWithHeader(ctx, http.MethodGet, next, nil, &page)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, page...)
		if pollInterval == 0 {
			if v := hdr.Get("X-Poll-Interval"); v != "" {
				if secs, err := strconv.Atoi(v); err == nil {
					pollInterval = time.Duration(secs) * time.Second
				}
			}
		}
		next = ""
		if m := linkNextRe.FindStringSubmatch(hdr.Get("Link")); m != nil {
			next = m[1]
		}
		// 無限ページングの保険。
		if len(all) > 500 {
			break
		}
	}
	return all, pollInterval, nil
}

// IsIssueLike は Issue / PullRequest スレッドかどうかを返す。
func (n Notification) IsIssueLike() bool {
	return n.Subject.Type == "Issue" || n.Subject.Type == "PullRequest"
}

// IsPullRequest は PR スレッドかどうかを返す。
func (n Notification) IsPullRequest() bool {
	return n.Subject.Type == "PullRequest" || strings.Contains(n.Subject.URL, "/pulls/")
}
