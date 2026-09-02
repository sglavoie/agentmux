package probes

import (
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// Freshness is what makes an external observation safe to act on. A probe reads
// a pane's screen off-lock, so between capture and application the tab may have
// been retitled, the pane may have moved, another pane may have taken title
// ownership, or the harness may have started or finished a turn. Each of those
// advances an epoch, and a binding that quotes a superseded epoch is dropped
// rather than applied.
//
// The signatures below are deliberately derived projections rather than raw
// Kitty data: they name only the facts probes actually consume, so ordinary
// churn (a spinner frame, a shell command in a neighbouring split) does not
// invalidate an in-flight read.

// tabState is the immutable, probe-relevant projection of one Kitty tab plus
// its monotonic freshness epoch and the per-pane epochs of the panes it
// currently holds. Nesting the pane map under the tab makes a pane that moved to
// another tab fail closed without a separate residency check.
type tabState struct {
	epoch     uint64
	signature tabSignature
	windows   map[int]windowState
}

// tabSignature holds the facts every probe in the tab shares. Pane ownership
// stays here because FindTabTitleOwner resolves to the active managed pane, else
// the first managed pane: a third pane becoming active can move ownership
// without touching the probed pane's own flags.
type tabSignature struct {
	osWindowID       int
	osWindowActive   bool
	osWindowFocused  bool
	osWindowLastUsed bool
	tabActive        bool
	title            string
	ownership        []ownershipSignature
}

type ownershipSignature struct {
	id               int
	active           bool
	effectiveProfile string
	effectiveHarness string
}

type windowState struct {
	epoch     uint64
	signature windowSignature
}

// windowSignature is a derived projection rather than raw argv. A plain pane
// rewrites its foreground processes on every shell command and a routed source
// pane rewrites them on every tool the harness spawns, so raw argv makes probes
// self-invalidate mid-read. The projection keeps only facts probes consume:
// harness/pipe identity plus Claude's normalized busy-title class, never the
// rotating marker or task text itself.
type windowSignature struct {
	mute               string
	foregroundHarness  string
	foregroundPipeMode bool
	claudeBusyTitle    bool
}

func tabSignatureFor(osWindow *kitty.OSWindow, tab *kitty.Tab) tabSignature {
	signature := tabSignature{
		osWindowID:       osWindow.ID,
		osWindowActive:   osWindow.IsActive,
		osWindowFocused:  osWindow.IsFocused,
		osWindowLastUsed: osWindow.LastFocused,
		tabActive:        tab.IsActive,
		title:            tab.Title,
		ownership:        make([]ownershipSignature, 0, len(tab.Windows)),
	}
	for i := range tab.Windows {
		win := &tab.Windows[i]
		signature.ownership = append(signature.ownership, ownershipSignature{
			id:               win.ID,
			active:           win.IsActive,
			effectiveProfile: win.UserVars.EffectiveProfile(),
			effectiveHarness: win.UserVars.EffectiveHarness(),
		})
	}
	return signature
}

func windowSignatureFor(win *kitty.Window) windowSignature {
	signature := windowSignature{
		mute:            win.UserVars.Mute,
		claudeBusyTitle: activity.ClaudeWindowTitleHasDescriptionMarker(win.Title),
	}
	signature.foregroundHarness, signature.foregroundPipeMode = activity.ForegroundHarness(win)
	return signature
}
