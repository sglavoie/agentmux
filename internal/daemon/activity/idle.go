package activity

import (
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// Naming and eligibility for liveness-derived idle evidence. What an
// authoritative idle observation DOES to a window is the daemon root's
// business — it needs the reducer, the renderer, and the notifier to say it.
// What such an observation is attributed to, and when a probe is allowed to
// produce one at all, is pure vocabulary and lives here.
//
// Each probe names its OWN SourceProbe key — the screen probes take theirs
// from their spec, the Copilot transcript probe from its package constant — so
// there is deliberately no shared idle-source constructor here: a generic one
// would be production API with no production caller.

// IdleHarnessLabel gives the authoritative-idle diagnostic a stable harness label,
// falling back to a generic tag when the observation carried none.
func IdleHarnessLabel(harnessName string) string {
	if h := harness.Normalize(harnessName); h != "" {
		return h
	}
	return "session"
}

// ScreenProbeEligibleIcon reports whether a window's reduced icon is the ⚡ the
// screen idle probe is eligible to classify. Every other icon means the probe
// does not run: a native stop, a blocking prompt, an error, or a clear/reset
// moves the window off ⚡ and the classifier stops being asked anything about
// it.
//
// It is EXPORTED because `amx doctor` gates screenstate_persistent_decline on
// exactly this predicate — a decline streak is only current health while the
// probe would still run — and a private copy of the string "bolt" in cmd would
// be free to drift away from the gate it is supposed to mirror.
func ScreenProbeEligibleIcon(icon string) bool {
	return icon == "bolt"
}

// ClassifyScreenState resolves a harness adapter and classifies one capture. A
// missing adapter is impossible in practice for the harnesses the probes wire —
// the registry is a package-level literal — but the fail-closed zero
// Observation is the right answer anyway, and it is never terminal.
func ClassifyScreenState(harnessName string, screen string) screenstate.Observation {
	adapter, ok := screenstate.For(harnessName)
	if !ok {
		return screenstate.Observation{}
	}
	return adapter.Classify(screen)
}
