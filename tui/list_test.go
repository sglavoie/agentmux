package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestStateGroupClassification(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute

	cases := []struct {
		name     string
		state    string
		unseen   bool
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
			// An unseen stop is the one the feature exists for: the turn
			// finished and nobody has looked at it yet.
			name:     "unseen stop is Needs Attention",
			state:    "stop",
			unseen:   true,
			windowID: 2,
			want:     0,
		},
		{
			// The demotion that makes Needs Attention drain. Stale activity is
			// irrelevant — seenness alone decides.
			name:     "seen stop is Idle",
			state:    "stop",
			windowID: 13,
			activity: map[int]time.Time{13: now.Add(-time.Minute)}, // fresh, ignored
			want:     2,
		},
		{
			// The unseen bit only ever means anything on stop; a non-resting
			// icon is classified by its name whatever the bit says. The daemon
			// clears it on every non-stop publish, so this pairing is defensive.
			name:     "unseen is ignored on a non-stop state",
			state:    "bolt",
			unseen:   true,
			windowID: 14,
			want:     1,
		},
		{
			// The inversion in action: no daemon behind a row leaves Unseen at
			// its zero value, and zero means seen, so the row demotes quietly
			// rather than pinning Needs Attention open.
			name:     "unknown legacy state stays Idle even when unseen",
			state:    "sparkle",
			unseen:   true,
			windowID: 15,
			want:     2,
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
			name:     "warning state classified as Active",
			state:    "warning",
			windowID: 8,
			want:     1,
		},
		{
			name:     "error state classified as Needs Attention",
			state:    "error",
			windowID: 12,
			want:     0,
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
			s := session.RunningSession{State: tc.state, Unseen: tc.unseen, WindowID: tc.windowID}
			got := dashboard.StateGroup(s, tc.activity, threshold)
			if got != tc.want {
				t.Errorf("dashboard.StateGroup(%q, unseen=%v, win=%d) = %d, want %d", tc.state, tc.unseen, tc.windowID, got, tc.want)
			}
		})
	}
}

func TestStateGroupZeroThreshold(t *testing.T) {
	// A zero threshold disables the staleness check; empty-state sessions
	// with any activity record should still classify as Active.
	s := session.RunningSession{State: "", WindowID: 1}
	activity := map[int]time.Time{1: time.Now().Add(-time.Hour)}
	got := dashboard.StateGroup(s, activity, 0)
	if got != 1 {
		t.Errorf("expected Active (1) with zero threshold, got %d", got)
	}
}

// ansiRE strips ANSI CSI escape sequences from a Lipgloss-rendered row so
// we can compare visual offsets independent of styling.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// terminalWidth approximates how many cells a string consumes when printed to
// a real terminal. Unlike go-runewidth, uniseg accounts for VS-16 selecting the
// two-cell emoji presentation of glyphs such as "⚠️".
func terminalWidth(s string) int {
	return uniseg.StringWidth(s)
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
	if !strings.Contains(out, "No running sessions") {
		t.Errorf("expected title; got %q", out)
	}
	if !strings.Contains(out, "press  l  to launch") {
		t.Errorf("expected launch hint; got %q", out)
	}
	if strings.Contains(out, "esc") {
		t.Errorf("zero-running hint should not mention esc; got %q", out)
	}
}

// TestRenderListInitialLoadDoesNotClaimTheListIsEmpty verifies that the cold
// startup state remains distinct from a successfully loaded empty snapshot.
func TestRenderListInitialLoadDoesNotClaimTheListIsEmpty(t *testing.T) {
	m := testModel()
	m.width = 60

	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "Loading sessions…") {
		t.Errorf("expected loading title; got %q", out)
	}
	if strings.Contains(out, "No running sessions") {
		t.Errorf("startup must not render the confirmed-empty title; got %q", out)
	}

	m = loadSessions(m, nil)
	out = stripAnsi(renderList(m))
	if !strings.Contains(out, "No running sessions") {
		t.Errorf("expected confirmed-empty title after load; got %q", out)
	}
	if strings.Contains(out, "Loading sessions…") {
		t.Errorf("loading title remained after the initial snapshot; got %q", out)
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
		activeFilter: dashboard.Parse("nomatch"),
	}
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "No sessions match the active filter") {
		t.Errorf("expected filtered-out title; got %q", out)
	}
	if !strings.Contains(out, "press  esc  to clear filters") {
		t.Errorf("expected esc hint; got %q", out)
	}
}

// TestRenderListMixedGroupBorders covers a list with two urgency groups
// (Needs you and Running) and asserts each row carries the left border glyph
// and that group labels are emitted above each band.
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
	m.rebuildItems()
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "Needs you (1)") {
		t.Errorf("expected attention label; got %q", out)
	}
	if !strings.Contains(out, "Running (1)") {
		t.Errorf("expected active label; got %q", out)
	}
	if !strings.Contains(out, dashboard.GroupBorderGlyph) {
		t.Errorf("expected border glyph %q in output %q", dashboard.GroupBorderGlyph, out)
	}
}

// TestRenderListSingleGroupKeepsItsHeadingWithoutABorder asserts what the two
// halves of group chrome now say separately. The heading is a fact about the
// queue — how many sessions are in this bucket — and stays drawn when the bucket
// is the only one; the left border exists to tell two adjacent buckets apart, so
// with nothing to separate it is not drawn at all.
func TestRenderListSingleGroupKeepsItsHeadingWithoutABorder(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 80,
		sessions: []session.RunningSession{
			{Base: "a", State: "bolt", Account: "w"},
			{Base: "b", State: "bolt", Account: "w"},
		},
	}
	m.filtered = m.sessions
	m.rebuildItems()
	out := stripAnsi(renderList(m))
	if !strings.Contains(out, "Running (2)") {
		t.Errorf("single-group list should still count its bucket; got %q", out)
	}
	if strings.Contains(out, dashboard.GroupBorderGlyph) {
		t.Errorf("single-group list should not render border; got %q", out)
	}
}

// queueRowFor projects one session the way a render pass does — build the
// queue, scan its columns, compose the line — so a row assertion in this package
// measures the same string RenderList would draw for that session.
func queueRowFor(s session.RunningSession, width int) string {
	queue := dashboard.BuildQueue(dashboard.QueueInput{Rows: []session.RunningSession{s}})
	return stripAnsi(dashboard.FormatRow(dashboard.RowInput{
		Queue:   queue[0],
		Width:   width,
		Columns: dashboard.ScanQueueColumns(queue),
	}))
}

// TestOperationalPrefixesAreGoneFromTheQueueRow is the negative half of the
// redesign's first sentence: the row no longer opens with the operational
// glyphs it used to lead with. The state icon and the account badge were both
// prefixes — cells spent before the name, in every row, whether or not they had
// anything to say — and a queue that answers "which session, what is it doing"
// starts with the answer to the first question.
//
// It asserts across every state rather than one, because the icon column was
// reserved unconditionally: a renderer that dropped the glyph but kept its
// padding would still push every name one cell right, and only the row that has
// no icon would reveal it.
func TestOperationalPrefixesAreGoneFromTheQueueRow(t *testing.T) {
	for _, state := range []string{"", "bolt", "stop", "key", "warning", "error"} {
		s := session.RunningSession{Base: "api", Account: "work", State: state, Unseen: state == "stop"}
		row := queueRowFor(s, 80)

		if icon := palette.StateIcon(state); icon != "" && strings.Contains(row, icon) {
			t.Errorf("state %q: the row still draws its state icon %q: %q", state, icon, row)
		}
		if strings.Contains(row, "[") {
			t.Errorf("state %q: the row still draws an account badge: %q", state, row)
		}
		if !strings.HasPrefix(row, "api") {
			t.Errorf("state %q: the row does not open with its name: %q", state, row)
		}
	}
}

// TestHarnessTravelsWithTheSelectedSessionRatherThanEveryRow covers where the
// harness went. It was an always-reserved row cell carrying a configured emoji
// and a label; it is now one token of the selected session's context, spelled in
// words. The emoji is what the assertion is on: the configured glyph is the
// thing an operator would notice returning to the row, and a row that quietly
// reserved its two cells again would go unnoticed by a label-only check.
func TestHarnessTravelsWithTheSelectedSessionRatherThanEveryRow(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🐱", "codex": "🦊", "copilot": "🐙"},
	}
	for harness, emoji := range cfg.HarnessEmojis {
		s := session.RunningSession{Base: "api", Account: "work", Harness: harness}

		if row := queueRowFor(s, 80); strings.Contains(row, emoji) || strings.Contains(row, harness) {
			t.Errorf("harness %q still reaches the queue row: %q", harness, row)
		}

		pane := stripAnsi(dashboard.RenderInspector(dashboard.InspectorInput{
			Width: 100, Height: 12, Content: dashboard.InspectorReady, Session: s, Cfg: cfg,
		}))
		if !strings.Contains(pane, harness) {
			t.Errorf("harness %q is named nowhere in the selected context:\n%s", harness, pane)
		}
	}
}

// TestMultibyteProfileNameSurvivesWholeInTheSelectedContext replaces the badge's
// first-rune test. The row used to compress a profile to `[É]`, which is why
// taking that initial by byte rather than by rune was a real hazard; the
// inspector spends a whole labelled line on the name instead, so the contract is
// now that the name arrives intact and still renders as valid UTF-8.
func TestMultibyteProfileNameSurvivesWholeInTheSelectedContext(t *testing.T) {
	for _, profile := range []string{"éscriba", "中文", "🐙ctopus"} {
		s := session.RunningSession{Base: "x", Account: profile}
		pane := dashboard.RenderInspector(dashboard.InspectorInput{
			Width: 100, Height: 12, Content: dashboard.InspectorReady,
			Session: s, Cfg: &config.Config{},
		})
		if !strings.Contains(stripAnsi(pane), profile) {
			t.Errorf("profile %q is not carried whole by the selected context:\n%s", profile, pane)
		}
		if !utf8.ValidString(pane) {
			t.Errorf("profile %q: rendered context is not valid UTF-8:\n%s", profile, pane)
		}
	}
}

// TestQueueRowEmojiTitleColumnAlignment locks in the redesign's "retain the
// custom project emoji" against the column that now has to survive it. The icon
// set is the one the live dashboard uses (🐱amX, 🧠BrainNotes, 🗣commscoach,
// 🧬curator, 🧰dev-helpers, •dotfiles); the anchor moved from the retired
// directory column to Status, which follows the Session cell directly and is
// therefore what a mismeasured name pushes sideways.
//
// The rows are composed as ONE pass sharing scanned columns, because that is the
// only arrangement in which the claim says anything: rows measured individually
// would align by construction. Without grapheme-aware width, FE0F-bearing glyphs
// and narrow pictographs pad the Session cell wrongly and the Status column frays.
func TestQueueRowEmojiTitleColumnAlignment(t *testing.T) {
	titles := []string{
		"🐱amX",
		"🧠BrainNotes",
		"🗣commscoach",
		"🧬curator",
		"🧰dev-helpers",
		"•dotfiles",
	}
	rows := make([]session.RunningSession, len(titles))
	for i, title := range titles {
		rows[i] = session.RunningSession{WindowID: i + 1, Base: title, Account: "work", State: "bolt"}
	}
	queue := dashboard.BuildQueue(dashboard.QueueInput{Rows: rows})
	columns := dashboard.ScanQueueColumns(queue)
	rendered := make([]string, len(titles))
	for i := range titles {
		rendered[i] = stripAnsi(dashboard.FormatRow(dashboard.RowInput{
			Queue: queue[i], Width: 120, Columns: columns,
		}))
	}

	var statusOffset int
	for i, title := range titles {
		row := rendered[i]
		if !utf8.ValidString(row) {
			t.Fatalf("title %q produced invalid UTF-8 row: %q", title, row)
		}
		idx := strings.Index(row, queue[i].Status)
		if idx < 0 {
			t.Fatalf("status %q not found in row for title %q: %q", queue[i].Status, title, row)
		}
		offset := layout.Width(row[:idx])
		if i == 0 {
			statusOffset = offset
			continue
		}
		if offset != statusOffset {
			t.Errorf("Status column misaligned for %q: got cell %d, want %d (row=%q)", title, offset, statusOffset, row)
		}
	}

	// Pictographic-prefixed titles must reserve a 2-cell icon column so the
	// title body aligns across the dashboard icon set. The bullet '•' is
	// intentionally not promoted to that column — it stays at its measured
	// 1-cell width so it reads as a narrow marker rather than a chunky icon —
	// so its body lands one cell earlier than the rest.
	bodies := []string{"amX", "BrainNotes", "commscoach", "curator", "dev-helpers", "dotfiles"}
	var bodyOffset int
	for i, body := range bodies {
		idx := strings.Index(rendered[i], body)
		if idx < 0 {
			t.Fatalf("title body %q not found in row for title %q: %q", body, titles[i], rendered[i])
		}
		offset := layout.Width(rendered[i][:idx])
		switch {
		case i == 0:
			bodyOffset = offset
		case titles[i] == "•dotfiles":
			if offset != bodyOffset-1 {
				t.Errorf("bullet-prefixed body offset = %d, want %d (one cell before the emoji bodies; row=%q)",
					offset, bodyOffset-1, rendered[i])
			}
		case offset != bodyOffset:
			t.Errorf("emoji-prefixed title body offset drift for %q: got %d, want %d (row=%q)",
				titles[i], offset, bodyOffset, rendered[i])
		}
	}
}

// The legend strip is gone from above the list; its content is a generated
// section of the keyboard help sheet. That is a strict gain in reach: the strip
// needed both a spacious width and a tall terminal, so it was suppressed on
// exactly the frames where an unfamiliar glyph is hardest to guess at.
func TestStateIconReferenceIsGeneratedIntoHelp(t *testing.T) {
	out := strings.Join(helpStateIconSection(), "\n")
	for _, state := range dashboard.LegendStates {
		gloss := dashboard.StateGloss(state)
		if gloss == "" {
			continue
		}
		if !strings.Contains(out, gloss) {
			t.Errorf("state icon reference is missing the gloss %q:\n%s", gloss, out)
		}
		if icon := palette.StateIcon(state); icon != "" && !strings.Contains(out, icon) {
			t.Errorf("state icon reference is missing the icon %q for %q:\n%s", icon, state, out)
		}
	}
	if !strings.Contains(stripAnsi(strings.Join(helpContentLines(), "\n")), "State icons") {
		t.Error("help overlay does not carry the State icons section")
	}
}
