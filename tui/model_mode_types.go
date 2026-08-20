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
	ModeLiveSessionPicker      // ambiguous live Kitty sessions for one target
	ModeASLRuns                // agent-session-loop active and actionable runs
	ModeASLResponse            // answer editor and closure buttons for one ASL question
)

// LivePickerPurpose names the caller that raised ModeLiveSessionPicker. The
// picker only chooses among candidates; the purpose decides what Enter does
// with the chosen one and which root view the detour returns to.
type LivePickerPurpose int

const (
	LivePickerWorktreeReopen LivePickerPurpose = iota
	LivePickerASLFocus
)
