package tui

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestDispatchPickerLaunch_ToastTickNotInSequence pins the fix that the
// toast-tick cmd returned by replaceToastByID is NOT placed inside the
// tea.Sequence of launches. If it were, the tick's 250ms timer would gate
// every batch behind a quarter-second delay before the first launch fires.
//
// The dispatch result must be a tea.Batch whose first leaf is the toast
// tick (yielding a toastTickMsg) and whose second leaf is the sequence of
// launch closures (yielding one launchProgressMsg per session). Counting
// launchProgressMsg via the existing helper still works because flattenMsg
// walks both Batch and Sequence shapes.
func TestDispatchPickerLaunch_ToastTickNotInSequence(t *testing.T) {
	m := launchDispatchModel(t, nil)
	selectByTitle(&m, "api", "backend")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when both sessions are launchable")
	}

	// Inspect the top-level message produced by cmd(). The fix wraps the
	// toast tick + the launch sequence in a tea.Batch, so the top-level
	// result must be a slice (BatchMsg) of length 2: [toastTickCmd, seq].
	top := cmd()
	v := reflect.ValueOf(top)
	if v.Kind() != reflect.Slice {
		t.Fatalf("expected top-level Batch (slice), got kind=%v type=%T", v.Kind(), top)
	}
	if v.Len() != 2 {
		t.Fatalf("expected top-level Batch len=2 (toast + sequence), got %d", v.Len())
	}

	// Independently confirm launch count is still 2 — the fix must not
	// change the number of launchProgressMsg emitted.
	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg after fix, got %d", len(got))
	}
}

// TestDispatchPickerLaunch_NoToastWhenAlreadyAlive verifies that when a
// toast already exists (so replaceToastByID returns nil because no fresh
// tick is needed), dispatch returns the bare launch sequence — there is
// no Batch wrapper to consume an extra reflect step in callers.
func TestDispatchPickerLaunch_NoToastWhenAlreadyAlive(t *testing.T) {
	m := launchDispatchModel(t, nil)
	// Seed an unrelated toast so the strip is non-empty when dispatch fires.
	if c := m.pushToast(Toast{Severity: toastInfo, Text: "seed"}); c == nil {
		t.Fatal("expected pushToast cmd when seeding from empty")
	}
	selectByTitle(&m, "api", "backend")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when sessions are launchable")
	}
	// All leaves are still launchProgressMsg (no toastTickMsg appears at
	// the top level because the seed toast already armed the ticker).
	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg, got %d", len(got))
	}
}

// TestConfigPickerEnterProjectIdxOOBSafe pins the fix that
// handleConfigPickerKey bounds-checks m.configPickerProjectIdx against
// len(m.pickerSessions) before indexing. A stale projectIdx (e.g.
// referencing a session that was removed from cfg.Sessions between
// opening the picker and pressing enter) must not panic — it should be
// treated as a no-op that returns the user to the return mode.
func TestConfigPickerEnterProjectIdxOOBSafe(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)

	// Simulate "user opened the launch-profile picker for a project index
	// that is now beyond pickerSessions" by setting projectIdx past the
	// slice length.
	m.pickerSessions = []config.Session{{Title: "api", Account: "work"}}
	m.configPickerAccounts = cfg.Accounts
	m.configPickerCursor = 0
	m.configPickerPurpose = ConfigPickerLauncherOverride
	m.configPickerReturnMode = ModePicker
	m.configPickerProjectIdx = 99 // stale / out of bounds
	m.mode = ModeConfigPicker

	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handleConfigPickerKey panicked on OOB projectIdx: %v", r)
		}
	}()
	updated, _ := m.Update(keyPress("enter"))
	got := updated.(Model)
	// Return mode is honored; no override was written for the stale idx.
	if got.mode != ModePicker {
		t.Fatalf("expected ModePicker (configPickerReturnMode) after OOB enter, got %d", got.mode)
	}
	if _, exists := got.pickerProfileOverride[99]; exists {
		t.Error("OOB projectIdx must not be written into pickerProfileOverride")
	}
}

func TestConfigPickerEnterNegativeCursorSafe(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m.configPickerAccounts = cfg.Accounts
	m.configPickerCursor = -1
	m.configPickerPurpose = ConfigPickerRestartProfile
	m.configPickerReturnMode = ModeList
	m.mode = ModeConfigPicker

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handleConfigPickerKey panicked on negative cursor: %v", r)
		}
	}()
	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if got.mode != ModeConfigPicker {
		t.Fatalf("mode = %d, want ModeConfigPicker for invalid cursor", got.mode)
	}
	if cmd != nil {
		t.Fatal("invalid cursor enter returned non-nil cmd")
	}
}
