package daemon

import (
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
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
// Not everything a Kitty window gets is here. The Claude busy-title recovery
// and the Codex cleared-screen check read things only Kitty supplies (a pane
// title set by the harness, a whole scrollback), and Copilot is confirmed
// from its transcript by a probe that walks the Kitty tree. Those sessions
// keep what hooks alone give them; a Copilot stop on another backend is
// simply not held for confirmation (terminalStopHoldArmableLocked).

// RuntimeScreenReader opens the screen observer of one backend instance. It
// runs off stateMu and may start a subprocess.
type RuntimeScreenReader func(instance runtimesession.BackendInstance) (backend.Observer, error)

// SetRuntimeScreenReader installs the reader the probes of non-Kitty sessions
// read their panes through. It is called once at startup; nil leaves those
// sessions unprobed.
func (h *Handler) SetRuntimeScreenReader(reader RuntimeScreenReader) {
	h.runtimeScreens = reader
}

// NewTmuxRuntimeScreenReader is the production reader: the private tmux
// server at the instance's address. It never starts one.
func NewTmuxRuntimeScreenReader() RuntimeScreenReader {
	return func(instance runtimesession.BackendInstance) (backend.Observer, error) {
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
	case harness.Claude, harness.Codex:
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

// runtimeProbeTargets lists the non-Kitty slots holding a live producer, in
// slot order. Each binding captures the slot's reducer receipt now, so
// anything that lands before a probe acts makes the binding stale.
func (h *Handler) runtimeProbeTargets() []runtimeProbeTarget {
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
		identity, source, ok := st.Reducer.CurrentLiveProducerRoute(name, slot)
		if !ok || source != slot {
			continue
		}
		binding := probes.Binding{
			Producer:        identity,
			SourceWindowID:  slot,
			TargetWindowID:  slot,
			SourceReceipt:   st.ShadowSeq,
			TargetReceipt:   st.ShadowSeq,
			RuntimeInstance: bound.target.instanceOf(),
			RuntimePane:     backend.PaneID(bound.target.pane),
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

// probeRuntimeScreens runs one tick of the screen probes over the sessions on
// backends other than Kitty. Every probe body bails before reading a screen
// unless the slot's icon is one it may act on, so a session at rest on a
// finished turn costs no read at all.
func (h *Handler) probeRuntimeScreens(now time.Time, screens *activity.ScreenTextCache) {
	if h.runtimeScreens == nil {
		return
	}
	quietTick := h.runtimeProbeTicks%runtimeQuietProbeEvery == 0
	h.runtimeProbeTicks++
	for _, target := range h.runtimeProbeTargets() {
		if target.title.State == "" && !quietTick {
			continue
		}
		switch target.harness {
		case harness.Claude:
			// Same order as the Kitty tick: the banner settles to the error
			// state first, and the idle classifier reads the same frame.
			if h.claudeContextLimitProbeEnabled {
				h.probeClaudeContextLimitWindow(now, target.binding, target.title, target.vars, screens)
			}
			h.stampClaudePromptKind(target.binding, screens)
			h.probeScreenIdle(now, claudeScreenIdleSpec, target.binding, target.title, target.vars, "", screens)
		case harness.Codex:
			h.probeCodexBinding(now, target.binding, target.title, target.vars, screens)
		}
	}
}
