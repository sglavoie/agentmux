package kittykeys

// Workspace is the keybinding reference of Agent Mux.app's workspace: Kitty's
// function keys as the app's main menu binds them
// (macos/AgentMux/UI/WorkspaceMenu.swift), with the handful of ⌘ shortcuts
// that stand in for Kitty's own. The app runs each one itself, so only Key,
// Section, and Description are set and none has a ConfigLine.
//
// TestWorkspaceFunctionKeysMatchTheApp holds the function keys listed here to
// the ones the app binds.
var Workspace = []Action{
	// Views
	{Key: "f1", Section: "Views", Description: "show this keybinding reference"},
	{Key: "f2", Section: "Views", Description: "show the live session overview"},
	{Key: "cmd+d", Section: "Views", Description: "show the Agent Mux dashboard"},
	// Session management
	{Key: "f5", Section: "Sessions", Description: "set session harness icon (picker)"},
	{Key: "f6", Section: "Sessions", Description: "set session color (picker)"},
	{Key: "cmd+r", Section: "Sessions", Description: "name the shown session (an empty name removes it)"},
	{Key: "cmd+shift+w", Section: "Sessions", Description: "end the shown session after confirmation"},
	{Key: "f8", Section: "Sessions", Description: "clear markers and state icons from session"},
	{Key: "f11", Section: "Sessions", Description: "mark session active (⚡ until the next real event)"},
	{Key: "f10", Section: "Sessions", Description: "toggle mute on the shown session (suppresses notifications)"},
	{Key: "f4", Section: "Sessions", Description: "focus next session needing attention"},
	// Scrollback / terminal
	{Key: "f3", Section: "Scrollback", Description: "toggle error/failure/warning/deprecated markers"},
	// Navigation
	{Key: "f7", Section: "Navigation", Description: "focus previous session"},
	{Key: "f9", Section: "Navigation", Description: "focus next session"},
	{Key: "cmd+1 … cmd+0", Section: "Navigation", Description: "switch to session 1 … 10"},
	{Key: "cmd+l", Section: "Navigation", Description: "toggle between two most recently shown sessions or views"},
	{Key: "cmd+p", Section: "Navigation", Description: "go to a session or view by name"},
}
