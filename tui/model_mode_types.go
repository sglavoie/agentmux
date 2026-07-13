package tui

// Mode represents the current UI mode.
type Mode int

const (
	ModeList          Mode = iota
	ModeSearch             // typing a filter expression (DSL)
	ModeConfirm            // y/n confirmation for a destructive action
	ModePicker             // multi-select session launcher
	ModeConfigPicker       // account picker for config override
	ModeHelp               // help overlay
	ModeDetail             // detail pane: per-session hook event log
	ModeActions            // per-row actions overlay
	ModeSessionSearch      // searchable list of past harness transcripts
	ModeSessionView        // scrollable transcript viewer for a single past session
	ModeDeleteChoice       // three-way prompt: hide from index, also delete file, cancel
	ModeChipPicker         // overlay listing chip values for ModeSessionSearch
	ModeMessageDetail      // full-message overlay opened from ModeSessionView
	ModePlanSearch         // searchable list of past agent-plan directories
	ModePlanView           // inline pager for a single plan directory
	ModeWorkspace          // project-centric workspace home (secondary dashboard view)
	ModeUnifiedSearch      // cross-entity FTS5 search opened from the command palette
	ModePalette            // global context-aware command and navigation palette
)
