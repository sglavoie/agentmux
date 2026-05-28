package tui

// Key string constants used in KeyPressMsg switch cases.
const (
	keyUp            = "up"
	keyDown          = "down"
	keyJ             = "j"
	keyK             = "k"
	keyG             = "g"
	keyGG            = "G"
	keySlash         = "/"
	keyF             = "f"
	keyA             = "a"
	keyO             = "o"
	keyL             = "l"
	keyY             = "y"
	keyN             = "n"
	keySpace         = "space"
	keyEnter         = "enter"
	keyEsc           = "esc"
	keyQ             = "q"
	keyCtrlC         = "ctrl+c"
	keyE             = "e"
	keyX             = "x"
	keyR             = "r"
	keyU             = "u" // undo the most recent destructive action
	keyBackspace     = "backspace"
	keyQuestion      = "?" // opens ModeHelp overlay
	keyC             = "c" // opens ModeConfigPicker for profile override
	keyClearStatus   = "C" // clears the highlighted session's icon
	keyOpenEditor    = "O" // opens the project directory in the configured editor
	keyResort        = "R" // re-sort tabs in Kitty to repair drifted physical order
	keyLock          = "L" // toggle the session lock (blocks close/restart/override)
	keyM             = "m" // toggle session mute (suppresses notifications)
	keyD             = "d" // dismisses the config deprecation banner; otherwise opens ModeDetail
	keySessionSearch = "S" // opens ModeSessionSearch (past harness transcripts)
	keyPlanSearch    = "P" // opens ModePlanSearch (past agent plans)

	keyToggleTimeline   = "t" // toggle flat list ↔ timeline in ModeDetail
	keyToggleTimestamps = "T" // toggle timestamp column in timeline (ModeDetail)
	keyPhasePrev        = "[" // jump to previous phase header in timeline
	keyPhaseNext        = "]" // jump to next phase header in timeline
	// keyOpenTimeline is the same key string as keyToggleTimestamps; disambiguation is by mode.
	// In ModeList: open detail pane directly in timeline mode.
	keyOpenTimeline = "T"

	keyPgUp   = "pgup"   // scroll detail pane up by one page
	keyPgDown = "pgdown" // scroll detail pane down by one page
	keyCtrlD  = "ctrl+d" // list: cursor down by half a page
	keyCtrlU  = "ctrl+u" // list: cursor up by half a page

	keyTogglePreview = "p" // toggles the split-pane live preview

	keyTab = "tab"

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
