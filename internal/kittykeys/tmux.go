package kittykeys

import "strings"

// Tmux is the keybinding table of a plain terminal attached to a session on
// the private tmux server: Kitty's function keys and its dashboard key, bound
// in the server's configuration (internal/tmux.Config) instead of kitty.conf.
// What Kitty did with an overlay tmux does with a popup over the terminal
// that pressed the key, and what Kitty launched in the background tmux runs
// with run-shell. Either way the program is amx, started in the environment
// of the session the terminal is on, so it knows its runtime session without
// being told; a command that moves the terminal is told which one pressed
// the key.
//
// A key with no AmxCommand is one the configuration binds by hand: F3 marks
// the scrollback as the viewer's history key does, and F12 detaches.
//
// TestTmuxFunctionKeysMatchKitty holds this table to Actions.
var Tmux = []Action{
	// Overlays
	{Key: "f1", Section: "Overlays", Description: "show this keybinding reference", AmxCommand: "keybindings", LaunchType: tmuxPopup},
	{Key: "f2", Section: "Overlays", Description: "open the live session overview", AmxCommand: "overview --popup", LaunchType: tmuxPopup},
	{Key: "ctrl+shift+d", Section: "Overlays", Description: "open the Agent Mux dashboard", AmxCommand: "--popup", LaunchType: tmuxPopup},
	{Key: "ctrl+shift+y", Section: "Overlays", Description: "open Past Sessions directly", AmxCommand: "past-sessions --popup", LaunchType: tmuxPopup},
	// Session management
	{Key: "f5", Section: "Sessions", Description: "set session harness icon (picker)", AmxCommand: "icon-picker", LaunchType: tmuxPopup},
	{Key: "f6", Section: "Sessions", Description: "set session color (picker)", AmxCommand: "color-picker", LaunchType: tmuxPopup},
	{Key: "f8", Section: "Sessions", Description: "clear markers and state icons from session", AmxCommand: "hook-indicator user_clear", LaunchType: "background"},
	{Key: "f11", Section: "Sessions", Description: "mark session active (⚡ until the next real event)", AmxCommand: "hook-indicator user_active", LaunchType: "background"},
	{Key: "f10", Section: "Sessions", Description: "toggle mute on the shown session (suppresses notifications)", AmxCommand: "mute-toggle", LaunchType: "background"},
	{Key: "f4", Section: "Sessions", Description: "focus next session needing attention", AmxCommand: "focus-next " + tmuxClient, LaunchType: "background"},
	// Scrollback / terminal
	{Key: "f3", Section: "Scrollback", Description: "toggle error/failure/warning/deprecated markers"},
	// Navigation
	{Key: "f7", Section: "Navigation", Description: "focus previous session", AmxCommand: "focus-prev-managed " + tmuxClient, LaunchType: "background"},
	{Key: "f9", Section: "Navigation", Description: "focus next session", AmxCommand: "focus-next-managed " + tmuxClient, LaunchType: "background"},
	{Key: "f12", Section: "Navigation", Description: "detach this terminal (the agent keeps running)"},
}

// TmuxAmxEnv is the variable of the tmux server's environment naming the amx
// executable the keys run. A server keeps the PATH of whatever started it,
// which for one started by the desktop app holds no amx; the terminal that
// attaches says where its own is (tmux.AttachView.Amx). Unset, the keys run
// the amx on that PATH.
const TmuxAmxEnv = "AMX_BIN"

// tmuxAmx is amx as a key's shell command names it.
const tmuxAmx = `"${` + TmuxAmxEnv + `:-amx}"`

const (
	// tmuxPopup is the LaunchType of a key that opens a popup.
	tmuxPopup = "popup"
	// tmuxClient names the terminal that pressed the key. run-shell expands
	// the format; a popup's command is not expanded, which is why no popup
	// carries it.
	tmuxClient = `--client "#{client_name}"`
)

// TmuxKey is key as tmux spells it: "f9" is F9 and "ctrl+shift+d" is C-S-d.
func TmuxKey(key string) string {
	parts := strings.Split(key, "+")
	last := parts[len(parts)-1]
	if len(last) > 1 {
		last = strings.ToUpper(last[:1]) + last[1:]
	}
	var out strings.Builder
	for _, modifier := range parts[:len(parts)-1] {
		switch modifier {
		case "ctrl":
			out.WriteString("C-")
		case "shift":
			out.WriteString("S-")
		case "alt":
			out.WriteString("M-")
		}
	}
	return out.String() + last
}

// TmuxLine returns the tmux configuration line binding the action, or "" for
// a key the configuration binds by hand. The commands hold no single quote,
// so each is one single-quoted word, which /bin/sh runs: run-shell always
// uses it, and the configuration makes it the shell of a popup too.
//
// A background command says nothing and never fails as far as tmux can tell:
// run-shell would otherwise put its output over the agent's pane, or a
// "returned 1" on the terminal for a key that had nowhere to go.
func (a Action) TmuxLine() string {
	if a.AmxCommand == "" || strings.Contains(a.AmxCommand, "'") {
		return ""
	}
	bind := "bind-key -n " + TmuxKey(a.Key) + " "
	switch a.LaunchType {
	case tmuxPopup:
		return bind + "display-popup -E -w 100% -h 100% '" + tmuxAmx + " " + a.AmxCommand + "'"
	case "background":
		return bind + "run-shell -b '" + tmuxAmx + " " + a.AmxCommand + " >/dev/null 2>&1; true'"
	}
	return ""
}

// TmuxConfig is every generated binding of Tmux, one line each.
func TmuxConfig() string {
	var out strings.Builder
	for _, action := range Tmux {
		if line := action.TmuxLine(); line != "" {
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	return out.String()
}
