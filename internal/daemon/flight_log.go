package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/flightlog"
)

// All identity reads here are from the already validated slot and immutable
// snapshot. No terminal command or filesystem operation runs under stateMu.
func (h *Handler) flightEventLocked(slot int, st *state.WindowState, at time.Time) flightlog.Event {
	e := flightlog.Event{At: at, Icon: st.Effective.Icon, Looping: st.Effective.Looping, Harness: st.LastHarness, Profile: st.LastAccount}
	if identity, ok := st.CurrentConversation(at.Unix()); ok {
		e.ConversationID = identity.Harness + ":" + identity.SessionID
		e.Harness = identity.Harness
	}
	if binding, ok := h.runtimeRoutes.bySlot[slot]; ok {
		e.RuntimeSessionID = binding.runtime
		e.Key = binding.runtime
		label := h.runtimeLabels[binding.runtime]
		e.Project = label.project
		if label.profile != "" {
			e.Profile = label.profile
		}
		if e.Harness == "" {
			e.Harness = label.harness
		}
		return e
	}
	view := h.snapshots.View()
	windowID := slot
	socket := h.kittySockets[slot]
	if key, ok := h.runtimeRoutes.kittyBySlot[slot]; ok {
		windowID = key.windowID
		socket = key.socket
	}
	windows := view.Windows()
	if socket != "" && socket != view.Socket() {
		windows = h.otherKittyTrees[socket]
	}
	for _, osw := range windows {
		for _, tab := range osw.Tabs {
			for _, window := range tab.Windows {
				if window.ID != windowID {
					continue
				}
				e.LaunchID = window.UserVars.LaunchID
				e.Project = window.UserVars.Project
				if e.Profile == "" {
					e.Profile = window.UserVars.EffectiveProfile()
				}
				if e.Harness == "" {
					e.Harness = window.UserVars.EffectiveHarness()
				}
			}
		}
	}
	if e.LaunchID != "" {
		e.Key = "launch:" + e.LaunchID
	} else {
		e.Key = fmt.Sprintf("observed:%s:%d", h.flightLog.Epoch, slot)
		e.Partial = true
	}
	return e
}

func (h *Handler) recordFlightStateLocked(slot int, st *state.WindowState, next state.EffectiveState, at time.Time) {
	if h.flightLog == nil || st.Effective.Icon == next.Icon && st.Effective.Looping == next.Looping {
		return
	}
	e := h.flightEventLocked(slot, st, at)
	e.Kind = "state"
	e.BeforeIcon = st.Effective.Icon
	e.BeforeLooping = st.Effective.Looping
	e.Icon = next.Icon
	e.Looping = next.Looping
	h.flightLog.Record(e)
}

func (h *Handler) recordFlightRun(event protocol.SessionEvent) {
	if h.flightLog == nil || !strings.HasPrefix(event.Event, "AgentSessionLoop") {
		return
	}
	if event.ReducerDecision != protocol.DecisionApplied && event.ReducerDecision != protocol.DecisionAcknowledged && event.ReducerDecision != protocol.DecisionBypassed {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	at := time.Now()
	if event.ReceivedAtNs > 0 {
		at = time.Unix(0, event.ReceivedAtNs)
	}
	e := flightlog.Event{At: at, Kind: "run", Name: event.Event, RunID: event.RunID, TraceID: event.TraceID, Harness: event.ProducerHarness, Profile: event.Profile}
	runtimeID := ""
	if event.Origin != nil {
		runtimeID = event.Origin.RuntimeSessionID
	}
	st := h.store[event.TargetWindowID]
	if st != nil && (runtimeID == "" || h.runtimeRoutes.bySlot[event.TargetWindowID].runtime == runtimeID) {
		identity := h.flightEventLocked(event.TargetWindowID, st, at)
		e.Key = identity.Key
		e.RuntimeSessionID = identity.RuntimeSessionID
		e.LaunchID = identity.LaunchID
		e.Project = identity.Project
		e.ConversationID = identity.ConversationID
		e.Partial = identity.Partial
		if e.Harness == "" {
			e.Harness = identity.Harness
		}
		if e.Profile == "" {
			e.Profile = identity.Profile
		}
	} else if runtimeID != "" {
		e.Key = runtimeID
		e.RuntimeSessionID = runtimeID
		e.Partial = true
	} else if e.RunID != "" {
		e.Key = "run:" + e.RunID
		e.Partial = true
	} else {
		return
	}
	if event.SessionID != "" && e.Harness != "" {
		e.ConversationID = e.Harness + ":" + event.SessionID
	}
	if e.RunID != "" {
		e.ID = "run:" + e.RunID + ":" + event.Event
	}
	h.flightLog.Record(e)
}

func (h *Handler) startFlightLog() func() {
	store, err := flightlog.DefaultStore()
	if err != nil {
		slog.Warn("flight log unavailable", "error", err)
		return func() {}
	}
	collector, err := flightlog.NewCollector(store)
	if err != nil {
		slog.Warn("flight log unavailable", "error", err)
		return func() {}
	}
	h.flightLog = collector
	h.sampleFlightLog(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				h.sampleFlightLog(at)
			}
		}
	}()
	return func() { cancel(); <-done; collector.Close() }
}

func (h *Handler) sampleFlightLog(at time.Time) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for slot, st := range h.store {
		if !st.StateSet {
			continue
		}
		e := h.flightEventLocked(slot, st, at)
		e.Kind = "sample"
		h.flightLog.Record(e)
	}
}
