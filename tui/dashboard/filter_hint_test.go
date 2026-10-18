package dashboard

import (
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
	"strings"
	"testing"
)

func TestFilterHintsExplainIgnoredTokensWithoutChangingMatching(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"!attn", "try !attention"}, {"!nonsense", "not applied"},
		{`#"two words`, "close the quote"}, {`#"two words"oops`, "separate it"},
		{"!attention", ""}, {"!KEY", ""}, {"!", ""}, {`\!attn`, ""}, {`#"two words"`, ""},
	} {
		got := FilterHint(tc.input)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%q: %q, want %q", tc.input, got, tc.want)
		}
	}
	row := session.RunningSession{Base: "api", State: "bolt"}
	if !Parse("!attn").Match(row, nil, 0) {
		t.Fatal("hint must preserve forgiving matching")
	}
	for _, width := range []int{32, 80, 120} {
		rendered := RenderChipsWithHits(Parse(`!attn`), nil, width)
		for _, line := range strings.Split(rendered.Text, "\n") {
			if layout.StyledWidth(line) > width {
				t.Fatalf("hint overflow at %d: %s", width, line)
			}
		}
		if len(rendered.Hits) != 1 || rendered.Hits[0].Rect.Y != 0 {
			t.Fatal("hint must not move chip hit targets")
		}
		if RenderChips(Parse(`#"unfinished`), nil, width) == "" {
			t.Fatal("an incomplete project must explain why it is not applied")
		}
	}
}
