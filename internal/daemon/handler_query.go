package daemon

import (
	"slices"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func (h *Handler) handleStateQuery() protocol.Response {
	h.stateMu.Lock()
	now := time.Now().Unix()
	states, snapshot := h.copyStateLocked(now)
	watchdog := make(map[int]protocol.WatchdogState, len(h.store))
	for id, st := range h.store {
		action := st.LastAction
		if action != "active" && action != "subagent" && action != "compact" {
			continue
		}
		entry := protocol.WatchdogState{LastAction: action}
		if !st.Activity.IsZero() {
			entry.LastEventAt = st.Activity.Unix()
		}
		if d := h.staleThresholds.Resolve(st.LastAccount, action); d > 0 {
			entry.ThresholdSec = int(d / time.Second)
		}
		watchdog[id] = entry
	}
	// Topology is read in the same critical section as the window states, under
	// the established stateMu → SnapshotStore order. A publication landing
	// between two separate reads would otherwise hand a dashboard states from
	// one moment and a window tree from another, which is precisely the
	// incoherence a single-round-trip refresh exists to avoid.
	view, listClient, listSocket := h.kittyListSourceLocked()
	runtimeSlots := h.runtimeSlotsLocked()
	kittySlots := make(map[int]protocol.KittySlot, len(h.runtimeRoutes.kittyBySlot))
	for slot, key := range h.runtimeRoutes.kittyBySlot {
		kittySlots[slot] = protocol.KittySlot{Socket: key.socket, WindowID: key.windowID}
	}
	h.stateMu.Unlock()
	// Sampled after the unlock: kittyClientForList takes stateMu itself, so
	// scoping the already-captured client here is what keeps the sample off the
	// lock without re-entering it. These are operational counters for the same
	// effective socket rather than facts about the publication above, so they
	// are not required to be coherent with it.
	status, hasStatus := kittyListStatus(listClient, listSocket)
	return protocol.Response{
		OK:            true,
		Activity:      snapshot,
		Watchdog:      watchdog,
		States:        states,
		RuntimeSlots:  runtimeSlots,
		KittySlots:    kittySlots,
		KittySnapshot: kittySnapshotBlock(view, status, hasStatus, time.Now(), true),
	}
}

// copyStateLocked projects every window's visible state and last-activity
// instant, keyed by window ID. It is the one copy the state query and the
// companion hub both publish, so a dashboard and a companion cannot disagree
// about what a window shows. It clones and does nothing else. Callers hold
// stateMu.
func (h *Handler) copyStateLocked(now int64) (map[int]protocol.WindowStateSnapshot, map[int]int64) {
	states := make(map[int]protocol.WindowStateSnapshot, len(h.store))
	snapshot := make(map[int]int64, len(h.store))
	openASLQuestions := make(map[int]bool)
	for _, pending := range h.aslSupervisors.Pending() {
		openASLQuestions[pending.Target.WindowID] = true
	}
	for id, st := range h.store {
		state := protocol.WindowStateSnapshot{
			StatusEvidence:  statusEvidence(st),
			IconState:       rendering.DesiredIcon(st),
			Looping:         st.Looping,
			LastAction:      st.LastAction,
			GuardAction:     st.GuardAction,
			AgentTurn:       st.Reducer.AgentTurn(now),
			ASLPhases:       st.Reducer.ASLPhases(),
			ASLRunIDs:       liveASLRunIDs(st.Reducer),
			ASLQuestionOpen: openASLQuestions[id],
			// The durable bit is projected as-is rather than recomputed: the
			// daemon's publish path and focus observation are its only writers,
			// so a query that derived it again would be a second opinion.
			Unseen: st.Unseen,
			Muted:  st.Muted,
			// Same rule for the blocked reason, and the same reason for it.
			// stampPromptKindLocked already clears the kind on every publish
			// whose icon is outside the blocking class, so re-checking the icon
			// here would be a second opinion about a question the publish path
			// has already answered — and one that could disagree with it.
			Reason: st.PromptKind,
			// The Git facts are likewise projected verbatim. The probe's
			// write-back already dropped a result whose directory no longer
			// matches the window's, so the pair on the state is either what the
			// checkout said or nothing at all; re-deriving either here would
			// mean running git under stateMu, which is the one thing the
			// asynchronous refresh exists to avoid. GitDirectory stays behind:
			// it is the write-back's staleness evidence, not a display fact.
			Branch: st.GitBranch,
			Dirty:  st.GitDirty,
		}
		if identity, ok := st.CurrentConversation(now); ok {
			identityCopy := identity
			state.Conversation = &identityCopy
		}
		if !st.BusySince.IsZero() {
			state.BusySinceAt = st.BusySince.Unix()
		}
		if !st.PresentationChangedAt.IsZero() {
			state.PresentationChangedAtMS = st.PresentationChangedAt.UnixMilli()
		}
		if st.Activity.IsZero() {
			states[id] = state
			continue
		}
		snapshot[id] = st.Activity.Unix()
		state.LastEventAt = st.Activity.Unix()
		states[id] = state
	}
	return states, snapshot
}

func statusEvidence(st *state.WindowState) *protocol.StatusEvidence {
	// A masked/cleared icon cannot borrow the reducer's explanation for a
	// different icon. Unknown provenance remains explicitly unknown in clients.
	if st.Effective.Icon == "" || rendering.DesiredIcon(st) != st.Effective.Icon {
		return nil
	}
	fact, ok := st.Reducer.Fact(st.Effective.Source)
	if !ok {
		return nil
	}
	kind := string(fact.Kind)
	if fact.Kind == protocol.SourceProbe {
		switch fact.Key {
		case screenPromptProbeSource, claudeScreenStopProbeSource, claudeContextLimitFactSource:
			kind = "screen"
		case copilotTranscriptStopProbeSource:
			kind = "transcript"
		default:
			kind = "probe"
		}
	}
	return &protocol.StatusEvidence{Kind: kind, At: fact.At}
}

// handleSessionDetail returns the per-window event ring buffer for req.WindowID.
// An unknown window yields a non-nil empty slice rather than nil so the TUI
// can distinguish "no daemon error" from "no events yet" without a nil check.
// A socketless ambiguous Kitty number reports a target conflict instead.
func (h *Handler) handleSessionDetail(req protocol.Request) protocol.Response {
	h.stateMu.Lock()
	if req.RuntimeSessionID != "" {
		slot := h.runtimeRoutes.byRuntime[req.RuntimeSessionID]
		if slot <= 0 || req.WindowID != 0 {
			h.stateMu.Unlock()
			return protocol.Response{OK: true, Events: []protocol.SessionEvent{}}
		}
		req.WindowID = slot
	} else if req.KittyListenOn != "" {
		// Kitty numbers are local to a socket. A stamped request whose pair
		// the daemon has never seen cannot read another instance's bare slot.
		slot := h.runtimeRoutes.kittyByKey[kittySlotKey{socket: req.KittyListenOn, windowID: req.WindowID}]
		if slot <= 0 {
			h.stateMu.Unlock()
			return protocol.Response{OK: true, Events: []protocol.SessionEvent{}}
		}
		req.WindowID = slot
	} else if h.ambiguousKittyWindowIDsLocked()[req.WindowID] {
		// A legacy caller supplied only Kitty's local window number. Once two
		// instances claim it, reading the bare slot could disclose the other
		// instance's events.
		h.stateMu.Unlock()
		return protocol.Response{OK: false, Error: protocol.OriginReasonTargetConflict}
	}
	st, _ := h.windowStateLocked(req.WindowID)
	detail := eventlog.Detail(st, h.notificationEnricherLocked(req.WindowID))
	h.stateMu.Unlock()
	return protocol.Response{OK: true, Events: detail}
}

// notificationEnricherLocked returns the per-window enrichment the event-log
// projections apply to each exported copy. Callers hold stateMu.
func (h *Handler) notificationEnricherLocked(windowID int) func(protocol.SessionEvent) protocol.SessionEvent {
	return func(event protocol.SessionEvent) protocol.SessionEvent {
		return h.enrichNotificationEvent(windowID, event)
	}
}

// handleEventsExport returns a deep copy of the requested window's event ring
// buffer plus the durable per-window export snapshot. When req.WindowID is
// zero, every exported window is returned. Mirrors handleSessionDetail's
// locking and defensive copies so callers cannot mutate the daemon's internal
// state.
func (h *Handler) handleEventsExport(req protocol.Request) protocol.Response {
	h.stateMu.Lock()
	if req.RuntimeSessionID != "" {
		// Selection by runtime session: the daemon names the slot, as it
		// does for a session detail, so a session with no Kitty window can
		// ask for its own history. An unknown session is an error rather
		// than an export of everything.
		slot, ok := h.runtimeRoutes.byRuntime[req.RuntimeSessionID]
		if !ok || req.WindowID != 0 {
			h.stateMu.Unlock()
			return protocol.Response{OK: false, Error: protocol.OriginReasonRuntimeSessionUnknown}
		}
		req.WindowID = slot
	} else if req.KittyListenOn != "" && req.WindowID > 0 {
		slot := h.runtimeRoutes.kittyByKey[kittySlotKey{socket: req.KittyListenOn, windowID: req.WindowID}]
		if slot <= 0 {
			h.stateMu.Unlock()
			return protocol.Response{OK: false, Error: "Kitty window is not known on the requested socket"}
		}
		req.WindowID = slot
	} else if req.WindowID > 0 && h.ambiguousKittyWindowIDsLocked()[req.WindowID] {
		h.stateMu.Unlock()
		return protocol.Response{OK: false, Error: protocol.OriginReasonTargetConflict}
	}
	out := make(map[int][]protocol.SessionEvent)
	meta := make(map[int]protocol.WindowDebugMetadata)
	if req.WindowID != 0 {
		if st, ok := h.windowStateLocked(req.WindowID); ok && len(st.EventLog) > 0 {
			out[req.WindowID] = eventlog.Detail(st, h.notificationEnricherLocked(req.WindowID))
		}
		if snapshot, ok := h.windowMetadata[req.WindowID]; ok {
			meta[req.WindowID] = h.exportEnrichLocked(req.WindowID, eventlog.CloneWindowMetadata(snapshot))
		}
	} else {
		out = make(map[int][]protocol.SessionEvent, len(h.store))
		for id, st := range h.store {
			if len(st.EventLog) == 0 {
				continue
			}
			out[id] = eventlog.Detail(st, h.notificationEnricherLocked(id))
		}
		meta = make(map[int]protocol.WindowDebugMetadata, len(h.windowMetadata))
		for id, snapshot := range h.windowMetadata {
			meta[id] = h.exportEnrichLocked(id, eventlog.CloneWindowMetadata(snapshot))
		}
	}
	slots := h.runtimeSlotsLocked()
	kittySlots := make(map[int]protocol.KittySlot, len(h.runtimeRoutes.kittyBySlot))
	for slot, key := range h.runtimeRoutes.kittyBySlot {
		kittySlots[slot] = protocol.KittySlot{Socket: key.socket, WindowID: key.windowID}
	}
	if req.WindowID != 0 {
		bound, ok := slots[req.WindowID]
		slots = nil
		if ok {
			slots = map[int]protocol.RuntimeSlot{req.WindowID: bound}
		}
		if key, ok := kittySlots[req.WindowID]; ok {
			kittySlots = map[int]protocol.KittySlot{req.WindowID: key}
		} else {
			kittySlots = nil
		}
	}
	h.stateMu.Unlock()
	return protocol.Response{OK: true, EventsByWindow: out, WindowMetadata: meta, RuntimeSlots: slots, KittySlots: kittySlots}
}

// exportEnrichLocked stamps live-only state onto an export snapshot: the
// renderer dirty age (DirtySince in the metadata is copied at mutation time;
// the age is computed at export time so a long-stuck window reads truthfully),
// the reducer's current source facts, so diagnostics can hold the visible icon
// accountable to the authoritative fact set, any terminal stop still being
// withheld from that fact set, and the current busy-stretch anchor. Callers
// hold stateMu.
func (h *Handler) exportEnrichLocked(windowID int, meta protocol.WindowDebugMetadata) protocol.WindowDebugMetadata {
	st, ok := h.store[windowID]
	if !ok {
		return meta
	}
	if st.Dirty && !st.DirtySince.IsZero() {
		meta.Renderer.DirtyAgeSeconds = int64(time.Since(st.DirtySince) / time.Second)
	}
	if st.Reducer != nil {
		meta.Shadow.Facts = st.Reducer.Facts()
	}
	meta.Shadow.PendingTerminalStop = h.pendingTerminalStopSnapshotLocked(windowID)
	if !st.BusySince.IsZero() {
		meta.Shadow.BusySinceAt = st.BusySince.Unix()
	}
	return meta
}

// SetReducerConfig installs the shadow reducer thresholds. Existing per-window
// reducers are re-bound too, so a restore that ran before configuration (unit
// tests, degraded startups) still ends up on the configured thresholds.

// liveASLRunIDs lists the runs holding a live lease on one slot, sorted.
func liveASLRunIDs(reducer *state.Reducer) []string {
	if reducer == nil {
		return nil
	}
	var runs []string
	for _, fact := range reducer.Facts() {
		if fact.Kind == protocol.SourceASL && fact.LeaseLive && fact.RunID != "" && !slices.Contains(runs, fact.RunID) {
			runs = append(runs, fact.RunID)
		}
	}
	slices.Sort(runs)
	return runs
}
