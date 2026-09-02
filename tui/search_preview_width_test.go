package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

// assertVisibleWidthAtMost fails when any line in output exceeds maxWidth
// after ANSI escape sequences are stripped. Used by the session-4 narrow
// width regression tests for search, preview, and path renderers.
func assertVisibleWidthAtMost(t *testing.T, output string, maxWidth int) {
	t.Helper()
	for i, line := range strings.Split(output, "\n") {
		if w := layout.StyledWidth(line); w > maxWidth {
			t.Fatalf("line %d visible width = %d > max %d: %q", i, w, maxWidth, line)
		}
	}
}

// narrowFixtureMeta seeds a SessionMeta useful for narrow-width tests: a long
// project name and snippet that would push the row past a tight viewport
// unless the renderer trims them.
func narrowFixtureMeta(project string, snippet string) transcript.SessionMeta {
	return transcript.SessionMeta{
		ID:           "narrow-" + project,
		Harness:      harness.Claude,
		Profile:      "work",
		Project:      project,
		MessageCount: 3,
		Snippet:      snippet,
		LastAt:       time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
	}
}

func TestFormatSessionRowNarrowWidthStaysWithinBounds(t *testing.T) {
	cases := []struct {
		name    string
		project string
		preview string
		matches int
		width   int
	}{
		{name: "long preview at 40 cells", project: "api", preview: strings.Repeat("alpha beta gamma ", 10), matches: 2, width: 40},
		{name: "no preview at 30 cells", project: "api", preview: "", matches: 0, width: 30},
		{name: "long project clipped at 50 cells", project: "a-very-long-project-name-that-overflows", preview: "short", matches: 1, width: 50},
		{name: "emoji project at 60 cells", project: "🗣commscoach", preview: "transcript preview body text", matches: 3, width: 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := narrowFixtureMeta(tc.project, tc.preview)
			row := formatSessionRow(transcript.Result{Meta: meta, Preview: tc.preview, Matches: tc.matches}, map[string]string{}, tc.width)
			if w := layout.StyledWidth(row); w > tc.width {
				t.Fatalf("formatSessionRow visible width = %d > max %d: %q", w, tc.width, row)
			}
		})
	}
}

func TestFormatSessionRowEmojiProjectReservesIconColumn(t *testing.T) {
	meta := narrowFixtureMeta("🗣commscoach", "snippet")
	row := stripAnsi(formatSessionRow(transcript.Result{Meta: meta, Preview: "snippet", Matches: 1}, map[string]string{}, 120))
	if !strings.Contains(row, "🗣commscoach") {
		t.Fatalf("session-search row should keep title icon tight, got %q", row)
	}
}

func TestFormatPlanRowNarrowWidthStaysWithinBounds(t *testing.T) {
	cases := []struct {
		name    string
		planRes plans.Result
		width   int
	}{
		{
			name: "long preview at 40 cells",
			planRes: plans.Result{
				Meta:    plans.PlanMeta{Name: "alpha", Files: []string{"manifest.md", "session-1.md"}, ModTime: time.Now()},
				Matches: 4,
				Preview: strings.Repeat("alpha beta gamma ", 10),
			},
			width: 40,
		},
		{
			name: "no preview at 30 cells",
			planRes: plans.Result{
				Meta:    plans.PlanMeta{Name: "beta", Files: []string{"manifest.md"}, ModTime: time.Now()},
				Matches: 0,
				Preview: "",
			},
			width: 30,
		},
		{
			name: "long name clipped at 50 cells",
			planRes: plans.Result{
				Meta:    plans.PlanMeta{Name: "an-overly-long-plan-name-that-spills", Files: []string{"a.md", "b.md", "c.md"}, ModTime: time.Now()},
				Matches: 2,
				Preview: "preview line",
			},
			width: 50,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := formatPlanRow(tc.planRes, tc.width)
			if w := layout.StyledWidth(row); w > tc.width {
				t.Fatalf("formatPlanRow visible width = %d > max %d: %q", w, tc.width, row)
			}
		})
	}
}

func TestSearchRowsStyledTruncationPreservesAnsi(t *testing.T) {
	sessionRow := formatSessionRow(transcript.Result{
		Meta:    narrowFixtureMeta("api", strings.Repeat("alpha ", 20)),
		Preview: strings.Repeat("alpha ", 20),
		Matches: 3,
	}, map[string]string{"work": "blue"}, 24)
	assertStyledTruncatedLine(t, "session row", sessionRow, 24)

	planRow := formatPlanRow(plans.Result{
		Meta:    plans.PlanMeta{Name: "api", Files: []string{"manifest.md", "session-1.md"}, ModTime: time.Now()},
		Matches: 3,
		Preview: strings.Repeat("alpha ", 20),
	}, 24)
	assertStyledTruncatedLine(t, "plan row", planRow, 24)
}

// lastANSIResetIndex reports where the final SGR reset sits in a rendered
// line. It is a private copy of the helper that moved with the display-width
// tests into tui/layout: the assertion below is about the root's own styled
// rendering, and one three-line test helper is cheaper to duplicate than an
// exported test seam on a layout leaf.
func lastANSIResetIndex(s string) int {
	reset := strings.LastIndex(s, "\x1b[m")
	if alt := strings.LastIndex(s, "\x1b[0m"); alt > reset {
		reset = alt
	}
	return reset
}

func assertStyledTruncatedLine(t *testing.T, label, line string, maxWidth int) {
	t.Helper()
	if !utf8.ValidString(line) {
		t.Fatalf("%s produced invalid UTF-8: %q", label, line)
	}
	if !strings.Contains(line, "\x1b[") {
		t.Fatalf("%s should preserve ANSI styling, got %q", label, line)
	}
	if w := layout.StyledWidth(line); w > maxWidth {
		t.Fatalf("%s visible width = %d > max %d: %q", label, w, maxWidth, line)
	}
	if !strings.Contains(stripAnsi(line), "…") {
		t.Fatalf("%s should be visibly truncated, got %q", label, line)
	}
	if reset := lastANSIResetIndex(line); reset < strings.LastIndex(line, "…") {
		t.Fatalf("%s should preserve a reset after the truncation tail, got %q", label, line)
	}
}

func TestRenderSessionSearchLinesStayWithinWidth(t *testing.T) {
	results := []transcript.Result{
		{Meta: narrowFixtureMeta("api", "alpha beta gamma delta"), Preview: strings.Repeat("alpha beta ", 12), Matches: 2},
		{Meta: narrowFixtureMeta("🗣commscoach", "voice notes from the team meeting"), Preview: "voice notes from the team meeting", Matches: 1},
		{Meta: narrowFixtureMeta("dotfiles", "config drift again"), Preview: "config drift again", Matches: 0},
	}
	// 60 is the smallest width at which the static chip bar
	// "[h] harness: any   [p] profile: any   [r] project: any" (50 cells)
	// fits on a single line; chip-bar wrapping is out of scope here.
	for _, width := range []int{60, 80, 120} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			m := Model{
				mode:           ModeSessionSearch,
				width:          width,
				height:         30,
				cfg:            &config.Config{},
				sessionResults: results,
			}
			assertVisibleWidthAtMost(t, renderSessionSearch(m), width)
		})
	}
}

func TestRenderPlanSearchLinesStayWithinWidth(t *testing.T) {
	results := []plans.Result{
		{
			Meta:    plans.PlanMeta{Name: "alpha", DirPath: "/tmp/alpha-aabb112233cc", Files: []string{"manifest.md", "session-1.md"}, ModTime: time.Now()},
			Matches: 3,
			Preview: strings.Repeat("needle haystack ", 10),
		},
		{
			Meta:    plans.PlanMeta{Name: "beta", DirPath: "/tmp/beta-ccdd445566ee", Files: []string{"manifest.md"}, ModTime: time.Now()},
			Matches: 0,
			Preview: "no match preview ignored",
		},
	}
	for _, width := range []int{40, 60, 100} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			m := Model{
				mode:        ModePlanSearch,
				width:       width,
				height:      30,
				cfg:         &config.Config{},
				planResults: results,
			}
			assertVisibleWidthAtMost(t, renderPlanSearch(m), width)
		})
	}
}

func TestRenderPreviewLinesStayWithinWidth(t *testing.T) {
	sess := session.RunningSession{
		WindowID: 7,
		Base:     "a-very-long-project-name-with-noise",
		Project:  "a-very-long-project-name-with-noise",
		Profile:  "work",
		Harness:  harness.Claude,
	}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Color: "blue"}}}
	acctColor := map[string]string{"work": "blue"}
	text := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa\n", 8)

	for _, width := range []int{20, 40, 80} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			out := renderPreview(width, 6, sess, text, "", acctColor, cfg)
			assertVisibleWidthAtMost(t, out, width)
		})
	}

	t.Run("error path", func(t *testing.T) {
		errText := strings.Repeat("a very long error message that should be clipped ", 3)
		out := renderPreview(30, 4, sess, "", errText, acctColor, cfg)
		assertVisibleWidthAtMost(t, out, 30)
	})

	t.Run("not running placeholder", func(t *testing.T) {
		idle := session.RunningSession{Base: "idle", Project: "idle"}
		out := renderPreview(20, 3, idle, "", "", acctColor, cfg)
		assertVisibleWidthAtMost(t, out, 20)
	})
}

func TestRenderPreviewEmojiTitleReservesIconColumn(t *testing.T) {
	emojiSess := session.RunningSession{
		WindowID: 1,
		Base:     "🗣commscoach",
		Project:  "🗣commscoach",
		Profile:  "work",
		Harness:  harness.Claude,
	}
	plainSess := session.RunningSession{
		WindowID: 2,
		Base:     "Foocoach",
		Project:  "Foocoach",
		Profile:  "work",
		Harness:  harness.Claude,
	}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Color: "blue"}}}
	acctColor := map[string]string{"work": "blue"}

	const width = 40
	emojiHeader := strings.SplitN(stripAnsi(renderPreview(width, 1, emojiSess, "", "", acctColor, cfg)), "\n", 2)[0]
	plainHeader := strings.SplitN(stripAnsi(renderPreview(width, 1, plainSess, "", "", acctColor, cfg)), "\n", 2)[0]

	// The emoji "🗣" must reserve a 2-cell column without gaining a phantom
	// space before the title body. Compare the two headers' "body after the
	// icon column" segments.
	emojiBody := emojiHeader[len("🗣"):]
	plainBody := plainHeader[2:] // skip the first 2 ASCII chars to reach the rest
	if !strings.HasPrefix(emojiBody, "commscoach") {
		t.Fatalf("expected emoji preview header to keep title body tight, got %q", emojiHeader)
	}
	if !strings.HasPrefix(plainBody, "ocoach") {
		t.Fatalf("expected plain ASCII header to render without padding, got %q", plainHeader)
	}
}
