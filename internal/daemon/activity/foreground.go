package activity

import (
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// Foreground classification: which harness, if any, a Kitty pane is actually
// RUNNING right now. This is the daemon's only source of activity evidence that
// does not depend on the harness cooperating — a pane with no hooks installed,
// no user vars, and no transcript still names its own process — which is why it
// gates the screen probes' eligibility and the snapshot's change signature.

// ForegroundHarness names the harness this pane is running, if any, preferring
// the pane's own command line over its foreground processes. The second result
// reports whether the invocation is a non-interactive pipe-mode run, which the
// probes must never settle as if it were an interactive session.
func ForegroundHarness(win *kitty.Window) (string, bool) {
	if name, pipeMode, ok := harnessInvocationFromCmdline(win.Cmdline); ok {
		return name, pipeMode
	}
	for i := range win.ForegroundProcesses {
		if name, pipeMode, ok := harnessInvocationFromCmdline(win.ForegroundProcesses[i].Cmdline); ok {
			return name, pipeMode
		}
	}
	return "", false
}

// PaneForegroundHarness is ForegroundHarness for a pane discovered through any
// backend: the first foreground process whose command line is a known harness.
// A backend that reports only PIDs (tmux names the pane's root process, not
// its argv) yields no harness here, and the caller falls back to the pane's
// launch metadata.
func PaneForegroundHarness(pane backend.Pane) (string, bool) {
	for i := range pane.Foreground {
		if name, pipeMode, ok := harnessInvocationFromCmdline(pane.Foreground[i].Argv); ok {
			return name, pipeMode
		}
	}
	return "", false
}

// harnessInvocationFromCmdline reads one command line as a harness invocation:
// the normalized harness name, whether the arguments make it a pipe-mode run,
// and whether the command was a known harness at all.
func harnessInvocationFromCmdline(cmdline []string) (string, bool, bool) {
	if len(cmdline) == 0 {
		return "", false, false
	}
	name := harness.NormalizeKnown(filepath.Base(cmdline[0]))
	if name == "" {
		return "", false, false
	}
	return name, harness.InvocationIsPipeMode(name, cmdline[1:]), true
}
