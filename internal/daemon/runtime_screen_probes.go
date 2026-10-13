package daemon

import (
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// A session on a backend other than Kitty is probed by the same bodies a
// Kitty window is: the Claude context-limit banner, the prompt label, and the
// idle screen that settles a stuck turn or confirms a held stop; the Codex
// prompt and idle screen. What differs is only where the binding comes from
// and how its screen is read.
//
// A Kitty binding is derived from the window tree and names two windows. A
// runtime binding is derived from the slot table: its source and target are
// both the session's slot, its freshness is the reducer receipt alone, and
// its screen must be the registered pane under the registered generation, so
// a pane of a restarted server, a recycled pane ID, an exited agent, or a
// read that failed is never classified (probes.CheckScreen). A pane that
// cannot be read is "no evidence this tick", exactly as a Kitty window whose
// get-text failed: a held stop then waits for its deadline, and no banner is
// reported.
//
// Three things a Kitty probe takes from the window tree come from elsewhere
// here. The title Claude sets on its pane is read with the screen
// (backend.Screen.Title), so the busy-title recovery and the idle title bump
// judge it by the same generation and instant as the text. The whole
// scrollback the Codex cleared-screen check wants is a second read of the
// registered pane (probeScrollbackText). And the routes the Kitty probes find
// by walking managed windows, a stopped Codex session and a Copilot stop its
// transcript probe may take back, are found by walking the slot table
// (runtimeProbeTargets): a slot holds one session on one pane, so each is a
// direct route and none needs the ambiguity rule a window tree does.

// RuntimeScreen reads the panes of one backend instance: the visible screen
// with the facts that qualify it, and a pane's text with its history.
type RuntimeScreen interface {
	backend.Observer
	backend.Capturer
}

// RuntimeScreenReader opens the screen reader of one backend instance. It
// runs off stateMu and may start a subprocess.
type RuntimeScreenReader func(instance runtimesession.BackendInstance) (RuntimeScreen, error)

// SetRuntimeScreenReader installs the reader the probes of non-Kitty sessions
// read their panes through. It is called once at startup; nil leaves those
// sessions unprobed.
func (h *Handler) SetRuntimeScreenReader(reader RuntimeScreenReader) {
	h.runtimeScreens = reader
}

// NewTmuxRuntimeScreenReader is the production reader: the private tmux
// server at the instance's address. It never starts one.
func NewTmuxRuntimeScreenReader() RuntimeScreenReader {
	return func(instance runtimesession.BackendInstance) (RuntimeScreen, error) {
		if instance.Kind != runtimesession.BackendTmux {
			return nil, fmt.Errorf("no screen reader for backend %q", instance.Kind)
		}
		server, err := tmux.OpenExisting(instance.Address)
		if err != nil {
			return nil, err
		}
		return tmux.NewBackend(server), nil
	}
}

// harnessHasRuntimeStopProbe reports whether a held stop of this harness can
// be confirmed on a backend other than Kitty.
func harnessHasRuntimeStopProbe(name string) bool {
	switch harness.NormalizeKnown(name) {
	case harness.Claude, harness.Codex, harness.Copilot:
		return true
	default:
		return false
	}
}

// runtimeScreenRouter is the observer behind the per-tick screen cache. A
// reference naming a runtime slot is read from the pane that slot's session
// is registered on; anything else is a Kitty window and goes to base.
type runtimeScreenRouter struct {
	h    *Handler
	base backend.Observer
}

func (r runtimeScreenRouter) Observe(pane backend.Ref) (backend.Screen, error) {
	slot, ok := probes.RuntimeSlotOfPane(pane)
	if !ok {
		return r.base.Observe(pane)
	}
	r.h.stateMu.Lock()
	bound, ok := r.h.runtimeRoutes.binding(slot)
	r.h.stateMu.Unlock()
	if !ok || r.h.runtimeScreens == nil {
		return backend.Screen{}, fmt.Errorf("slot %d has no pane to read", slot)
	}
	observer, err := r.h.runtimeScreens(bound.target.instanceOf())
	if err != nil {
		return backend.Screen{}, err
	}
	return observer.Observe(backend.PaneID(bound.target.pane))
}

// probeScrollbackText returns the producer pane's text with its history, the
// second read the Codex cleared-screen check makes once the visible screen
// looks cleared. A Kitty window is read through the Kitty client, as before.
//
// A pane on another backend is captured from the server the binding
// registered, and a capture names no generation. So the pane is observed
// once more afterwards: a server that answers under the registered
// generation after the capture is the one that answered it, since a
// restarted server never gets its old generation back. Anything
// probes.CheckScreen refuses of that observation withholds the capture too.
func (h *Handler) probeScrollbackText(binding probes.Binding) (string, error) {
	if !binding.OnRuntimeBackend() {
		if h.kittyClient == nil {
			return "", fmt.Errorf("window %d has no scrollback to read", binding.SourceWindowID)
		}
		return h.kittyClient.GetText(kitty.MatchWindowByID(binding.SourceWindowID), string(backend.ExtentAll))
	}
	if h.runtimeScreens == nil {
		return "", fmt.Errorf("slot %d has no pane to read", binding.SourceWindowID)
	}
	started := time.Now()
	reader, err := h.runtimeScreens(binding.RuntimeInstance)
	if err != nil {
		h.recordScreenRead(time.Since(started), err)
		return "", err
	}
	all, err := reader.Capture(binding.RuntimePane, backend.ExtentAll)
	h.recordScreenRead(time.Since(started), err)
	if err != nil {
		return "", err
	}
	confirmStarted := time.Now()
	screen, err := reader.Observe(binding.RuntimePane)
	h.recordScreenRead(time.Since(confirmStarted), err)
	want := binding.ScreenExpectation()
	want.NotBefore = started
	if _, err := probes.FreshScreenText(screen, err, want, time.Now()); err != nil {
		return "", err
	}
	return screenstate.StripANSI(all), nil
}

// instanceOf is the registered instance of a slot's target.
func (t runtimeTarget) instanceOf() runtimesession.BackendInstance {
	return runtimesession.BackendInstance{
		Kind:       runtimesession.BackendKind(t.backend),
		Address:    t.instance,
		Generation: t.generation,
	}
}

// runtimeProbeTarget is one non-Kitty session a tick may probe, with the
// presentation context a Kitty window's tab title and user vars would supply.
type runtimeProbeTarget struct {
	binding probes.Binding
	harness string
	title   palette.TitleParts
	vars    kitty.UserVars
}

// runtimeProbeTargets lists the non-Kitty slots a tick may probe, in slot
// order: those holding a live producer, and the two a probe may still act on
// after the live route closed. Each binding captures the slot's reducer
// receipt now, so anything that lands before a probe acts makes the binding
// stale.
func (h *Handler) runtimeProbeTargets(now time.Time) []runtimeProbeTarget {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	var targets []runtimeProbeTarget
	for slot, bound := range h.runtimeRoutes.bySlot {
		if !isRuntimeSyntheticSlot(slot) {
			continue
		}
		st, ok := h.windowStateLocked(slot)
		if !ok || st == nil || st.Reducer == nil {
			continue
		}
		name := harness.NormalizeKnown(st.LastHarness)
		binding := probes.Binding{
			SourceWindowID:  slot,
			TargetWindowID:  slot,
			SourceReceipt:   st.ShadowSeq,
			TargetReceipt:   st.ShadowSeq,
			RuntimeInstance: bound.target.instanceOf(),
			RuntimePane:     backend.PaneID(bound.target.pane),
		}
		identity, source, ok := st.Reducer.CurrentLiveProducerRoute(name, slot)
		switch {
		case ok:
			binding.Producer = identity
		case name == harness.Codex && st.Effective.Icon == "stop":
			// A stopped Codex session has no live route left, and its stop is
			// what a /clear typed at rest must take away. On Kitty that pane is
			// reached as the tab's title owner (screenProbeBindings); a slot's
			// pane is its session's by construction.
			binding.Producer = conversation.Identity{Harness: harness.Codex}
			binding.LegacyDirectManaged = true
			source = slot
		case name == harness.Copilot:
			// The transcript probe's own stop retired the live route; the
			// session it settled is the one it may take that stop back for.
			recovery := protocol.SourceID{Kind: protocol.SourceProbe, Key: copilotTranscriptStopProbeSource}
			identity, source, ok = st.Reducer.ProbeStopRecoveryRoute(recovery, harness.Copilot, slot, now.Unix())
			if !ok {
				continue
			}
			binding.Producer = identity
			binding.RecoverySource = recovery
			binding.RecoveryAt = now.Unix()
		default:
			continue
		}
		if source != slot {
			continue
		}
		if !h.screenProbeBindingCurrentLocked(binding) {
			continue
		}
		targets = append(targets, runtimeProbeTarget{
			binding: binding,
			harness: name,
			title:   palette.TitleParts{Base: st.LastSessionTitle, State: st.Effective.Icon, Muted: st.Muted},
			vars:    kitty.UserVars{Profile: st.LastAccount, Harness: name},
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].binding.TargetWindowID < targets[j].binding.TargetWindowID
	})
	return targets
}

// runtimeQuietProbeEvery is how many ticks pass between reads of a session
// that shows no icon. Such a session is read for one thing only, a
// context-limit banner its hooks never reported, and every read of a tmux
// pane is a client process: at the full cadence a fleet of idle agents would
// cost a steady stream of them for a banner that is nearly never there. A
// session that is working is read on every tick.
const runtimeQuietProbeEvery = 5

// runtimeProbeQuiet reports a session that is read on the quiet cadence only:
// one showing no icon, and a stopped Codex session, which is read for one
// thing only as well, a /clear typed at rest.
func runtimeProbeQuiet(target runtimeProbeTarget) bool {
	return target.title.State == "" || (target.harness == harness.Codex && target.title.State == "stop")
}

// probeRuntimeScreens runs one tick of the screen probes over the sessions on
// backends other than Kitty. Every probe body bails before reading a screen
// unless the slot's icon is one it may act on, so a Claude or Copilot session
// at rest on a finished turn costs no read at all.
func (h *Handler) probeRuntimeScreens(now time.Time, screens *activity.ScreenTextCache) {
	if h.runtimeScreens == nil {
		return
	}
	quietTick := h.runtimeProbeTicks%runtimeQuietProbeEvery == 0
	h.runtimeProbeTicks++
	for _, target := range h.runtimeProbeTargets(now) {
		if runtimeProbeQuiet(target) && !quietTick {
			continue
		}
		switch target.harness {
		case harness.Claude:
			// Same order as the Kitty tick: the banner settles to the error
			// state first, and the idle classifier reads the same frame.
			if h.claudeContextLimitProbeEnabled {
				h.probeClaudeContextLimitWindow(now, target.binding, target.title, target.vars, screens)
			}
			// The title is the one this tick's screen was read with. Only
			// the two states that use it ask, and both read the screen
			// anyway: no icon (recovery) and a bolt (the idle sample bump).
			paneTitle := ""
			if target.title.State == "" || target.title.State == "bolt" {
				paneTitle = probeScreenTitle(screens, target.binding)
			}
			// As on Kitty, a promotion committed here makes the binding stale
			// for the rest of this tick; the next one reads the screen.
			if activity.ClaudeWindowTitleHasDescriptionMarker(paneTitle) {
				h.recoverClaudeBusyTitle(now, target.binding, target.title, target.vars, paneTitle)
			}
			h.stampClaudePromptKind(target.binding, screens)
			h.probeScreenIdle(now, claudeScreenIdleSpec, target.binding, target.title, target.vars, paneTitle, screens)
		case harness.Codex:
			h.probeCodexBinding(now, target.binding, target.title, target.vars, screens)
		case harness.Copilot:
			h.probeCopilotTranscriptWindow(now, target.binding, target.title, target.vars, screens)
		}
	}
}
