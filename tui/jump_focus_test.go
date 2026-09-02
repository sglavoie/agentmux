package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// jumpFocusModel builds a Model wired to a MockRunner that captures every
// kitten invocation. The caller can inject any RunningSession slice and
// initial cursor; sessions are set directly (no loadSessions) so the order
// matches the test's expectation without going through sortFiltered.
func jumpFocusModel(rows []session.RunningSession, cursor int) (Model, *kitty.MockRunner) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m.sessions = rows
	m.filtered = rows
	m.cursor = cursor
	return m, runner
}

// focusCallSeen reports whether runner saw a `kitten @ focus-tab --match=id:<tabID>` call.
func focusCallSeen(runner *kitty.MockRunner, tabID int) bool {
	match := "--match=" + kitty.MatchTabByID(tabID)
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		var sawFocusTab, sawMatch bool
		for _, a := range c.Args {
			if a == "focus-tab" {
				sawFocusTab = true
			}
			if a == match {
				sawMatch = true
			}
		}
		if sawFocusTab && sawMatch {
			return true
		}
	}
	return false
}

// anyFocusTabCall reports whether the runner saw any focus-tab invocation.
// Used to assert that the no-match branch does not dispatch a focus cmd.
func anyFocusTabCall(runner *kitty.MockRunner) bool {
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "focus-tab" {
				return true
			}
		}
	}
	return false
}

// TestJumpForwardWithFocus verifies `}` advances the cursor to the next
// attention row and dispatches commands.Focus for that row's TabID.
func TestJumpForwardWithFocus(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 100, Base: "idle-a", State: ""},
		{TabID: 101, Base: "needs-key", State: "key"},
		{TabID: 102, Base: "idle-b", State: ""},
	}
	m, runner := jumpFocusModel(rows, 0)

	updated, cmd := m.Update(keyPress("}"))
	m2 := updated.(Model)

	if m2.cursor != 1 {
		t.Errorf("forward jump: expected cursor=1, got %d", m2.cursor)
	}
	if cmd == nil {
		t.Fatal("forward jump: expected non-nil cmd carrying focus batch, got nil")
	}
	collectLeafMsgs(t, cmd)
	if !focusCallSeen(runner, 101) {
		t.Errorf("forward jump: expected focus-tab id:101, runner.Calls=%v", runner.Calls)
	}
}

// TestJumpBackwardWithFocus verifies `{` walks backward to the prior
// attention row and focuses it.
func TestJumpBackwardWithFocus(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 200, Base: "failed", State: "error"},
		{TabID: 201, Base: "idle-a", State: ""},
		{TabID: 202, Base: "idle-b", State: ""},
	}
	m, runner := jumpFocusModel(rows, 2)

	updated, cmd := m.Update(keyPress("{"))
	m2 := updated.(Model)

	if m2.cursor != 0 {
		t.Errorf("backward jump: expected cursor=0, got %d", m2.cursor)
	}
	if cmd == nil {
		t.Fatal("backward jump: expected non-nil cmd carrying focus batch, got nil")
	}
	collectLeafMsgs(t, cmd)
	if !focusCallSeen(runner, 200) {
		t.Errorf("backward jump: expected focus-tab id:200, runner.Calls=%v", runner.Calls)
	}
}

// TestJumpFocusNoMatchLeavesCursorAndDoesNotFocus verifies that pressing
// `}` when no attention row exists leaves the cursor untouched and does
// not dispatch a focus command.
func TestJumpFocusNoMatchLeavesCursorAndDoesNotFocus(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 300, Base: "idle-a", State: ""},
		{TabID: 301, Base: "active", State: "bolt"},
		{TabID: 302, Base: "idle-b", State: ""},
	}
	m, runner := jumpFocusModel(rows, 0)

	updated, cmd := m.Update(keyPress("}"))
	m2 := updated.(Model)

	if m2.cursor != 0 {
		t.Errorf("no-match forward: expected cursor unchanged (0), got %d", m2.cursor)
	}
	if cmd != nil {
		collectLeafMsgs(t, cmd)
	}
	if anyFocusTabCall(runner) {
		t.Fatalf("no-match forward: focus-tab must not be invoked; runner.Calls=%v", runner.Calls)
	}
}

// TestJumpFocusBackwardNoMatch mirrors the no-match assertion for `{`.
func TestJumpFocusBackwardNoMatch(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 400, Base: "idle", State: ""},
		{TabID: 401, Base: "active", State: "bolt"},
	}
	m, runner := jumpFocusModel(rows, 1)

	updated, cmd := m.Update(keyPress("{"))
	m2 := updated.(Model)

	if m2.cursor != 1 {
		t.Errorf("no-match backward: expected cursor unchanged (1), got %d", m2.cursor)
	}
	if cmd != nil {
		collectLeafMsgs(t, cmd)
	}
	if anyFocusTabCall(runner) {
		t.Fatalf("no-match backward: focus-tab must not be invoked; runner.Calls=%v", runner.Calls)
	}
}
