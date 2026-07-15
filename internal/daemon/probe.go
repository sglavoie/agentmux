package daemon

import (
	"log/slog"

	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file converts the hook-time context-limit screen read into a targeted
// asynchronous probe request. A GetText round-trip to Kitty can be slow or wedge
// entirely; doing it on the hook goroutine would delay the hook acknowledgement
// and stall the serialized ingress queue behind it. Instead the hook path
// resolves and dispatches the base event immediately, then hands a
// contextLimitProbe to an async runner. When the banner is found the probe
// applies a stop_failure upgrade through the normal state path, so the icon
// still reaches error — just eventually, without ever blocking the ack.

// contextLimitProbeTarget is one managed window whose icon should be promoted to
// error when the source pane's screen shows the context-limit banner. It carries
// the render context a stop_failure state update needs, captured at hook time.
type contextLimitProbeTarget struct {
	windowID      int
	sessionTitle  string
	profile       string
	targetHarness string
	currentIcon   string
}

// contextLimitProbe describes a single asynchronous screen read. sourceWindowID
// is the pane that stalled (the banner is only visible there); targets are the
// managed windows to promote on detection. For an in-pane hook the source is
// also the sole target; for an ASL sibling hook the source is the unmanaged pane
// and the targets are the managed title owners.
type contextLimitProbe struct {
	sourceWindowID int
	req            Request
	event          string
	payload        hookevent.HookPayload
	targets        []contextLimitProbeTarget
}

// runProbeAsync runs fn off the hook goroutine. Production spawns a goroutine so
// a slow GetText never delays hook acknowledgement; tests inject a synchronous
// runner for deterministic assertions.
func (h *Handler) runProbeAsync(fn func()) {
	if h.probeRunner != nil {
		h.probeRunner(fn)
		return
	}
	go fn()
}

// enqueueContextLimitProbe schedules an asynchronous context-limit screen read.
// It is a no-op when there is no Kitty client or nothing to promote, so the hook
// path can call it unconditionally after deciding a probe is warranted.
func (h *Handler) enqueueContextLimitProbe(p contextLimitProbe) {
	if h.kittyClient == nil || p.sourceWindowID <= 0 || len(p.targets) == 0 {
		return
	}
	h.runProbeAsync(func() { h.runContextLimitProbe(p) })
}

// runContextLimitProbe performs the targeted screen read and, on a banner match,
// applies a stop_failure upgrade to every target. It runs off the hook
// goroutine; a read error or a clean screen leaves the base event's outcome
// untouched.
func (h *Handler) runContextLimitProbe(p contextLimitProbe) {
	text, err := h.hookKittyClient(p.req).GetText(kitty.MatchWindowByID(p.sourceWindowID), "screen")
	if err != nil {
		slog.Debug("context-limit probe failed", "window_id", p.sourceWindowID, "event", p.event, "err", err)
		return
	}
	if !contextlimit.ScreenShowsMarker(text) {
		return
	}
	for _, t := range p.targets {
		stateReq := buildHookStateUpdateRequest(
			t.windowID,
			"stop_failure",
			p.event,
			p.payload,
			t.sessionTitle,
			t.profile,
			p.req.Harness,
			t.targetHarness,
			t.currentIcon,
			p.req.PipeMode,
			false, // preservesIcon: an error promotion never preserves the current icon
			true,  // contextLimitDetected
			false, // resetFlowDetected: a context-limit stall is not a reset flow
			p.req.KittyListenOn,
		)
		// The promotion is daemon-synthesized, so its shadow fact is attributed
		// to the probe source rather than the harness session.
		normalized := NormalizedEvent{
			Source:         ProbeSource(),
			Action:         "stop_failure",
			Event:          p.event,
			OriginWindowID: p.sourceWindowID,
		}
		stateReq.normalized = &normalized
		h.handleStateUpdate(stateReq)
	}
}
