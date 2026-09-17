package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
)

// actionsModalAt returns a model with the per-row actions overlay open, sized
// to width x height. The actions box grows to fit its widest description row, so
// it is the natural probe for width-fit detection.
func actionsModalAt(width, height int) Model {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "a")
	m.width, m.height = width, height
	return m
}

// configPickerModalAt returns a model with the profile picker open over long
// profile rows (long names + home dirs) so the box must grow well past its
// 60-column default. width x height set the viewport.
func configPickerModalAt(width, height int) Model {
	m := testModel()
	m.cfg.HarnessEmojis = map[string]string{"claude": "⭕", "codex": "📟"}
	m.cfg.Accounts = []config.Account{
		{Name: "tradition-claude-long", Color: "blue", Harness: "claude", HomeDir: "/Users/sglavoie/.claude-personal-home"},
		{Name: "personal-codex", Color: "purple", Harness: "codex", HomeDir: "/Users/sglavoie/.codex"},
	}
	m.targetSession = session.RunningSession{TabID: 1, Base: "api", Project: "api", Account: "tradition-claude-long", Harness: "claude"}
	m.configPickerAccounts = m.cfg.Accounts
	m.configPickerCurrent = "tradition-claude-long"
	m.configPickerCursor = 0
	m.configPickerPurpose = launch.ConfigPickerRestartProfile
	m.configPickerReturnMode = ModeList
	m.mode = ModeConfigPicker
	m.width, m.height = width, height
	return m
}

func hasIssueType(issues []UITestLayoutIssue, t UITestLayoutIssueType) bool {
	for _, iss := range issues {
		if iss.Type == t {
			return true
		}
	}
	return false
}

// TestUITestModalNoIssuesWhenFits verifies a modal at a comfortably large
// terminal — where its natural width equals its rendered width and its rows fit
// the body area — reports no modal-fit issues.
func TestUITestModalNoIssuesWhenFits(t *testing.T) {
	if iss := detectModalFitIssues(actionsModalAt(140, 50)); len(iss) != 0 {
		t.Errorf("actions overlay at 140x50 should fit, got %v", iss)
	}
	if iss := detectModalFitIssues(configPickerModalAt(140, 50)); len(iss) != 0 {
		t.Errorf("config picker at 140x50 should fit, got %v", iss)
	}
}

// TestUITestActionsMenuFitsEveryTargetSize is the positive form of the layout
// guarantee the selectable menu is required to hold: at each of the four target
// sizes the whole menu is on screen, with no modal-fit issue of any kind and no
// scrolling.
//
// It replaces an assertion that the overlay OVERFLOWED at 80x24. That assertion
// pinned the old wrapped-description legend, whose widest row could not fit, and
// leaving it in place would have meant the menu passed its acceptance by
// continuing to be clipped. The detector itself is unweakened: the genuine
// clipping cases are TestUITestModalConfigPickerWidthOverflow below, which still
// reports modal_width_overflow for a real overflowing box, and the synthetic
// line/height overflow tests in uitest_screen_test.go.
func TestUITestActionsMenuFitsEveryTargetSize(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {100, 24}, {120, 30}, {160, 45}} {
		m := actionsModalAt(size.w, size.h)
		if iss := detectModalFitIssues(m); len(iss) != 0 {
			t.Errorf("actions menu at %dx%d reports %v, want it to fit", size.w, size.h, iss)
		}
		in := actionsInput(m)
		rows, visible := in.ScrollBounds()
		if visible < len(rows) {
			t.Errorf("actions menu at %dx%d shows %d of %d rows, want the whole menu without scrolling",
				size.w, size.h, visible, len(rows))
		}
	}
}

// TestUITestModalConfigPickerWidthOverflow pins the profile-picker sizing
// regression: at a terminal narrower than its widest profile row the picker
// reports modal_width_overflow, and at a wide terminal it does not.
func TestUITestModalConfigPickerWidthOverflow(t *testing.T) {
	if iss := detectModalFitIssues(configPickerModalAt(60, 40)); !hasIssueType(iss, UITestIssueModalWidthOverflow) {
		t.Errorf("expected modal_width_overflow for config picker at 60x40, got %v", iss)
	}
	if iss := detectModalFitIssues(configPickerModalAt(140, 50)); hasIssueType(iss, UITestIssueModalWidthOverflow) {
		t.Errorf("config picker at 140x50 should not overflow width, got %v", iss)
	}
}

// TestUITestModalDetectsHeightOverflow verifies that a modal taller than the
// body area root composition gives it is reported as modal_height_overflow.
//
// The probe height moved from 8 rows to 4 because the menu now sheds its own
// chrome — separator, context line, detail rows, hint — before it will render
// taller than the body, so 8 rows is a fit rather than an overflow. Four rows
// leaves a body of two, which cannot hold the box frame itself, and the detector
// must still say so: this is the case the shed cannot rescue, and suppressing it
// would be the weakened check. The paired positive assertion is the line below.
func TestUITestModalDetectsHeightOverflow(t *testing.T) {
	iss := detectModalFitIssues(actionsModalAt(120, 4))
	if !hasIssueType(iss, UITestIssueModalHeightOverflow) {
		t.Fatalf("expected modal_height_overflow for actions at 120x4, got %v", iss)
	}
	for _, i := range iss {
		if i.Type == UITestIssueModalHeightOverflow && i.Actual <= i.Limit {
			t.Errorf("height overflow should record rendered rows (%d) above body height (%d)", i.Actual, i.Limit)
		}
	}
	// And the shed is real rather than a suppressed report: a viewport with room
	// for the box fits it, entries and all.
	if fit := detectModalFitIssues(actionsModalAt(120, 8)); len(fit) != 0 {
		t.Errorf("actions menu at 120x8 reports %v, want the chrome shed to make it fit", fit)
	}
	short := actionsModalAt(120, 8)
	if _, visible := actionsInput(short).ScrollBounds(); visible < 1 {
		t.Error("a short viewport shows no entries at all; the shed must leave at least one")
	}
}

// TestUITestModalTopAnchoredBoundaryFits sweeps the top-anchored config picker
// across body heights. When the picker is too tall it reports height overflow;
// at and above the exact fit boundary it anchors at a row that keeps the bottom
// border visible, so modal_clipped_by_root should not appear.
func TestUITestModalTopAnchoredBoundaryFits(t *testing.T) {
	var sawHeight, sawClean bool
	for h := 6; h <= 16; h++ {
		iss := detectModalFitIssues(configPickerModalAt(120, h))
		if len(iss) > 1 {
			t.Fatalf("h=%d: expected at most one vertical issue, got %v", h, iss)
		}
		height := hasIssueType(iss, UITestIssueModalHeightOverflow)
		clip := hasIssueType(iss, UITestIssueModalClippedByRoot)
		if height && clip {
			t.Fatalf("h=%d: height overflow and placement clip must be mutually exclusive", h)
		}
		if clip {
			t.Fatalf("h=%d: top-anchored picker should avoid placement clipping, got %v", h, iss)
		}
		switch {
		case height:
			sawHeight = true
		case len(iss) == 0:
			sawClean = true
		}
	}
	if !sawHeight {
		t.Error("expected some short terminal to report modal_height_overflow")
	}
	if !sawClean {
		t.Error("expected some terminal to fit the config picker with no issues")
	}
}

// TestUITestModalIgnoresNonModalModes verifies non-modal modes (and the
// full-layout message-detail overlay, which is not a bounded box) yield no
// modal-fit issues, so the detector never fabricates issues for views whose
// overflow is already covered by the screen-level checks.
func TestUITestModalIgnoresNonModalModes(t *testing.T) {
	list := loadSessions(testModelWithSessions(), makeSessions())
	list.width, list.height = 40, 10
	if iss := detectModalFitIssues(list); iss != nil {
		t.Errorf("ModeList should have no modal-fit issues, got %v", iss)
	}

	search := list
	search.mode = ModeSearch
	if iss := detectModalFitIssues(search); iss != nil {
		t.Errorf("ModeSearch should have no modal-fit issues, got %v", iss)
	}

	msg := list
	msg.mode = ModeMessageDetail
	if iss := detectModalFitIssues(msg); iss != nil {
		t.Errorf("ModeMessageDetail is a full-layout view, expected no modal-fit issues, got %v", iss)
	}
}

// TestUITestModalUnsizedModelIsSkipped verifies the detector is a no-op before
// the first WindowSizeMsg lands (m.width == 0), so a pre-sized render never
// produces spurious overflow.
func TestUITestModalUnsizedModelIsSkipped(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "a")
	m.width, m.height = 0, 0
	if iss := detectModalFitIssues(m); iss != nil {
		t.Errorf("unsized model should yield no modal-fit issues, got %v", iss)
	}
}

// TestUITestModalBoxRendererCoversOverlayModes guards the mode→renderer map
// against drift: every mode model_preview.go composites as a bounded popup must
// resolve to a renderer, and plainly non-modal modes must not.
func TestUITestModalBoxRendererCoversOverlayModes(t *testing.T) {
	modal := []Mode{
		ModeActions, ModeConfirm, ModeConfigPicker, ModeHelp,
		ModeDetail, ModeDeleteChoice, ModeChipPicker, ModeCommandPalette,
		ModeLiveSessionPicker,
	}
	for _, mode := range modal {
		if modalBoxRenderer(mode) == nil {
			t.Errorf("mode %s should map to a bounded-popup renderer", uitestModeName(mode))
		}
	}
	for _, mode := range []Mode{ModeList, ModeSearch, ModePicker, ModeMessageDetail, ModeSessionSearch} {
		if modalBoxRenderer(mode) != nil {
			t.Errorf("mode %s should not map to a bounded-popup renderer", uitestModeName(mode))
		}
	}
}
