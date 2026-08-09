package gh

import "testing"

func TestNotificationNumber(t *testing.T) {
	cases := []struct {
		url  string
		want int
	}{
		{"https://api.github.com/repos/o/r/issues/12", 12},
		{"https://api.github.com/repos/o/r/pulls/345", 345},
		{"https://api.github.com/repos/o/r/releases/1", 0},
		{"", 0},
	}
	for _, c := range cases {
		n := Notification{Subject: Subject{URL: c.url}}
		if got := n.Number(); got != c.want {
			t.Errorf("Number(%q) = %d, want %d", c.url, got, c.want)
		}
	}
}

func TestNotificationIsPullRequest(t *testing.T) {
	pr := Notification{Subject: Subject{Type: "PullRequest", URL: "https://api.github.com/repos/o/r/pulls/1"}}
	if !pr.IsPullRequest() {
		t.Error("PR スレッドを判定できていません")
	}
	is := Notification{Subject: Subject{Type: "Issue", URL: "https://api.github.com/repos/o/r/issues/1"}}
	if is.IsPullRequest() {
		t.Error("Issue を PR と誤判定しています")
	}
	if !is.IsIssueLike() || !pr.IsIssueLike() {
		t.Error("IsIssueLike が false になっています")
	}
}

func TestUserIsBot(t *testing.T) {
	if !(User{Login: "foo[bot]", Type: "User"}).IsBot() {
		t.Error("[bot] サフィックスを判定できていません")
	}
	if !(User{Login: "foo", Type: "Bot"}).IsBot() {
		t.Error("Type=Bot を判定できていません")
	}
	if (User{Login: "k-wa-wa", Type: "User"}).IsBot() {
		t.Error("人間を bot と誤判定しています")
	}
}

func TestLinkNextParsing(t *testing.T) {
	link := `<https://api.github.com/notifications?page=2>; rel="next", <https://api.github.com/notifications?page=5>; rel="last"`
	m := linkNextRe.FindStringSubmatch(link)
	if m == nil || m[1] != "https://api.github.com/notifications?page=2" {
		t.Errorf("next リンクを取り出せていません: %v", m)
	}
	if linkNextRe.FindStringSubmatch(`<https://x>; rel="last"`) != nil {
		t.Error("next が無いのに一致しています")
	}
}

func TestCIStatusMapping(t *testing.T) {
	cases := map[string]CIStatus{
		"SUCCESS":  CISuccess,
		"":         CISuccess, // CI 未設定は成功扱い
		"FAILURE":  CIFailure,
		"ERROR":    CIFailure,
		"PENDING":  CIPending,
		"EXPECTED": CIPending,
	}
	for state, want := range cases {
		pr := &PullRequest{CheckState: state}
		if got := pr.CI(); got != want {
			t.Errorf("CheckState=%q -> %v, want %v", state, got, want)
		}
	}
}

func TestSplitRepo(t *testing.T) {
	o, n, err := splitRepo("k-wa-wa/nuage")
	if err != nil || o != "k-wa-wa" || n != "nuage" {
		t.Errorf("splitRepo = (%q, %q, %v)", o, n, err)
	}
	if _, _, err := splitRepo("invalid"); err == nil {
		t.Error("不正な形式がエラーになりません")
	}
}
