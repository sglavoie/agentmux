package tui

// This file captures a normalized, JSON-ready observation of the live model.
// It bridges the pure contract in uitest_types.go (UITestScreen, UITestSize) to
// the real dashboard Model, so the in-process driver can record what the screen
// looked like after each scenario step without any terminal event loop.

// UITestObservation is a JSON-ready snapshot of the model after an action: the
// terminal size the model was rendered at, the active UI mode, the cursor
// position relevant to that mode, and the normalized screen with any layout
// issues attached — both the screen-level checks (line/height overflow, invalid
// UTF-8) and the model-aware modal-fit issues from uitest_modal.go.
type UITestObservation struct {
	Size   UITestSize   `json:"size"`
	Mode   string       `json:"mode"`
	Cursor int          `json:"cursor,omitempty"`
	Screen UITestScreen `json:"screen"`
}

// observeUITest renders m at the given size and normalizes the result into a
// stable observation. size is passed explicitly (rather than read from m.width /
// m.height) so callers can record the size they intended even if a resize is
// still settling. The screen carries both screen-level and modal-fit layout
// issues via captureUITestScreen.
func observeUITest(m Model, size UITestSize) UITestObservation {
	return UITestObservation{
		Size:   size,
		Mode:   uitestModeName(m.mode),
		Cursor: uitestCursor(m),
		Screen: captureUITestScreen(m, size),
	}
}

// uitestCursor reports the cursor position that matters for the active mode.
// Most modes track the dashboard list cursor; the picker and config picker keep
// their own so scenarios can assert on the row the user is actually navigating.
func uitestCursor(m Model) int {
	switch m.mode {
	case ModePicker:
		return m.pickerCursor
	case ModeConfigPicker:
		return m.configPickerCursor
	case ModeCommandPalette:
		return m.commandCursor
	case ModeWorktrees:
		return m.worktreeCursor
	case ModeWorktreeSessionPicker:
		return m.worktreeSessionCursor
	default:
		return m.cursor
	}
}

// uitestModeName maps a Mode to a stable, lower-snake JSON string. New modes
// must be added here so scenario output never carries a bare integer; unknown
// values fall back to "unknown" rather than panicking.
func uitestModeName(mode Mode) string {
	switch mode {
	case ModeList:
		return "list"
	case ModeSearch:
		return "search"
	case ModeConfirm:
		return "confirm"
	case ModePicker:
		return "picker"
	case ModeConfigPicker:
		return "config_picker"
	case ModeHelp:
		return "help"
	case ModeDetail:
		return "detail"
	case ModeActions:
		return "actions"
	case ModeSessionSearch:
		return "session_search"
	case ModeSessionView:
		return "session_view"
	case ModeDeleteChoice:
		return "delete_choice"
	case ModeChipPicker:
		return "chip_picker"
	case ModeMessageDetail:
		return "message_detail"
	case ModeWorktrees:
		return "worktrees"
	case ModePlanSearch:
		return "plan_search"
	case ModePlanView:
		return "plan_view"
	case ModeCommandPalette:
		return "command_palette"
	case ModeWorktreeSessionPicker:
		return "worktree_session_picker"
	default:
		return "unknown"
	}
}
