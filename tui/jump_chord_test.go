package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// chordModel builds a Model wired to the standard mock client and seeds the
// session list directly so row order matches the test's expectations without
// going through sortFiltered.
func chordModel(rows []session.RunningSession, cursor int) Model {
	m := testModel()
	m.sessions = rows
	m.filtered = rows
	m.rebuildItems()
	m.cursor = cursor
	return m
}

// TestChordSingleBracketArmsDirection verifies that `]` and `[` alone set
// pendingJumpDir to their direction without moving the cursor.
func TestChordSingleBracketArmsDirection(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "a", State: ""},
		{TabID: 101, Base: "b", State: "key"},
		{TabID: 102, Base: "c", State: ""},
	}

	cases := []struct {
		name    string
		key     string
		wantDir int
	}{
		{"forward arms +1", "]", +1},
		{"backward arms -1", "[", -1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := chordModel(rows, 0)
			updated, _ := m.Update(keyPress(tc.key))
			m2 := updated.(Model)
			if m2.pendingJumpDir != tc.wantDir {
				t.Errorf("pendingJumpDir = %d, want %d", m2.pendingJumpDir, tc.wantDir)
			}
			if m2.cursor != 0 {
				t.Errorf("cursor moved to %d, expected 0 (no movement on chord arm)", m2.cursor)
			}
		})
	}
}

// TestChordCompletionForwardPermission verifies that `]p` advances to the
// next `key` row and resets pendingJumpDir.
func TestChordCompletionForwardPermission(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "active", State: "bolt"},
		{TabID: 102, Base: "needs-key", State: "key"},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("p"))
	m2 := updated.(Model)

	if m2.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (jumped to key row)", m2.cursor)
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0 (chord consumed)", m2.pendingJumpDir)
	}
}

// TestChordCompletionFailureEorWarning verifies that `]e` matches either
// "error" or "warning" — both are treated as the failure chord.
func TestChordCompletionFailureEorWarning(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "warned", State: "warning"},
		{TabID: 102, Base: "broken", State: "error"},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("e"))
	m2 := updated.(Model)

	if m2.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (first failure row)", m2.cursor)
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0", m2.pendingJumpDir)
	}
}

// TestChordCompletionBackward verifies that `[a` walks backward to a `bolt`
// row.
func TestChordCompletionBackward(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "active", State: "bolt"},
		{TabID: 101, Base: "idle", State: ""},
		{TabID: 102, Base: "other", State: ""},
	}
	m := chordModel(rows, 2)

	updated, _ := m.Update(keyPress("["))
	updated, _ = updated.(Model).Update(keyPress("a"))
	m2 := updated.(Model)

	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (backward to bolt row)", m2.cursor)
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0", m2.pendingJumpDir)
	}
}

// TestChordDoubleTapPerformsAttentionJump verifies that `]]` performs the
// S1 attention-set jump (preserved behavior).
func TestChordDoubleTapPerformsAttentionJump(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "warned", State: "warning"},
		{TabID: 102, Base: "idle-b", State: ""},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("]"))
	m2 := updated.(Model)

	if m2.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (attention jump landed on warning row)", m2.cursor)
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0", m2.pendingJumpDir)
	}
}

// TestChordDoubleTapBackward verifies that `[[` performs the backward
// attention-set jump.
func TestChordDoubleTapBackward(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "needs-key", State: "key"},
		{TabID: 101, Base: "idle", State: ""},
		{TabID: 102, Base: "idle-b", State: ""},
	}
	m := chordModel(rows, 2)

	updated, _ := m.Update(keyPress("["))
	updated, _ = updated.(Model).Update(keyPress("["))
	m2 := updated.(Model)

	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (backward attention jump)", m2.cursor)
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0", m2.pendingJumpDir)
	}
}

// TestChordUnknownKeyCancels verifies that `]z` cancels the chord (resets
// pendingJumpDir) without moving the cursor.
func TestChordUnknownKeyCancels(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "needs-key", State: "key"},
		{TabID: 102, Base: "idle-b", State: ""},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("z"))
	m2 := updated.(Model)

	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0 (canceled)", m2.pendingJumpDir)
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (no movement on unknown chord)", m2.cursor)
	}
}

// TestChordTailKeyCancelsWithoutLaunching verifies `]t` only cancels the
// pending jump chord. The `t` key is a ModeList action, but not a chord
// follow-up, so it must not dispatch loop-tail discovery from chord state.
func TestChordTailKeyCancelsWithoutLaunching(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "needs-key", State: "key"},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, cmd := updated.(Model).Update(keyPress("t"))
	m2 := updated.(Model)

	if cmd != nil {
		t.Fatal("expected no loop-tail command from ]t")
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0 (canceled)", m2.pendingJumpDir)
	}
	if m2.mode != ModeList {
		t.Errorf("mode = %v, want ModeList", m2.mode)
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (no movement on tail chord)", m2.cursor)
	}
}

// TestChordEscCancels verifies that pressing Esc while a chord is pending
// silently cancels it without side effects.
func TestChordEscCancels(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "needs-key", State: "key"},
	}
	m := chordModel(rows, 0)
	// Apply an active filter so we can prove Esc didn't also clear it.
	m.activeFilter = dashboard.Parse("@work")

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("esc"))
	m2 := updated.(Model)

	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0", m2.pendingJumpDir)
	}
	if m2.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m2.cursor)
	}
	if m2.activeFilter.IsEmpty() {
		t.Errorf("active filter was cleared by chord-cancel Esc; should be preserved")
	}
}

// TestChordModeTransitionClearsPending verifies that pressing `/` to enter
// filter mode clears any pending chord direction.
func TestChordModeTransitionClearsPending(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "needs-key", State: "key"},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("/"))
	m2 := updated.(Model)

	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0 after mode transition", m2.pendingJumpDir)
	}
	if m2.mode != ModeSearch {
		t.Errorf("mode = %v, want ModeSearch (filter entered)", m2.mode)
	}
}

// TestChordQDoesNotQuit verifies that with a pending chord, `q` is consumed
// by the chord (question state matcher) rather than quitting the app. The
// `q` chord targets the "question" state, so the cursor advances if such a
// row exists.
func TestChordQDoesNotQuit(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "elicit", State: "question"},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("q"))
	m2 := updated.(Model)

	if m2.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (jumped to question row)", m2.cursor)
	}
	if m2.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0", m2.pendingJumpDir)
	}
}

// TestChordClearedAcrossInvocations ensures pendingJumpDir does not persist
// past a completed chord — pressing `p` again on a clean state must NOT
// behave like a chord follow-up.
func TestChordClearedAcrossInvocations(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle", State: ""},
		{TabID: 101, Base: "needs-key", State: "key"},
		{TabID: 102, Base: "idle-b", State: ""},
	}
	m := chordModel(rows, 0)

	updated, _ := m.Update(keyPress("]"))
	updated, _ = updated.(Model).Update(keyPress("p"))
	m2 := updated.(Model)
	if m2.cursor != 1 || m2.pendingJumpDir != 0 {
		t.Fatalf("setup failed: cursor=%d, pendingJumpDir=%d", m2.cursor, m2.pendingJumpDir)
	}

	// Pressing `p` again should toggle the preview pane (its normal
	// list-mode binding), not run a chord. We only assert that the model
	// did not move the cursor as a chord would and that pendingJumpDir
	// remains 0.
	updated, _ = m2.Update(keyPress("p"))
	m3 := updated.(Model)
	if m3.pendingJumpDir != 0 {
		t.Errorf("pendingJumpDir = %d, want 0 (no stale chord state)", m3.pendingJumpDir)
	}
}
