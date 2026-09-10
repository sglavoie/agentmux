package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestUITestObservationCapturesModeSizeAndScreen verifies that observeUITest
// records the mode, size, and a normalized non-empty screen for a sized model.
func TestUITestObservationCapturesModeSizeAndScreen(t *testing.T) {
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
// columns or rows than the terminal can provide — even though the root screen
// itself renders clean at that size.
func TestUITestLayoutIssuesIncludeModalFit(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())

	// Narrow: the actions overlay's widest description row cannot fit, and its
	// wrapped rows exceed the body area.
	narrow := m
	updated, _ := narrow.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	narrow = updated.(Model)
	narrow = pressKey(narrow, "a")
	if narrow.mode != ModeActions {
		t.Fatalf("expected ModeActions after a, got %d", narrow.mode)
	}
	obs := observeUITest(narrow, UITestSize{Width: 80, Height: 24})

	// The root screen must not itself overflow — the whole point is that the
	// modal issue is only visible via the model-aware detector.
	for _, iss := range obs.Screen.Issues {
		if iss.Type == UITestIssueLineWidthOverflow {
			t.Fatalf("did not expect a root line_width_overflow at 80x24: %+v", iss)
		}
	}
	if !hasIssueType(obs.Screen.Issues, UITestIssueModalWidthOverflow) {
		t.Errorf("expected modal_width_overflow on the observation screen, got %v", obs.Screen.Issues)
	}

	// The no_layout_issues assertion should fail because of the modal issue.
	fail := evaluateUITestAssertion(UITestAssertion{Type: UITestAssertNoLayoutIssues}, obs, narrow, nil)
	if fail == nil {
		t.Error("no_layout_issues should fail when the modal overflows")
	}

	// Wide: the same overlay fits, so no modal issue is attached and the
	// assertion passes.
	wide := m
	updated, _ = wide.Update(tea.WindowSizeMsg{Width: 140, Height: 50})
	wide = updated.(Model)
	wide = pressKey(wide, "a")
	wobs := observeUITest(wide, UITestSize{Width: 140, Height: 50})
	if hasIssueType(wobs.Screen.Issues, UITestIssueModalWidthOverflow) ||
		hasIssueType(wobs.Screen.Issues, UITestIssueModalHeightOverflow) {
		t.Errorf("actions overlay at 140x50 should not report modal issues, got %v", wobs.Screen.Issues)
	}
	if fail := evaluateUITestAssertion(UITestAssertion{Type: UITestAssertNoLayoutIssues}, wobs, wide, nil); fail != nil {
		t.Errorf("no_layout_issues should pass for a fitting overlay, got %+v", fail)
	}
}

// TestUITestConfigPickerModalFitPinsWidthFix is the config-picker counterpart to
// TestUITestLayoutIssuesIncludeModalFit: it pins the profile-picker sizing fix
// (commit 1f08e24) at the observation layer. The wide_profiles fixture's long
// home-dir rows overflow the picker box at 120 cols (modal_width_overflow) but
// fit once the terminal is wide enough for the widest row.
func TestUITestConfigPickerModalFitPinsWidthFix(t *testing.T) {
	fx := wideProfilesFixture()

	// Narrow: the picker's widest row cannot fit, so the fit detector flags it.
	narrow, _ := newUITestFixtureDriver(fx, UITestSize{Width: 120, Height: 40})
	cleanupUITestDriver(t, narrow)
	if err := narrow.Key("C"); err != nil {
		t.Fatalf("Key(C) error: %v", err)
	}
	nobs := narrow.Observe()
	if nobs.Mode != "config_picker" {
		t.Fatalf("expected config_picker mode after C, got %q", nobs.Mode)
	}
	if !hasIssueType(nobs.Screen.Issues, UITestIssueModalWidthOverflow) {
		t.Errorf("expected modal_width_overflow for the config picker at 120 wide, got %v", nobs.Screen.Issues)
	}

	// Wide: the box grows to fit its widest row (the sizing fix), so no issue.
	wide, _ := newUITestFixtureDriver(fx, UITestSize{Width: 140, Height: 40})
	cleanupUITestDriver(t, wide)
	if err := wide.Key("C"); err != nil {
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
