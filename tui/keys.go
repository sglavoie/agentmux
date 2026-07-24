package tui

// Key string constants used in KeyPressMsg switch cases.
const (
	keyUp               = "up"
	keyDown             = "down"
	keyJ                = "j"
	keyK                = "k"
	keyG                = "g"
	keyGG               = "G"
	keySlash            = "/"
	keyColon            = ":" // opens ModeCommandPalette from ModeList only
	keyF                = "f"
	keyWorktreeFork     = "w" // ModeSessionSearch: confirm an isolated Git worktree fork
	keyA                = "a"
	keyO                = "o"
	keyL                = "l"
	keyY                = "y"
	keyCopyPath         = "Y"
	keyN                = "n"
	keySpace            = "space"
	keyEnter            = "enter"
	keyEsc              = "esc"
	keyQ                = "q"
	keyCtrlC            = "ctrl+c"
	keyE                = "e"
	keyClose            = "X" // closes the highlighted session
	keyR                = "r"
	keyU                = "u" // undo the most recent destructive action
	keyBackspace        = "backspace"
	keyQuestion         = "?" // opens ModeHelp overlay
	keyC                = "c" // opens ModeConfigPicker for profile override
	keyDashboardLaunch  = "C" // opens ModeConfigPicker for dashboard launch
	keyClearStatus      = "x" // clears the highlighted session's icon
	keyOpenEditor       = "O" // opens the project directory in the configured editor
	keyResort           = "R" // re-sort tabs in Kitty to repair drifted physical order
	keyLock             = "L" // toggle the session lock (blocks close/restart/override)
	keyM                = "m" // toggle session mute (suppresses notifications)
	keyD                = "d" // dismisses the config deprecation banner; otherwise opens ModeDetail
	keySessionSearch    = "S" // opens ModeSessionSearch (past harness transcripts)
	keyPlanSearch       = "P" // opens ModePlanSearch (past agent plans)
	keyPinProject       = "P" // ModePicker: pin/unpin the highlighted configured project
	keySortPlan         = "s" // cycles the ModePlanSearch result-sort mode
	keyToggleTimeline   = "t" // toggle flat list ↔ timeline in ModeDetail
	keyToggleTimestamps = "T" // toggle timestamp column in timeline (ModeDetail)
	keyPhasePrev        = "[" // jump to previous phase header in timeline
	keyPhaseNext        = "]" // jump to next phase header in timeline
	// keyJumpAttentionPrev/Next: in ModeList, jumps the cursor to the
	// previous/next session whose state is in the attention set. Shares the
	// same string values as keyPhasePrev/keyPhaseNext above; disambiguation
	// is by mode, exactly like keyOpenTimeline below.
	keyJumpAttentionPrev = "["
	keyJumpAttentionNext = "]"
	// keyJumpAttentionPrevFocus/NextFocus: jump cursor like
	// keyJumpAttentionPrev/Next, then focus the Kitty tab. Useful for
	// "address the next attention item" in one keystroke.
	keyJumpAttentionPrevFocus = "{"
	keyJumpAttentionNextFocus = "}"
	// keyOpenTimeline is the same key string as keyToggleTimestamps; disambiguation is by mode.
	// In ModeList: open detail pane directly in timeline mode.
	keyOpenTimeline = "T"

	keyPgUp   = "pgup"   // move up by one page in scrollable views
	keyPgDown = "pgdown" // move down by one page in scrollable views
	keyCtrlD  = "ctrl+d" // move down by half a page in scrollable views
	keyCtrlU  = "ctrl+u" // move up by half a page in scrollable views
	keyCtrlF  = "ctrl+f" // move down by one page in scrollable views
	keyCtrlB  = "ctrl+b" // move up by one page in scrollable views
	keyCtrlJ  = "ctrl+j" // move down in searchable command menus
	keyCtrlK  = "ctrl+k" // move up in searchable command menus
	keyCtrlN  = "ctrl+n" // move down in searchable command menus
	keyCtrlP  = "ctrl+p" // move up in searchable command menus

	keyTogglePreview = "p" // toggles the split-pane live preview

	keyTab      = "tab"
	keyShiftTab = "shift+tab"

	// Numeric chip-removal keys (ModeList only). 1 removes the first chip,
	// 2 the second, and so on up to 9.
	key1 = "1"
	key2 = "2"
	key3 = "3"
	key4 = "4"
	key5 = "5"
	key6 = "6"
	key7 = "7"
	key8 = "8"
	key9 = "9"
)

const keyProjectSessionSearch = "s" // opens ModeSessionSearch filtered to the selected project
