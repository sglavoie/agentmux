package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestStateGroupClassification(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute

	cases := []struct {
		name     string
		state    string
		windowID int
		activity map[int]time.Time
		want     int
	}{
		{
			name:     "key state always attention regardless of activity",
			state:    "key",
			windowID: 1,
			activity: map[int]time.Time{1: now.Add(-time.Hour)}, // stale
			want:     0,
		},
		{
			name:     "stop state always attention",
			state:    "stop",
			windowID: 2,
			want:     0,
		},
		{
			name:     "bolt state always active",
			state:    "bolt",
			windowID: 3,
			activity: map[int]time.Time{3: now.Add(-time.Hour)}, // ignored
			want:     1,
		},
		{
			name:     "empty state with recent activity is Active",
			state:    "",
			windowID: 4,
			activity: map[int]time.Time{4: now.Add(-time.Minute)},
			want:     1,
		},
		{
			name:     "empty state with stale activity is Idle",
			state:    "",
			windowID: 5,
			activity: map[int]time.Time{5: now.Add(-time.Hour)},
			want:     2,
		},
		{
			name:     "empty state with no activity record is Active",
			state:    "",
			windowID: 6,
			activity: nil,
			want:     1,
		},
		{
			name:     "empty state, nil map, no record for this window is Active",
			state:    "",
			windowID: 7,
			activity: map[int]time.Time{99: now}, // record for different window
			want:     1,
		},
		{
			name:     "error state classified as Active",
			state:    "error",
			windowID: 8,
			want:     1,
		},
		{
			// Regression: subagent (🤖) used to fall into the "unknown" branch
			// and end up in Idle, hiding actively-running sessions.
			name:     "subagent state classified as Active",
			state:    "subagent",
			windowID: 9,
			want:     1,
		},
		{
			// Regression: compact (🌀) used to fall into the "unknown" branch
			// and end up in Idle, hiding sessions mid-context-compaction.
			name:     "compact state classified as Active",
			state:    "compact",
			windowID: 10,
			want:     1,
		},
		{
			// Regression: question (❓) is an elicitation prompt and the user
			// must respond — it belongs in Needs Attention, not Idle.
			name:     "question state classified as Needs Attention",
			state:    "question",
			windowID: 11,
			want:     0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := session.RunningSession{State: tc.state, WindowID: tc.windowID}
			got := stateGroup(s, tc.activity, threshold)
			if got != tc.want {
				t.Errorf("stateGroup(%q, win=%d) = %d, want %d", tc.state, tc.windowID, got, tc.want)
			}
		})
	}
}

func TestStateGroupZeroThreshold(t *testing.T) {
	// A zero threshold disables the staleness check; empty-state sessions
	// with any activity record should still classify as Active.
	s := session.RunningSession{State: "", WindowID: 1}
	activity := map[int]time.Time{1: time.Now().Add(-time.Hour)}
	got := stateGroup(s, activity, 0)
	if got != 1 {
		t.Errorf("expected Active (1) with zero threshold, got %d", got)
	}
}

// ansiRE strips ANSI CSI escape sequences from a Lipgloss-rendered row so
// we can compare visual offsets independent of styling.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// terminalWidth approximates how many cells a string consumes when
// printed to a real terminal. go-runewidth treats U+FE0F as zero-width
// even though it forces emoji presentation (2 cells) on the preceding
// base character, so we add 1 per variation selector to compensate.
func terminalWidth(s string) int {
	w := runewidth.StringWidth(s)
	for _, r := range s {
		if r == '️' {
			w++
		}
	}
	return w
}

// idxBadge returns the visual cell offset of the first '[' character (the
// account badge opener) in a rendered row. Returns -1 if no '[' is found.
func idxBadge(row string) int {
	plain := ansiRE.ReplaceAllString(row, "")
	i := strings.IndexByte(plain, '[')
	if i < 0 {
		return -1
	}
	return terminalWidth(plain[:i])
}

// stripAnsi removes ANSI CSI escape sequences from a Lipgloss-rendered
// string so tests can assert against the visible content.
func stripAnsi(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// TestRenderListEmptyHeroNoSessions covers the case where no sessions are
// running at all: the hero should advertise the `l` launch shortcut.
func TestRenderListEmptyHeroNoSessions(t *testing.T) {
	m := Model{width: 60}
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "No running sessions.") {
		t.Errorf("expected title; got %q", out)
	}
	if !strings.Contains(out, "press  l  to launch") {
		t.Errorf("expected launch hint; got %q", out)
	}
	if strings.Contains(out, "esc") {
		t.Errorf("zero-running hint should not mention esc; got %q", out)
	}
}

// TestRenderListEmptyHeroFilteredOut covers the case where sessions exist
// but the active filter rules every one of them out: the hero should hint
// at `esc` to clear the filter.
func TestRenderListEmptyHeroFilteredOut(t *testing.T) {
	m := Model{
		width:        60,
		sessions:     []session.RunningSession{{Base: "x", State: "bolt"}},
		filtered:     nil,
		activeFilter: Parse("nomatch"),
	}
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "No sessions match the active filter.") {
		t.Errorf("expected filtered-out title; got %q", out)
	}
	if !strings.Contains(out, "press  esc  to clear filters") {
		t.Errorf("expected esc hint; got %q", out)
	}
}

// TestRenderListMixedGroupBorders covers a list with two state groups
// (Needs Attention and Active) and asserts each row carries the left
// border glyph and that group labels are emitted above each band.
func TestRenderListMixedGroupBorders(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{Base: "a", State: "key", Account: "w"},
			{Base: "b", State: "bolt", Account: "w"},
		},
	}
	m.filtered = m.sessions
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "Needs Attention (1)") {
		t.Errorf("expected attention label; got %q", out)
	}
	if !strings.Contains(out, "Active (1)") {
		t.Errorf("expected active label; got %q", out)
	}
	if !strings.Contains(out, groupBorderGlyph) {
		t.Errorf("expected border glyph %q in output %q", groupBorderGlyph, out)
	}
}

// TestRenderListSingleGroupNoBorder asserts that when every row falls into
// the same state group, neither the group label nor the left border is
// drawn — the visual chrome is reserved for multi-group lists.
func TestRenderListSingleGroupNoBorder(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{Base: "a", State: "bolt", Account: "w"},
			{Base: "b", State: "bolt", Account: "w"},
		},
	}
	m.filtered = m.sessions
	out := stripAnsi(renderList(m))
	if strings.Contains(out, "Active (") {
		t.Errorf("single-group list should not render label; got %q", out)
	}
	if strings.Contains(out, groupBorderGlyph) {
		t.Errorf("single-group list should not render border; got %q", out)
	}
}

// TestFormatRowIconColumnAlignment locks in that a row carrying the ⚠️
// failure icon aligns its account badge at the same visual column as a
// row carrying the ⚡ bolt icon. Without iconDisplayWidth's U+FE0F
// handling, ⚠️ would be measured as 1 cell and over-padded, shifting
// every following column right by one.
func TestFormatRowIconColumnAlignment(t *testing.T) {
	sBolt := session.RunningSession{State: "bolt", Account: "p", Base: "x"}
	sErr := session.RunningSession{State: "error", Account: "p", Base: "x"}
	rowBolt := formatRow(sBolt, nil, 0, "")
	rowErr := formatRow(sErr, nil, 0, "")

	iBolt := idxBadge(rowBolt)
	iErr := idxBadge(rowErr)
	if iBolt < 0 || iErr < 0 {
		t.Fatalf("badge '[' not found: bolt=%d err=%d (rows: %q / %q)", iBolt, iErr, rowBolt, rowErr)
	}
	if iBolt != iErr {
		t.Errorf("badge column misaligned: bolt=%d, error=%d", iBolt, iErr)
	}
}

// TestFormatRowHarnessBadge asserts that [cl] appears for claude sessions,
// [cp] for copilot sessions, and nothing harness-related for empty harness.
func TestFormatRowHarnessBadge(t *testing.T) {
	sClaude := session.RunningSession{Base: "W:api", Account: "work", Harness: "claude"}
	sCopilot := session.RunningSession{Base: "W:api", Account: "work", Harness: "copilot"}
	sNone := session.RunningSession{Base: "W:api", Account: "work", Harness: ""}

	rowClaude := stripAnsi(formatRow(sClaude, nil, 0, ""))
	rowCopilot := stripAnsi(formatRow(sCopilot, nil, 0, ""))
	rowNone := stripAnsi(formatRow(sNone, nil, 0, ""))

	if !strings.Contains(rowClaude, "[cl]") {
		t.Errorf("expected [cl] badge for claude harness; got %q", rowClaude)
	}
	if strings.Contains(rowClaude, "[cp]") {
		t.Errorf("unexpected [cp] badge in claude row; got %q", rowClaude)
	}
	if !strings.Contains(rowCopilot, "[cp]") {
		t.Errorf("expected [cp] badge for copilot harness; got %q", rowCopilot)
	}
	if strings.Contains(rowCopilot, "[cl]") {
		t.Errorf("unexpected [cl] badge in copilot row; got %q", rowCopilot)
	}
	if strings.Contains(rowNone, "[cl]") || strings.Contains(rowNone, "[cp]") {
		t.Errorf("expected no harness badge for empty harness; got %q", rowNone)
	}
}

// TestFormatRowAcctBadgeUnicode pins the contract that multibyte account
// names produce a valid UTF-8 badge taken from the first rune, not a
// truncated invalid byte that lipgloss would render as a replacement glyph.
func TestFormatRowAcctBadgeUnicode(t *testing.T) {
	cases := []struct {
		account string
		want    string
	}{
		{"éscriba", "[É]"},
		{"中文", "[中]"},
		{"🐙ctopus", "[🐙]"},
	}
	for _, tc := range cases {
		s := session.RunningSession{Base: "x", Account: tc.account}
		row := stripAnsi(formatRow(s, nil, 0, ""))
		if !strings.Contains(row, tc.want) {
			t.Errorf("account %q: row %q does not contain expected badge %q", tc.account, row, tc.want)
		}
		if !utf8.ValidString(row) {
			t.Errorf("account %q: rendered row is not valid UTF-8: %q", tc.account, row)
		}
	}
}

// TestRenderLegendVisible asserts the legend strip renders icon→meaning
// tokens for every known state when the terminal is tall enough.
func TestRenderLegendVisible(t *testing.T) {
	m := Model{width: 80, height: 30}
	out := stripAnsi(renderLegend(m))
	if !strings.Contains(out, "icons:") {
		t.Errorf("expected legend prefix; got %q", out)
	}
	for _, state := range legendStates {
		gloss := StateGloss(state)
		if gloss == "" {
			continue
		}
		if !strings.Contains(out, gloss) {
			t.Errorf("expected gloss %q in legend; got %q", gloss, out)
		}
	}
}

// TestRenderLegendHiddenWhenShort asserts the legend silently disappears
// when the terminal lacks enough vertical room to spare a line.
func TestRenderLegendHiddenWhenShort(t *testing.T) {
	m := Model{width: 80, height: legendMinHeight - 1}
	if got := renderLegend(m); got != "" {
		t.Errorf("expected empty legend for short terminal, got %q", got)
	}
}
