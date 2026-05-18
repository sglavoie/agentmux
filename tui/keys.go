package tui

// Key string constants used in KeyPressMsg switch cases.
const (
	keyUp          = "up"
	keyDown        = "down"
	keyJ           = "j"
	keyK           = "k"
	keyG           = "g"
	keyGG          = "G"
	keySlash       = "/"
	keyF           = "f"
	keyA           = "a"
	keyO           = "o"
	keyL           = "l"
	keyY           = "y"
	keyN           = "n"
	keySpace       = "space"
	keyEnter       = "enter"
	keyEsc         = "esc"
	keyQ           = "q"
	keyCtrlC       = "ctrl+c"
	keyE           = "e"
	keyX           = "x"
	keyR           = "r"
	keyU           = "u" // undo the most recent destructive action
	keyBackspace   = "backspace"
	keyQuestion    = "?" // opens ModeHelp overlay
	keyC           = "c" // opens ModeConfigPicker for profile override
	keyClearStatus = "C" // clears the highlighted session's icon
	keyResort      = "R" // re-sort tabs in Kitty to repair drifted physical order
	keyD           = "d" // dismisses the config deprecation banner

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
