package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/k-wa-wa/nuage-autopilot2/internal/gh"
)

func TestRetryFindPRSucceedsImmediately(t *testing.T) {
	calls := 0
	pr, err := retryFindPR(context.Background(), 3, time.Millisecond, func(context.Context) (*gh.PullRequest, error) {
		calls++
		return &gh.PullRequest{Number: 7}, nil
	})
	if err != nil || pr == nil || pr.Number != 7 {
		t.Fatalf("pr=%v err=%v", pr, err)
	}
	if calls != 1 {
		t.Errorf("見つかっているのに %d 回呼ばれています", calls)
	}
}

// PR 作成直後の timeline 反映ラグを模したケース。
func TestRetryFindPRRecoversFromLag(t *testing.T) {
	calls := 0
	pr, err := retryFindPR(context.Background(), 3, time.Millisecond, func(context.Context) (*gh.PullRequest, error) {
		calls++
		if calls < 3 {
			return nil, nil // まだ timeline に現れていない
		}
		return &gh.PullRequest{Number: 9, HeadRefName: "feat/x"}, nil
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if pr == nil || pr.Number != 9 {
		t.Fatalf("ラグから復帰できていません: %v", pr)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// 全試行で見つからない場合は (nil, nil)。呼び出し側が「本当に PR が無い」と判断できる。
func TestRetryFindPRGivesUp(t *testing.T) {
	calls := 0
	pr, err := retryFindPR(context.Background(), 3, time.Millisecond, func(context.Context) (*gh.PullRequest, error) {
		calls++
		return nil, nil
	})
	if pr != nil || err != nil {
		t.Fatalf("pr=%v err=%v, want (nil, nil)", pr, err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// 一時的な API エラーもラグと同様に吸収する。
func TestRetryFindPRRetriesAfterError(t *testing.T) {
	calls := 0
	pr, err := retryFindPR(context.Background(), 3, time.Millisecond, func(context.Context) (*gh.PullRequest, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("502 Bad Gateway")
		}
		return &gh.PullRequest{Number: 1}, nil
	})
	if err != nil || pr == nil {
		t.Fatalf("エラーから復帰できていません: pr=%v err=%v", pr, err)
	}
}

// 全試行がエラーなら最後のエラーを返す（PR 不在と区別できるようにする）。
func TestRetryFindPRReturnsLastError(t *testing.T) {
	pr, err := retryFindPR(context.Background(), 2, time.Millisecond, func(context.Context) (*gh.PullRequest, error) {
		return nil, errors.New("boom")
	})
	if pr != nil {
		t.Errorf("pr = %v, want nil", pr)
	}
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestRetryFindPRHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	start := time.Now()
	_, err := retryFindPR(ctx, 3, time.Hour, func(context.Context) (*gh.PullRequest, error) {
		calls++
		cancel() // 1 回目の失敗後、backoff 待ちに入る前にキャンセルする
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("backoff の待機を中断できていません: %v", elapsed)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
