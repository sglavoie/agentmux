package probes

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// CodexForegroundTarget picks the pane the Codex screen-prompt probe reads in
// a tab that has no managed title owner: the active pane that runs Codex,
// else the first one that does. A tab with a managed owner is the managed
// path's to probe, and a tab with no Codex pane has nothing to read, so both
// report false.
//
// It only interprets the captured window tree. What the screen found there
// means for the window (whether it may overwrite the icon, which event row it
// records) stays in the daemon root.
func CodexForegroundTarget(tab *kitty.Tab) (*kitty.Window, bool) {
	if _, ok := kitty.FindTabTitleOwner(tab); ok {
		return nil, false
	}
	if tab == nil {
		return nil, false
	}

	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if !codexShouldScan(*win) {
			continue
		}
		if fallback == nil {
			fallback = win
		}
		if win.IsActive {
			return win, true
		}
	}
	if fallback == nil {
		return nil, false
	}
	return fallback, true
}

// codexShouldScan accepts a pane tagged as Codex, or an untagged pane whose
// foreground is an interactive Codex. A pane tagged as another harness is
// never Codex's, whatever runs in it.
func codexShouldScan(win kitty.Window) bool {
	switch win.UserVars.EffectiveHarness() {
	case harness.Codex:
		return true
	case "":
		return codexInForeground(win)
	default:
		return false
	}
}

func codexInForeground(win kitty.Window) bool {
	if cmdlineIsInteractiveCodex(win.Cmdline) {
		return true
	}
	for _, proc := range win.ForegroundProcesses {
		if cmdlineIsInteractiveCodex(proc.Cmdline) {
			return true
		}
	}
	return false
}

// cmdlineIsInteractiveCodex accepts only an interactive Codex invocation. A
// non-interactive run such as `codex exec` is a background subprocess in an
// unmanaged pane, not a session the dashboard reports on.
func cmdlineIsInteractiveCodex(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}
	if !strings.EqualFold(filepath.Base(cmdline[0]), "codex") {
		return false
	}
	return !harness.InvocationIsPipeMode(harness.Codex, cmdline[1:])
}
