package transcript

import "testing"

func TestShortenPaths(t *testing.T) {
	const home = "/Users/me"
	for _, tc := range []struct{ in, want string }{
		{
			"/asl CLAUDE_CONFIG_DIR=~/.claude-personal run /Users/me/.local/share/agent-plans/4f4e36c66116-93448e2a42c6/ --harness claude",
			"/asl CLAUDE_CONFIG_DIR=~/.claude-personal run …/4f4e36c66116-93448e2a42c6/ --harness claude",
		},
		{"the baseline is /Users/me/Downloads/report.html", "the baseline is ~/Downloads/report.html"},
		{"read /usr/local/share/doc/README now", "read …/README now"},
		{"PLAN=/Users/me/plans/a/b go", "PLAN=…/b go"},
		{"/Users/me", "~"},
		{"see https://example.com/a/b/c/d", "see https://example.com/a/b/c/d"},
		{"a//b //host/x/y/z", "a//b //host/x/y/z"},
		{"/clear", "/clear"},
		{"a=b c", "a=b c"},
		{"", ""},
	} {
		if got := ShortenPaths(tc.in, home); got != tc.want {
			t.Errorf("ShortenPaths(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ShortenPaths("/Users/me/a/b", ""); got != "…/b" {
		t.Errorf("without a home, ShortenPaths = %q, want …/b", got)
	}
}
