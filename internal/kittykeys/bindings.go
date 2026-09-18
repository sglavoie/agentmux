package kittykeys

import "strings"

// Action describes a single Kitty-level keybinding.
type Action struct {
	Key          string // Kitty key expression, e.g. "f8", "ctrl+shift+d"
	Description  string // One-line human-readable description
	Section      string // Grouping header for display
	AmxCommand   string // e.g. "focus-next --window-id @active-kitty-window-id"; empty for native Kitty actions or the amx root dashboard
	LaunchType   string // "overlay" | "background" | "" (native Kitty action)
	KittyCommand string // Native Kitty action expression, e.g. "goto_tab 1"; empty for amx launches
}

// Actions is the single source of truth for all Kitty-level keybindings.
var Actions = []Action{
	// Overlays
	{Key: "f1", Section: "Overlays", Description: "show this keybinding reference", AmxCommand: "keybindings", LaunchType: "overlay"},
	{Key: "f2", Section: "Overlays", Description: "open or focus the live session overview", AmxCommand: "overview --window-id @active-kitty-window-id", LaunchType: "overlay"},
	{Key: "ctrl+shift+d", Section: "Overlays", Description: "open or focus Agent Mux dashboard", AmxCommand: "--window-id @active-kitty-window-id", LaunchType: "overlay"},
	// Session management
	{Key: "f5", Section: "Sessions", Description: "set tab harness icon (overlay picker)", AmxCommand: "icon-picker", LaunchType: "overlay"},
	{Key: "f6", Section: "Sessions", Description: "set tab color (overlay picker)", AmxCommand: "color-picker", LaunchType: "overlay"},
	{Key: "f8", Section: "Sessions", Description: "clear markers and state icons from tab", AmxCommand: "hook-indicator user_clear --window-id @active-kitty-window-id", LaunchType: "background"},
	{Key: "f10", Section: "Sessions", Description: "toggle mute on the focused tab (suppresses notifications)", AmxCommand: "mute-toggle --window-id @active-kitty-window-id", LaunchType: "background"},
	{Key: "f4", Section: "Sessions", Description: "focus next tab needing attention", AmxCommand: "focus-next --window-id @active-kitty-window-id", LaunchType: "background"},
	// Scrollback / terminal
	{Key: "f3", Section: "Scrollback", Description: "toggle error/failure/warning/deprecated markers", KittyCommand: `toggle_marker iregex 1 \\bError\\b 2 \\bFailed\\b 3 \\bWarning\\b 3 \\bDEPRECATED\\b`},
	// Tab navigation
	{Key: "f7", Section: "Navigation", Description: "focus previous managed tab", AmxCommand: "focus-prev-managed --window-id @active-kitty-window-id", LaunchType: "background"},
	{Key: "f9", Section: "Navigation", Description: "focus next managed tab", AmxCommand: "focus-next-managed --window-id @active-kitty-window-id", LaunchType: "background"},
	{Key: "cmd+1", Section: "Navigation", Description: "switch to tab 1", KittyCommand: "goto_tab 1"},
	{Key: "cmd+2", Section: "Navigation", Description: "switch to tab 2", KittyCommand: "goto_tab 2"},
	{Key: "cmd+3", Section: "Navigation", Description: "switch to tab 3", KittyCommand: "goto_tab 3"},
	{Key: "cmd+4", Section: "Navigation", Description: "switch to tab 4", KittyCommand: "goto_tab 4"},
	{Key: "cmd+5", Section: "Navigation", Description: "switch to tab 5", KittyCommand: "goto_tab 5"},
	{Key: "cmd+6", Section: "Navigation", Description: "switch to tab 6", KittyCommand: "goto_tab 6"},
	{Key: "cmd+7", Section: "Navigation", Description: "switch to tab 7", KittyCommand: "goto_tab 7"},
	{Key: "cmd+8", Section: "Navigation", Description: "switch to tab 8", KittyCommand: "goto_tab 8"},
	{Key: "cmd+9", Section: "Navigation", Description: "switch to tab 9", KittyCommand: "goto_tab 9"},
	{Key: "cmd+0", Section: "Navigation", Description: "switch to tab 10", KittyCommand: "goto_tab 10"},
	{Key: "cmd+l", Section: "Navigation", Description: "toggle between two most recently used tabs", KittyCommand: "goto_tab -1"},
}

// ConfigLine returns the Kitty config map line for the action.
func (a Action) ConfigLine() string {
	if a.KittyCommand != "" {
		return strings.Join([]string{"map", a.Key, a.KittyCommand}, " ")
	}

	if a.LaunchType == "" {
		return ""
	}

	parts := []string{"map", a.Key, "launch", "--type=" + a.LaunchType, "--allow-remote-control", "amx"}
	if a.AmxCommand != "" {
		parts = append(parts, a.AmxCommand)
	}
	return strings.Join(parts, " ")
}
