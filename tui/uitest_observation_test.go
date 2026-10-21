package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestUITestObservationCapturesModeSizeAndScreen verifies that observeUITest
// records the mode, size, and a normalized non-empty screen for a sized model.
func TestUITestObservationCapturesModeSizeAndScreen(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m = loadSessions(m, makeSessions())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	size := UITestSize{Width: 80, Height: 24}
	obs := observeUITest(m, size)

	if obs.Mode != "list" {
		t.Errorf("expected mode list, got %q", obs.Mode)
	}
	if obs.Size != size {
		t.Errorf("expected size %+v, got %+v", size, obs.Size)
	}
	if obs.Screen.Size != size {
		t.Errorf("expected screen size %+v, got %+v", size, obs.Screen.Size)
	}
	if len(obs.Screen.Lines) == 0 {
		t.Fatal("expected non-empty screen lines")
	}
	// rootChrome.compose pads/truncates the body to the terminal height, so a
	// sized render should exactly fill the viewport with no height overflow.
	if obs.Screen.Height != 24 {
		t.Errorf("expected 24 rendered lines, got %d", obs.Screen.Height)
	}
	for _, iss := range obs.Screen.Issues {
		if iss.Type == UITestIssueScreenHeightOverflow {
			t.Errorf("unexpected screen height overflow at 80x24: %+v", iss)
		}
	}
}

// TestUITestObservationTracksModeCursor verifies the observation reports the
// cursor relevant to the active mode: the picker cursor once the picker is open.
func TestUITestObservationTracksModeCursor(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m = loadSessions(m, makeSessions())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	m.pickerCursor = 1

	obs := observeUITest(m, UITestSize{Width: 80, Height: 24})
	if obs.Mode != "picker" {
		t.Errorf("expected mode picker, got %q", obs.Mode)
	}
	if obs.Cursor != 1 {
		t.Errorf("expected picker cursor 1, got %d", obs.Cursor)
	}
}

// TestUITestObservationModeNames pins the stable mode-name strings so scenario
// output never regresses to bare integers or drifts silently.
func TestUITestObservationModeNames(t *testing.T) {
	t.Parallel()

	cases := map[Mode]string{
		ModeList:              "list",
		ModeSearch:            "search",
		ModeConfirm:           "confirm",
		ModePicker:            "picker",
		ModeConfigPicker:      "config_picker",
		ModeHelp:              "help",
		ModeActions:           "actions",
		ModeDetail:            "detail",
		ModeCommandPalette:    "command_palette",
		ModeWorktrees:         "worktrees",
		ModeLiveSessionPicker: "live_session_picker",
		ModeASLRuns:           "asl_runs",
	}
	for mode, want := range cases {
		if got := uitestModeName(mode); got != want {
			t.Errorf("uitestModeName(%d) = %q, want %q", mode, got, want)
		}
	}
	if got := uitestModeName(Mode(9999)); got != "unknown" {
		t.Errorf("expected unknown for out-of-range mode, got %q", got)
	}
}

// TestUITestLayoutIssuesIncludeModalFit verifies that observeUITest surfaces
// modal-fit issues on the observation's screen, so the no_layout_issues
// assertion (which reads final.Screen.Issues) catches an overlay that needs more
// rows than the terminal can provide — even though the root screen itself
// renders clean at that size.
//
// The overflowing probe is a terminal three rows tall rather than the 80x24 this
// test used to assert the actions overlay was clipped at. 80x24 is now a size
// the menu is REQUIRED to fit, and TestUITestActionsMenuFitsEveryTargetSize
// asserts it does; what is kept here is the property this test is actually
// about, which is that a modal issue reaches the observation and fails
// no_layout_issues rather than being invisible to a scenario.
func TestUITestLayoutIssuesIncludeModalFit(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), makeSessions())

	// Too short: the body area cannot hold the menu's box at all, which is the
	// one case shedding chrome cannot rescue.
	narrow := m
	updated, _ := narrow.Update(tea.WindowSizeMsg{Width: 120, Height: 3})
	narrow = updated.(Model)
	narrow = pressKey(narrow, "a")
	if narrow.mode != ModeActions {
		t.Fatalf("expected ModeActions after a, got %d", narrow.mode)
	}
	obs := observeUITest(narrow, UITestSize{Width: 120, Height: 3})
	if !hasIssueType(obs.Screen.Issues, UITestIssueModalHeightOverflow) {
		t.Errorf("expected modal_height_overflow on the observation screen, got %v", obs.Screen.Issues)
	}

	// The no_layout_issues assertion should fail because of the modal issue.
	fail := evaluateUITestAssertion(UITestAssertion{Type: UITestAssertNoLayoutIssues}, obs, narrow, nil)
	if fail == nil {
		t.Error("no_layout_issues should fail when the modal overflows")
	}

	// At every target size the same overlay fits, no modal issue is attached,
	// and the assertion passes. 80x24 is included deliberately: it is the size
	// this test used to require an overflow at.
	for _, size := range []UITestSize{{Width: 80, Height: 24}, {Width: 100, Height: 24}, {Width: 120, Height: 30}, {Width: 160, Height: 45}} {
		wide := m
		updated, _ = wide.Update(tea.WindowSizeMsg{Width: size.Width, Height: size.Height})
		wide = updated.(Model)
		wide = pressKey(wide, "a")
		wobs := observeUITest(wide, size)
		if hasIssueType(wobs.Screen.Issues, UITestIssueModalWidthOverflow) ||
			hasIssueType(wobs.Screen.Issues, UITestIssueModalHeightOverflow) {
			t.Errorf("actions overlay at %dx%d should not report modal issues, got %v", size.Width, size.Height, wobs.Screen.Issues)
		}
		if fail := evaluateUITestAssertion(UITestAssertion{Type: UITestAssertNoLayoutIssues}, wobs, wide, nil); fail != nil {
			t.Errorf("no_layout_issues should pass for a fitting overlay at %dx%d, got %+v", size.Width, size.Height, fail)
		}
	}
}

// The profile picker remains readable with selected-home details at both sizes.
func TestUITestConfigPickerModalFitPinsWidthFix(t *testing.T) {
	t.Parallel()

	fx := wideProfilesFixture()

	// Narrow: responsive rows and wrapped details fit.
	narrow, _ := newUITestFixtureDriver(fx, UITestSize{Width: 120, Height: 40})
	cleanupUITestDriver(t, narrow)
	if err := narrow.Key(keyA); err != nil {
		t.Fatalf("Key(a) error: %v", err)
	}
	if err := narrow.Key(keyDashboardLaunch); err != nil {
		t.Fatalf("Key(C) error: %v", err)
	}
	nobs := narrow.Observe()
	if nobs.Mode != "config_picker" {
		t.Fatalf("expected config_picker mode after C, got %q", nobs.Mode)
	}
	if len(nobs.Screen.Issues) != 0 {
		t.Errorf("expected a fitting profile picker at 120 wide, got %v", nobs.Screen.Issues)
	}

	// Wide: the same identity and detail remain visible.
	wide, _ := newUITestFixtureDriver(fx, UITestSize{Width: 140, Height: 40})
	cleanupUITestDriver(t, wide)
	if err := wide.Key(keyA); err != nil {
		t.Fatalf("Key(a) error: %v", err)
	}
	if err := wide.Key(keyDashboardLaunch); err != nil {
		t.Fatalf("Key(C) error: %v", err)
	}
	wobs := wide.Observe()
	if wobs.Mode != "config_picker" {
		t.Fatalf("expected config_picker mode after C, got %q", wobs.Mode)
	}
	if hasIssueType(wobs.Screen.Issues, UITestIssueModalWidthOverflow) ||
		hasIssueType(wobs.Screen.Issues, UITestIssueModalHeightOverflow) {
		t.Errorf("config picker at 140 wide should fit, got %v", wobs.Screen.Issues)
	}
}
