package kittykeys

// Action describes a single Kitty-level keybinding.
type Action struct {
	Key         string // Kitty key expression, e.g. "f8", "ctrl+shift+d"
	Description string // One-line human-readable description
	Section     string // Grouping header for display
	AmxCommand  string // e.g. "focus-next" — empty for native Kitty actions
	LaunchType  string // "overlay" | "background" | "" (native Kitty action)
}

// Actions is the single source of truth for all Kitty-level keybindings.
var Actions = []Action{
	// Overlays
	{Key: "f1", Section: "Overlays", Description: "show this keybinding reference", AmxCommand: "keybindings", LaunchType: "overlay"},
	{Key: "ctrl+shift+d", Section: "Overlays", Description: "open Agent Mux dashboard", LaunchType: "overlay"},
	// Session management
	{Key: "f6", Section: "Sessions", Description: "set tab color (overlay picker)", AmxCommand: "color-picker", LaunchType: "overlay"},
	{Key: "f8", Section: "Sessions", Description: "clear markers and state icons from tab", AmxCommand: "hook-indicator user_clear", LaunchType: "background"},
	{Key: "f4", Section: "Sessions", Description: "focus next tab needing attention", AmxCommand: "focus-next", LaunchType: "background"},
	// Scrollback / terminal
	{Key: "f3", Section: "Scrollback", Description: "toggle error/warning markers"},
	{Key: "f12", Section: "Scrollback", Description: "broadcast input to all windows in tab"},
	// Tab navigation
	{Key: "f7", Section: "Navigation", Description: "focus previous managed tab", AmxCommand: "focus-prev-managed", LaunchType: "background"},
	{Key: "f9", Section: "Navigation", Description: "focus next managed tab", AmxCommand: "focus-next-managed", LaunchType: "background"},
	{Key: "cmd+1 – cmd+0", Section: "Navigation", Description: "switch to tab 1–10"},
	{Key: "cmd+l", Section: "Navigation", Description: "toggle between two most recently used tabs"},
}
