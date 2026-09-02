package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	terminalHoldTestDelay   = 6 * time.Second
	terminalHoldTestTimeout = 15 * time.Second
)

// newTerminalHoldTestHandler builds the only pane shape in which the
// confirmed-idle gate arms: an interactive, screen-readable window owned by a
// harness that has an idle stop probe. Stop notifications are enabled so "no
// notification fired" is a real assertion rather than a disabled-config
// tautology.
func newTerminalHoldTestHandler(t *testing.T) (*Handler, *callRecorder) {
	t.Helper()
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)
	// Wire the live mute lookup the daemon installs, so mute is consulted at
	// dispatch time exactly as it is in production.
	notifier.SetIsMuted(h.IsMuted)
	h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, true)
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "", false), []kitty.Window{{
		ID:       42,
		IsActive: true,
		UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
	}})})
	return h, rec
}

func terminalHoldPending(t *testing.T, h *Handler, windowID int) (pendingStopHold, bool) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	pending, ok := h.pendingStopHolds[windowID]
	return pending, ok
}

// confirmTerminalHold drives the probe confirmation seam the way
// applyAuthoritativeIdle does — under stateMu — without needing a live screen or
// transcript read.
func confirmTerminalHold(t *testing.T, h *Handler, windowID int, source protocol.SourceID) bool {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.confirmStopHoldLocked(windowID, source)
}

// claudeScreenProbeSource is the SourceID the Claude screen probe settles under,
// and the one the confirmation seam maps to StopConfirmedByClaudeScreen.
var claudeScreenProbeSource = protocol.SourceID{Kind: protocol.SourceProbe, Key: claudeScreenStopProbeSource}

// newTerminalHoldProbeHandler builds the pane shape where the confirmed-idle
// gate and the harness's own screen probe both apply: an interactive managed tab
// whose screen the fake client can read while its stop is withheld.
func newTerminalHoldProbeHandler(t *testing.T, harnessName, screen string) (*Handler, *callRecorder) {
	t.Helper()
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)
	notifier.SetIsMuted(h.IsMuted)
	h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, true)
	h.SetKittyClient(&fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harnessName,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): screen},
	})
	return h, rec
}

// releaseHoldAtTestEnd drops a still-pending hold when the test returns. A probe
// test asserts on the confirmation, not on the commit, so without this the
// multi-second hold timer would outlive the test and repaint a handler nothing
// is watching any more.
func releaseHoldAtTestEnd(t *testing.T, h *Handler, windowID int) {
	t.Helper()
	t.Cleanup(func() { h.cancelStopHold(windowID) })
}

func windowFacts(t *testing.T, h *Handler, windowID int) []protocol.SourceFactSnapshot {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok || st.Reducer == nil {
		t.Fatalf("window %d has no reducer", windowID)
	}
	return st.Reducer.Facts()
}

// assertNoProbeStopFact proves a probe's idle evidence was consumed as
// confirmation of the held native stop instead of settling a synthetic stop of
// its own.
func assertNoProbeStopFact(t *testing.T, h *Handler, windowID int) {
	t.Helper()
	for _, fact := range windowFacts(t, h, windowID) {
		if fact.Kind == protocol.SourceProbe {
			t.Fatalf("probe fact %+v settled; the held native stop owns this transition", fact)
		}
	}
}

// armTerminalHold drives a prompt then a native Stop and asserts the Stop
// entered the terminal class instead of painting immediately.
func armTerminalHold(t *testing.T, h *Handler) {
	t.Helper()
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
		t.Fatalf("UserPromptSubmit response = %+v", resp)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon after prompt = %q, want bolt", eff.Icon)
	}
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, 42)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want an armed terminal hold", pending, armed)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon while held = %q, want bolt", eff.Icon)
	}
}

// deliverProcessStop fires a native Stop the way the hook client delivers one:
// carrying the session it belongs to and the exact process fingerprint that
// emitted it, so the daemon can tell this stop's producer apart from any sibling
// process sharing the window.
func deliverProcessStop(t *testing.T, h *Handler, windowID int, identity protocol.HarnessProcessIdentity, sessionID string) {
	t.Helper()
	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: windowID, Event: "Stop",
		Harness: identity.Harness, HarnessProcess: &identity,
		PayloadJSON: `{"session_id":"` + sessionID + `"}`,
	})
	if !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, windowID)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want an armed terminal hold", pending, armed)
	}
	if eff, _ := windowEffective(t, h, windowID); eff.Icon != "bolt" {
		t.Fatalf("icon while held = %q, want bolt", eff.Icon)
	}
}

// renameHeldWindow moves the durable title/profile of a window whose stop is
// already held. No hook event can do this without also superseding the hold, so
// the durable fields a later non-terminal delivery would have written are set
// directly.
func renameHeldWindow(t *testing.T, h *Handler, windowID int, title, account string) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.ensureWindowStateLocked(windowID)
	st.LastSessionTitle = title
	st.LastAccount = account
}

func awaitStopHoldClaim(t *testing.T, claims <-chan struct{}, failure string, resume chan struct{}) {
	t.Helper()
	select {
	case <-claims:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal(failure)
	}
}

func lastStopEvent(t *testing.T, h *Handler, windowID int) protocol.SessionEvent {
	t.Helper()
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: windowID})
	for i := len(detail.Events) - 1; i >= 0; i-- {
		if detail.Events[i].Event == "Stop" {
			return detail.Events[i]
		}
	}
	t.Fatalf("no committed Stop event in %+v", detail.Events)
	return protocol.SessionEvent{}
}

func stopHoldOutcomes(t *testing.T, h *Handler, windowID int) []protocol.StopHoldOutcome {
	t.Helper()
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: windowID})
	var out []protocol.StopHoldOutcome
	for _, ev := range detail.Events {
		if ev.Event == protocol.EventTerminalStopHold {
			out = append(out, ev.StopHoldOutcome)
		}
	}
	return out
}

// settledStopEvents counts the rows that actually painted a stop. The synthetic
// hold rows carry action "stop" too but apply nothing, so they are excluded.
func settledStopEvents(t *testing.T, h *Handler, windowID int) int {
	t.Helper()
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: windowID})
	settled := 0
	for _, ev := range detail.Events {
		if ev.Action == "stop" && ev.Event != protocol.EventTerminalStopHold {
			settled++
		}
	}
	return settled
}
