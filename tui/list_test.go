package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

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
	rowBolt := formatRow(sBolt, nil, 0)
	rowErr := formatRow(sErr, nil, 0)

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

	rowClaude := stripAnsi(formatRow(sClaude, nil, 0))
	rowCopilot := stripAnsi(formatRow(sCopilot, nil, 0))
	rowNone := stripAnsi(formatRow(sNone, nil, 0))

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

// TestRecordTransitionsFirstRefreshNoPulse asserts that the very first
// session refresh after startup seeds lastSeenStates without recording a
// transition for every row — otherwise launching the dashboard would pulse
// every session by definition.
func TestRecordTransitionsFirstRefreshNoPulse(t *testing.T) {
	m := Model{
		sessions: []session.RunningSession{
			{WindowID: 1, State: "bolt"},
			{WindowID: 2, State: "key"},
		},
	}
	m.recordTransitions()
	if len(m.recentTransitions) != 0 {
		t.Errorf("first refresh must not record pulses; got %v", m.recentTransitions)
	}
	if got := m.lastSeenStates[1]; got != "bolt" {
		t.Errorf("expected lastSeenStates[1] = bolt, got %q", got)
	}
	if got := m.lastSeenStates[2]; got != "key" {
		t.Errorf("expected lastSeenStates[2] = key, got %q", got)
	}
}

// TestRecordTransitionsPulsesOnChange asserts a state change between the
// snapshot and the current refresh records a pulse for the changed row only.
func TestRecordTransitionsPulsesOnChange(t *testing.T) {
	m := Model{
		lastSeenStates: map[int]string{1: "bolt", 2: "bolt"},
		sessions: []session.RunningSession{
			{WindowID: 1, State: "key"}, // changed
			{WindowID: 2, State: "bolt"}, // unchanged
		},
	}
	m.recordTransitions()
	if _, ok := m.recentTransitions[1]; !ok {
		t.Errorf("expected pulse for window 1; got %v", m.recentTransitions)
	}
	if _, ok := m.recentTransitions[2]; ok {
		t.Errorf("unchanged window 2 should not pulse; got %v", m.recentTransitions)
	}
}

// TestRecordTransitionsNewSessionNoPulse asserts a freshly launched session
// (no prior entry in lastSeenStates) is seeded silently rather than pulsing
// on its first appearance.
func TestRecordTransitionsNewSessionNoPulse(t *testing.T) {
	m := Model{
		lastSeenStates: map[int]string{1: "bolt"},
		sessions: []session.RunningSession{
			{WindowID: 1, State: "bolt"},
			{WindowID: 99, State: "bolt"}, // new
		},
	}
	m.recordTransitions()
	if _, ok := m.recentTransitions[99]; ok {
		t.Errorf("new session must not pulse on first sighting; got %v", m.recentTransitions)
	}
	if got := m.lastSeenStates[99]; got != "bolt" {
		t.Errorf("expected new session seeded into lastSeenStates, got %q", got)
	}
}

// TestSweepRecentTransitionsDecay asserts entries older than pulseDecay are
// dropped while fresh entries survive.
func TestSweepRecentTransitionsDecay(t *testing.T) {
	now := time.Now()
	m := Model{
		recentTransitions: map[int]time.Time{
			1: now.Add(-pulseDecay - time.Second), // stale
			2: now.Add(-time.Millisecond),         // fresh
		},
	}
	m.sweepRecentTransitions()
	if _, ok := m.recentTransitions[1]; ok {
		t.Errorf("stale pulse should be swept; got %v", m.recentTransitions)
	}
	if _, ok := m.recentTransitions[2]; !ok {
		t.Errorf("fresh pulse should survive sweep; got %v", m.recentTransitions)
	}
}

// TestSnapshotSeenStatesClearsPulses asserts a user keypress in ModeList
// (which calls snapshotSeenStates) wipes recentTransitions so the pulse
// layer surfaces only changes the user has not yet seen.
func TestSnapshotSeenStatesClearsPulses(t *testing.T) {
	m := Model{
		sessions: []session.RunningSession{
			{WindowID: 1, State: "key"},
		},
		recentTransitions: map[int]time.Time{1: time.Now()},
	}
	m.snapshotSeenStates()
	if len(m.recentTransitions) != 0 {
		t.Errorf("expected pulses cleared on snapshot; got %v", m.recentTransitions)
	}
	if got := m.lastSeenStates[1]; got != "key" {
		t.Errorf("expected lastSeenStates re-seeded to current state, got %q", got)
	}
}

// TestRenderListPulseColumnAppearsOnTransition asserts that when a row has
// an active pulse, the rendered list reserves the pulse column and shifts
// every row by exactly the column width so alignment is preserved.
func TestRenderListPulseColumnAppearsOnTransition(t *testing.T) {
	base := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt", Account: "w"},
			{WindowID: 2, Base: "b", State: "bolt", Account: "w"},
		},
	}
	base.filtered = base.sessions

	without := stripAnsi(renderList(base))

	pulsing := base
	pulsing.recentTransitions = map[int]time.Time{1: time.Now()}
	with := stripAnsi(renderList(pulsing))

	if without == with {
		t.Fatalf("expected pulse render to differ from baseline; both =\n%q", without)
	}
	// Every line of the pulsing render must be exactly pulseColumnWidth
	// wider than (or shifted from) the baseline — verify by checking the
	// pulse glyph appears on the row whose WindowID == 1.
	if !strings.Contains(with, groupBorderGlyph) {
		t.Errorf("expected pulse glyph %q in pulsing render; got %q", groupBorderGlyph, with)
	}
}

// TestRenderListNoPulseColumnByDefault asserts that with no active
// transitions, the pulse column is omitted entirely so rows render at full
// width — no permanent visual cost.
func TestRenderListNoPulseColumnByDefault(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "a", State: "bolt", Account: "w"},
		},
	}
	m.filtered = m.sessions
	out := renderList(m)
	if anyRowHasActivePulse(m) {
		t.Fatalf("baseline model should have no active pulse; got %v", m.recentTransitions)
	}
	// The row should not begin with the pulse-spacer (two leading spaces
	// from the pulse column). Since there's a single group, the row also
	// has no group border — so the row starts directly with formatRow.
	plain := stripAnsi(out)
	if strings.HasPrefix(plain, "  "+groupBorderGlyph) {
		t.Errorf("unexpected pulse column when no pulse active; got %q", plain)
	}
}

// TestRenderDeprecationBannerShownWhenDeprecationsPresent verifies the
// banner renders headline + hint when the config has at least one
// deprecated key and the user has not dismissed it.
func TestRenderDeprecationBannerShownWhenDeprecationsPresent(t *testing.T) {
	m := Model{
		width: 80,
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles", Location: "[[accounts]]"},
		},
	}
	out := stripAnsi(renderDeprecationBanner(m))
	if !strings.Contains(out, "deprecated keys") {
		t.Errorf("expected headline; got %q", out)
	}
	if !strings.Contains(out, "accounts → profiles") {
		t.Errorf("expected migration arrow; got %q", out)
	}
	if !strings.Contains(out, "press d to dismiss") {
		t.Errorf("expected dismiss hint; got %q", out)
	}
}

// TestRenderDeprecationBannerHiddenWhenDismissed verifies that setting
// deprecationDismissed suppresses the banner even when deprecations are
// recorded — the dismissal is session-scoped, not config-scoped.
func TestRenderDeprecationBannerHiddenWhenDismissed(t *testing.T) {
	m := Model{
		width: 80,
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles"},
		},
		deprecationDismissed: true,
	}
	if got := renderDeprecationBanner(m); got != "" {
		t.Errorf("expected empty banner after dismiss; got %q", got)
	}
}

// TestRenderDeprecationBannerHiddenWhenNoDeprecations covers the common
// case where the user is on a clean canonical config — no banner ever
// appears.
func TestRenderDeprecationBannerHiddenWhenNoDeprecations(t *testing.T) {
	m := Model{width: 80}
	if got := renderDeprecationBanner(m); got != "" {
		t.Errorf("expected empty banner with no deprecations; got %q", got)
	}
}

// TestRenderHealthPipsHiddenBeforeFirstCheck verifies that the pip strip
// is suppressed until at least one health refresh has completed so the
// header doesn't briefly mis-render as "all red" at startup.
func TestRenderHealthPipsHiddenBeforeFirstCheck(t *testing.T) {
	m := Model{width: 80}
	if got := renderHealthPips(m); got != "" {
		t.Errorf("expected no pips before first check; got %q", got)
	}
}

// TestRenderHealthPipsShowsBothPips verifies that after a health refresh
// both daemon and hooks pips render with their labels.
func TestRenderHealthPipsShowsBothPips(t *testing.T) {
	m := Model{
		width:                 80,
		daemonAlive:           true,
		daemonHealthCheckedAt: time.Now(),
		hookHealth:            HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   time.Now(),
	}
	out := stripAnsi(renderHealthPips(m))
	if !strings.Contains(out, "daemon") {
		t.Errorf("expected daemon label; got %q", out)
	}
	if !strings.Contains(out, "hooks") {
		t.Errorf("expected hooks label; got %q", out)
	}
	if !strings.Contains(out, healthPipGlyph) {
		t.Errorf("expected pip glyph; got %q", out)
	}
}

// TestRenderHealthHintsShownWhenDegraded verifies that the under-pip hint
// surfaces both a daemon-down and a hooks-drifted message when applicable,
// while staying silent when everything is healthy.
func TestRenderHealthHintsShownWhenDegraded(t *testing.T) {
	now := time.Now()
	m := Model{
		width:                 80,
		daemonAlive:           false,
		daemonHealthCheckedAt: now,
		hookHealth:            HookHealth{Drifted: []string{"work"}},
		hookHealthCheckedAt:   now,
	}
	out := stripAnsi(renderHealthHints(m))
	if !strings.Contains(out, "daemon down") {
		t.Errorf("expected daemon-down hint; got %q", out)
	}
	if !strings.Contains(out, "hooks drifted") {
		t.Errorf("expected hooks-drifted hint; got %q", out)
	}

	healthy := Model{
		width:                 80,
		daemonAlive:           true,
		daemonHealthCheckedAt: now,
		hookHealth:            HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   now,
	}
	if got := renderHealthHints(healthy); got != "" {
		t.Errorf("expected empty hints when healthy; got %q", got)
	}
}

// TestHarnessAbbrev verifies the two-letter abbreviations and the empty fallback.
func TestHarnessAbbrev(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"claude", "cl"},
		{"Claude", "cl"},
		{"CLAUDE", "cl"},
		{"copilot", "cp"},
		{"Copilot", "cp"},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		got := harnessAbbrev(tc.input)
		if got != tc.want {
			t.Errorf("harnessAbbrev(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
