package tui

// Mode represents the current UI mode.
type Mode int

const (
	ModeList              Mode = iota
	ModeSearch                 // typing a filter expression (DSL)
	ModeConfirm                // y/n confirmation for a destructive action
	ModePicker                 // multi-select session launcher
	ModeConfigPicker           // account picker for config override
	ModeHelp                   // help overlay
	ModeDetail                 // detail pane: per-session hook event log
	ModeActions                // per-row actions overlay
	ModeSessionSearch          // searchable list of past harness transcripts
	ModeSessionView            // scrollable transcript viewer for a single past session
	ModeDeleteChoice           // three-way prompt: hide from index, also delete file, cancel
	ModeChipPicker             // overlay listing chip values for ModeSessionSearch
	ModeMessageDetail          // full-message overlay opened from ModeSessionView
	ModeWorktrees              // managed Git worktree inventory and non-destructive actions
	ModePlanSearch             // searchable list of past agent-plan directories
	ModePlanView               // inline pager for a single plan directory
	ModeCommandPalette         // searchable dashboard command catalog
	ModeLiveSessionPicker      // ambiguous live sessions for one target
	ModeASLRuns                // agent-session-loop active and actionable runs
	ModeASLResponse            // answer editor and closure buttons for one ASL question
	ModeProjectSwitcher        // overlay choosing the dashboard's project scope
	ModeFeedback               // retained action outcomes
	ModeHealth                 // scrollable details for every health check, from cached facts
	ModeReviewNote
	ModeSend      // text and keys for one tmux session, sent without attaching
	ModePlanRun   // choices for starting an agent-session-loop run of one plan
	ModeFlightLog // durable session statistics and milestone collection
)

// String names the mode in stable lower snake case ("list",
// "command_palette"). The scenario harness reports and asserts modes by these
// names, so a new mode must be added here or its observations read "unknown";
// an out-of-range value reads "unknown" rather than panicking.
func (mode Mode) String() string {
	switch mode {
	case ModeFlightLog:
		return "flight_log"
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
	case ModeLiveSessionPicker:
		return "live_session_picker"
	case ModeASLRuns:
		return "asl_runs"
	case ModeASLResponse:
		return "asl_response"
	case ModeReviewNote:
		return "review_note"
	case ModeSend:
		return "send"
	case ModePlanRun:
		return "plan_run"
	case ModeProjectSwitcher:
		return "project_switcher"
	case ModeFeedback:
		return "feedback"
	case ModeHealth:
		return "health"
	default:
		return "unknown"
	}
}

// LivePickerPurpose names the caller that raised ModeLiveSessionPicker. The
// picker only chooses among candidates; the purpose decides what Enter does
// with the chosen one and which root view the detour returns to.
type LivePickerPurpose int

const (
	LivePickerWorktreeReopen LivePickerPurpose = iota
	LivePickerASLFocus
)
