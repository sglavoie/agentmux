package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

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
			{WindowID: 1, State: "key"},  // changed
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

// TestRecordTransitionsDoesNotRePulseStableState verifies that after a
// session transitions once, repeated refreshes (without further state
// changes) do not re-record a pulse once the prior one has decayed.
// Without advancing lastSeenStates inside recordTransitions, the stale
// snapshot would keep mismatching the current state and a fresh pulse
// would fire forever every pulseDecay seconds.
func TestRecordTransitionsDoesNotRePulseStableState(t *testing.T) {
	m := Model{
		lastSeenStates: map[int]string{1: "bolt"},
		sessions:       []session.RunningSession{{WindowID: 1, State: "key"}},
	}
	// First refresh records the transition.
	m.recordTransitions()
	if _, ok := m.recentTransitions[1]; !ok {
		t.Fatalf("setup: expected pulse on first transition; got %v", m.recentTransitions)
	}
	if got := m.lastSeenStates[1]; got != "key" {
		t.Fatalf("expected lastSeenStates advanced to %q after transition; got %q", "key", got)
	}

	// Simulate the pulse decaying past its window: the sweeper removes
	// the entry while the session state has not changed since.
	delete(m.recentTransitions, 1)

	// A subsequent refresh with the same session state must not record
	// a new pulse — the change has already been surfaced.
	m.recordTransitions()
	if _, ok := m.recentTransitions[1]; ok {
		t.Errorf("expected no new pulse for stable state after decay; got %v", m.recentTransitions)
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
	base.rebuildItems()

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
	if !strings.Contains(with, dashboard.GroupBorderGlyph) {
		t.Errorf("expected pulse glyph %q in pulsing render; got %q", dashboard.GroupBorderGlyph, with)
	}
}

// TestUnreadDotSurvivesTheKeypressThatClearsThePulse is the fallback the two
// notices share one gutter for. A ModeList keypress wipes the whole pulse layer
// — that is what makes a pulse mean "you have not seen this change yet" — but
// seenness is daemon-owned, so the completion underneath is still unread and the
// gutter keeps saying so with the durable dot. Neither the keypress, the cursor
// move it caused, nor the render that followed may touch Unseen.
func TestUnreadDotSurvivesTheKeypressThatClearsThePulse(t *testing.T) {
	m := Model{
		cfg:   &config.Config{},
		width: 100,
		sessions: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "stop", Account: "w", Unseen: true},
			{WindowID: 2, Base: "web", State: "stop", Account: "w", Unseen: true},
		},
		recentTransitions: map[int]time.Time{1: time.Now()},
		lastSeenStates:    map[int]string{1: "bolt", 2: "stop"},
	}
	m.filtered = m.sessions
	m.rebuildItems()

	pulsing := stripAnsi(renderList(m))
	pulsedRow, dottedRow := pulsing, pulsing
	if lines := strings.Split(strings.TrimRight(pulsing, "\n"), "\n"); len(lines) == 2 {
		pulsedRow, dottedRow = lines[0], lines[1]
	} else {
		t.Fatalf("setup: expected 2 rendered rows, got %d:\n%s", len(lines), pulsing)
	}
	if !strings.Contains(pulsedRow, dashboard.GroupBorderGlyph) {
		t.Fatalf("setup: expected the transition pulse in the gutter; got %q", pulsedRow)
	}
	// The pulsing row is ALSO an unread stop; the louder, newer notice wins the
	// cell, while its unpulsed sibling shows the dot in the same column.
	if strings.Contains(pulsedRow, dashboard.UnreadNoticeGlyph) {
		t.Errorf("the pulsing row drew the dot as well as the pulse: %q", pulsedRow)
	}
	if !strings.Contains(dottedRow, dashboard.UnreadNoticeGlyph) {
		t.Errorf("the unpulsed unread row lost its dot to its sibling's pulse: %q", dottedRow)
	}

	// A cursor move is ModeList input, so it clears the pulse layer.
	moved := pressKey(m, "j")
	if len(moved.recentTransitions) != 0 {
		t.Fatalf("setup: expected the keypress to clear the pulse layer; got %v", moved.recentTransitions)
	}

	after := stripAnsi(renderList(moved))
	if strings.Contains(after, dashboard.GroupBorderGlyph) {
		t.Errorf("the pulse outlived the keypress that cleared it:\n%s", after)
	}
	if got, want := strings.Count(after, dashboard.UnreadNoticeGlyph), 2; got != want {
		t.Errorf("expected %d unread dots once the pulse cleared, got %d:\n%s", want, got, after)
	}
	for i, s := range moved.filtered {
		if !s.Unseen {
			t.Errorf("row %d had its unseen bit cleared by a keypress and a render; only the daemon may clear it", i)
		}
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
	m.rebuildItems()
	out := renderList(m)
	if m.hasActivePulse(1) {
		t.Fatalf("baseline model should have no active pulse; got %v", m.recentTransitions)
	}
	// The row should not begin with the pulse-spacer (two leading spaces
	// from the pulse column). Since there's a single group, the row also
	// has no group border — so the row starts directly with formatRow.
	plain := stripAnsi(out)
	if strings.HasPrefix(plain, "  "+dashboard.GroupBorderGlyph) {
		t.Errorf("unexpected pulse column when no pulse active; got %q", plain)
	}
}
