package daemon

import (
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// This file is the hook-origin translation layer. Every hook delivery is
// translated into one reducer slot before anything else reads it:
//
//   - A managed hook names its AgentMux runtime session. The registry, not the
//     terminal, says where that session runs, so its target decides the slot,
//     and a session that is pending, stale, ended, or unknown routes nowhere.
//   - A legacy hook names only a Kitty window and listen socket. It keeps its
//     window ID as the slot, exactly as before runtime sessions existed, unless
//     that slot is now held by a runtime session of another Kitty instance.
//
// Slots are the integer keys of Handler.store. A Kitty target's slot is its
// window ID, so the renderer, the probes, and amx_state persistence keep
// working on it unchanged. Every other backend target gets a slot at or above
// runtimeSlotBase, far beyond any Kitty window ID; such a slot never appears in
// a Kitty snapshot, so no render, probe, or prune path ever sends it to Kitty.
//
// A slot belongs to at most one runtime session at a time. A delivery whose
// slot is held by a different session that is still live is refused rather
// than merged, and a new session claiming a slot whose previous session has
// ended first closes that slot, so no fact, notification timer, guard, or
// subagent count of one session survives into another.
//
// Translation reads only daemon memory and the registry reader's cache (one
// stat on a miss); it never lists a terminal backend.

// runtimeSlotBase is the first slot assigned to a non-Kitty backend target.
const runtimeSlotBase = 1 << 40

// RuntimeSessionDirectory resolves runtime-session IDs for hook routing.
// *runtimesession.Reader is the production implementation.
type RuntimeSessionDirectory interface {
	Lookup(id runtimesession.ID) (runtimesession.Record, bool, error)
}

// runtimeDirectoryRefresher is implemented by directories that can observe
// state transitions of IDs they answer from cache.
type runtimeDirectoryRefresher interface {
	Refresh() error
}

// runtimeTarget is a registered backend target in comparable form.
type runtimeTarget struct {
	backend    string
	instance   string
	generation string
	pane       string
}

// runtimeBinding records which runtime session holds a slot and where it runs.
type runtimeBinding struct {
	runtime string
	target  runtimeTarget
}

// runtimeRoutes is the slot table. Caller-locked: guarded by stateMu.
type runtimeRoutes struct {
	bySlot    map[int]runtimeBinding
	byRuntime map[string]int
	// synthetic remembers the slot each non-Kitty target was given, so a
	// target keeps its slot for as long as anything is bound to it.
	synthetic     map[runtimeTarget]int
	nextSynthetic int
}

func (r *runtimeRoutes) binding(slot int) (runtimeBinding, bool) {
	b, ok := r.bySlot[slot]
	return b, ok
}

func (r *runtimeRoutes) bind(slot int, b runtimeBinding) {
	if r.bySlot == nil {
		r.bySlot = make(map[int]runtimeBinding)
		r.byRuntime = make(map[string]int)
	}
	if previous, ok := r.byRuntime[b.runtime]; ok && previous != slot {
		r.release(previous)
	}
	r.bySlot[slot] = b
	r.byRuntime[b.runtime] = slot
}

func (r *runtimeRoutes) release(slot int) {
	b, ok := r.bySlot[slot]
	if !ok {
		return
	}
	delete(r.bySlot, slot)
	if r.byRuntime[b.runtime] == slot {
		delete(r.byRuntime, b.runtime)
	}
	if r.synthetic[b.target] == slot {
		delete(r.synthetic, b.target)
	}
}

// syntheticSlot returns the slot for a non-Kitty target, allocating one the
// first time the target is seen. Slots are never reused within a daemon
// lifetime, so a late event for a released slot cannot land on a new target.
func (r *runtimeRoutes) syntheticSlot(target runtimeTarget) int {
	if slot, ok := r.synthetic[target]; ok {
		return slot
	}
	if r.synthetic == nil {
		r.synthetic = make(map[runtimeTarget]int)
	}
	slot := runtimeSlotBase + r.nextSynthetic
	r.nextSynthetic++
	r.synthetic[target] = slot
	return slot
}

// forgetUnboundSynthetic drops target's synthetic slot when nothing is bound
// to it, so a restore that declined the slot does not keep it reserved.
func (r *runtimeRoutes) forgetUnboundSynthetic(target runtimeTarget) {
	slot, ok := r.synthetic[target]
	if !ok {
		return
	}
	if _, bound := r.bySlot[slot]; !bound {
		delete(r.synthetic, target)
	}
}

func (r *runtimeRoutes) snapshot() map[int]runtimeBinding {
	return maps.Clone(r.bySlot)
}

// isRuntimeSyntheticSlot reports whether slot belongs to a non-Kitty target.
func isRuntimeSyntheticSlot(slot int) bool { return slot >= runtimeSlotBase }

// SetRuntimeSessionDirectory installs the registry used to route managed
// hooks. It is called once at startup, before the server dispatches a request;
// without one, managed hooks are refused as retryable and legacy hooks route
// as before.
func (h *Handler) SetRuntimeSessionDirectory(directory RuntimeSessionDirectory) {
	h.runtimeDirectory = directory
}

// originRejection is a refused translation: the acknowledgement reason and
// whether a retry could succeed.
type originRejection struct {
	reason    string
	retryable bool
}

func rejectOrigin(reason string) *originRejection {
	return &originRejection{reason: reason, retryable: protocol.RetryableOriginReason(reason)}
}

// translateHookOrigin resolves req's runtime identity to a reducer slot,
// rewriting WindowID to the slot and stamping Origin. A rejected request must
// not be handled at all: it carries an identity the daemon cannot place.
func (h *Handler) translateHookOrigin(req protocol.Request) (protocol.Request, *originRejection) {
	raw := strings.TrimSpace(req.RuntimeSessionID)
	if raw == "" {
		return h.translateLegacyKittyOrigin(req)
	}
	id, err := runtimesession.ParseID(raw)
	if err != nil {
		return req, rejectOrigin(protocol.OriginReasonInvalidRuntimeSession)
	}
	req.RuntimeSessionID = string(id)
	if h.runtimeDirectory == nil {
		return req, rejectOrigin(protocol.OriginReasonRuntimeRegistryUnavailable)
	}
	record, found, err := h.runtimeDirectory.Lookup(id)
	switch {
	case !found && err != nil:
		slog.Warn("hook origin: runtime registry unreadable", "err", err)
		return req, rejectOrigin(protocol.OriginReasonRuntimeRegistryUnavailable)
	case !found:
		return req, rejectOrigin(protocol.OriginReasonRuntimeSessionUnknown)
	}
	switch record.State {
	case runtimesession.StateLive:
	case runtimesession.StatePending:
		return req, rejectOrigin(protocol.OriginReasonRuntimeSessionPending)
	default:
		// Post-termination: the session's late hooks route nowhere, and its slot
		// is released so a successor can claim it.
		h.stateMu.Lock()
		closed := h.releaseEndedRuntimeLocked(map[int]string{h.runtimeRoutes.byRuntime[string(id)]: string(id)}, time.Now())
		h.stateMu.Unlock()
		h.finishClosedWindows(closed)
		return req, rejectOrigin(protocol.OriginReasonRuntimeSessionEnded)
	}
	target, ok := runtimeTargetOf(record.Target)
	if !ok {
		return req, rejectOrigin(protocol.OriginReasonInvalidRuntimeTarget)
	}

	// The occupant's liveness is read before stateMu is taken: the directory
	// may stat the registry file, which never happens under the state lock.
	// Ingress is serialized, so only the maintenance pass can move a binding
	// between this read and the claim below; claimRuntimeSlotLocked re-checks
	// the occupant and fails closed if it changed.
	h.stateMu.Lock()
	slot := h.runtimeSlotForTargetLocked(target)
	occupant := h.slotRuntimeLocked(slot)
	h.stateMu.Unlock()
	occupantLive := occupant != "" && occupant != string(id) && h.runtimeSessionLive(occupant)

	h.stateMu.Lock()
	closed, rejection := h.claimRuntimeSlotLocked(slot, string(id), target, occupant, occupantLive)
	if rejection == nil && isRuntimeSyntheticSlot(slot) {
		h.noteRuntimeProjectLocked(string(id), record.Project)
	}
	h.stateMu.Unlock()
	if rejection != nil {
		return req, rejection
	}
	if closed != nil {
		h.finishClosedWindows(closed)
	}

	req.WindowID = slot
	if target.backend == string(runtimesession.BackendKitty) {
		req.KittyListenOn = target.instance
	} else {
		req.KittyListenOn = ""
	}
	req.Origin = &protocol.HookOrigin{
		RuntimeSessionID: string(id),
		Backend:          target.backend,
		Instance:         target.instance,
		Generation:       target.generation,
		Pane:             target.pane,
		Slot:             slot,
	}
	return req, nil
}

// runtimeTargetOf validates a registered target for routing. A Kitty target's
// reference must be the canonical decimal window ID its slot will be.
func runtimeTargetOf(target *runtimesession.Target) (runtimeTarget, bool) {
	if target == nil || target.Validate() != nil {
		return runtimeTarget{}, false
	}
	t := runtimeTarget{
		backend:    string(target.Instance.Kind),
		instance:   target.Instance.Address,
		generation: target.Instance.Generation,
		pane:       target.Ref,
	}
	if target.Instance.Kind == runtimesession.BackendKitty {
		if _, ok := kittyWindowSlot(target.Ref); !ok {
			return runtimeTarget{}, false
		}
	}
	return t, true
}

func kittyWindowSlot(ref string) (int, bool) {
	id, err := strconv.Atoi(ref)
	if err != nil || id <= 0 || id >= runtimeSlotBase || strconv.Itoa(id) != ref {
		return 0, false
	}
	return id, true
}

// runtimeSlotForTargetLocked returns the slot target routes to. Callers hold
// stateMu.
func (h *Handler) runtimeSlotForTargetLocked(target runtimeTarget) int {
	if target.backend == string(runtimesession.BackendKitty) {
		slot, _ := kittyWindowSlot(target.pane)
		return slot
	}
	return h.runtimeRoutes.syntheticSlot(target)
}

// slotRuntimeLocked names the runtime session holding slot: the routing
// binding, or — after the binding was released by a maintenance pass — the
// session the slot's reducer is still bound to. Callers hold stateMu.
func (h *Handler) slotRuntimeLocked(slot int) string {
	if b, ok := h.runtimeRoutes.binding(slot); ok {
		return b.runtime
	}
	if st, ok := h.windowStateLocked(slot); ok && st.Reducer != nil {
		return st.Reducer.RuntimeSession()
	}
	return ""
}

// claimRuntimeSlotLocked binds runtime to slot. occupant and occupantLive are
// the holder observed before the lock was retaken; a different holder now
// means the slot moved underneath this delivery, which fails closed. It
// returns the slot to finish closing when a previous session's state was
// discarded. Callers hold stateMu.
func (h *Handler) claimRuntimeSlotLocked(
	slot int,
	runtime string,
	target runtimeTarget,
	occupant string,
	occupantLive bool,
) (map[int]struct{}, *originRejection) {
	current := h.slotRuntimeLocked(slot)
	if current != occupant {
		return nil, rejectOrigin(protocol.OriginReasonTargetConflict)
	}
	var closed map[int]struct{}
	switch {
	case current == runtime:
		if b, ok := h.runtimeRoutes.binding(slot); ok && b.target != target {
			// The same session at a different target generation is a stale
			// registration the registry has not caught up with yet.
			return nil, rejectOrigin(protocol.OriginReasonTargetConflict)
		}
	case current != "" && occupantLive:
		return nil, rejectOrigin(protocol.OriginReasonTargetConflict)
	case current != "":
		// The previous session is over. Close its slot so nothing it left —
		// facts, subagents, notification timers, guard — leaks into this one.
		h.runtimeRoutes.release(slot)
		closed = map[int]struct{}{slot: {}}
		h.dropClosedWindowsLocked(time.Now(), closed)
	default:
		// An unclaimed Kitty slot may hold legacy state from another Kitty
		// instance whose window happens to share this ID.
		if target.backend == string(runtimesession.BackendKitty) {
			if _, live := h.store[slot]; live && h.windowOfOtherInstanceLocked(slot, target.instance) {
				return nil, rejectOrigin(protocol.OriginReasonTargetConflict)
			}
		}
	}
	h.runtimeRoutes.bind(slot, runtimeBinding{runtime: runtime, target: target})
	return closed, nil
}

// runtimeSessionLive reports whether the directory still lists id as live (or
// pending). An unreadable directory counts as live: failing closed keeps a
// session's slot from being taken over on missing evidence.
func (h *Handler) runtimeSessionLive(id string) bool {
	if h.runtimeDirectory == nil {
		return true
	}
	parsed, err := runtimesession.ParseID(id)
	if err != nil {
		return false
	}
	record, found, err := h.runtimeDirectory.Lookup(parsed)
	if !found {
		return err != nil
	}
	return record.State == runtimesession.StateLive || record.State == runtimesession.StatePending
}

// translateLegacyKittyOrigin validates a hook that carries only Kitty's window
// environment. Its slot remains its window ID; the only new refusal is a slot
// a runtime session of a different Kitty instance holds.
func (h *Handler) translateLegacyKittyOrigin(req protocol.Request) (protocol.Request, *originRejection) {
	if req.WindowID <= 0 || isRuntimeSyntheticSlot(req.WindowID) {
		// Windowless requests keep their historical handling (ignored, with a
		// rejected acknowledgement); a slot-range ID is never a Kitty window.
		if isRuntimeSyntheticSlot(req.WindowID) {
			return req, rejectOrigin(protocol.OriginReasonTargetConflict)
		}
		return req, nil
	}
	socket := strings.TrimSpace(req.KittyListenOn)
	h.stateMu.Lock()
	b, bound := h.runtimeRoutes.binding(req.WindowID)
	h.stateMu.Unlock()
	origin := &protocol.HookOrigin{
		Backend:  string(runtimesession.BackendKitty),
		Instance: socket,
		Pane:     strconv.Itoa(req.WindowID),
		Slot:     req.WindowID,
		Legacy:   true,
	}
	if bound {
		if b.target.backend != string(runtimesession.BackendKitty) ||
			(socket != "" && socket != b.target.instance) {
			return req, rejectOrigin(protocol.OriginReasonTargetConflict)
		}
		// A legacy hook from the claimed pane itself is that session's.
		origin.RuntimeSessionID = b.runtime
		origin.Generation = b.target.generation
	}
	req.Origin = origin
	return req, nil
}

// retireEndedRuntimeSessions releases every slot whose runtime session the
// registry no longer lists as live. A non-Kitty slot is closed with it, since
// no Kitty listing will ever prune it; a Kitty slot keeps its state until
// Kitty reports the window gone, but its reducer stays bound to the ended
// session so a successor still starts clean. It runs on the maintenance
// schedule, off the hook path, and performs the directory reads without
// stateMu.
func (h *Handler) retireEndedRuntimeSessions(now time.Time) {
	if h.runtimeDirectory == nil {
		return
	}
	if refresher, ok := h.runtimeDirectory.(runtimeDirectoryRefresher); ok {
		if err := refresher.Refresh(); err != nil {
			slog.Debug("maintenance: runtime registry refresh failed", "err", err)
			return
		}
	}
	h.stateMu.Lock()
	bindings := h.runtimeRoutes.snapshot()
	h.stateMu.Unlock()
	ended := make(map[int]string)
	for slot, b := range bindings {
		if !h.runtimeSessionLive(b.runtime) {
			ended[slot] = b.runtime
		}
	}
	if len(ended) == 0 {
		return
	}
	h.stateMu.Lock()
	closed := h.releaseEndedRuntimeLocked(ended, now)
	h.stateMu.Unlock()
	h.finishClosedWindows(closed)
}

// releaseEndedRuntimeLocked releases each slot still bound to the ended
// runtime session it maps to and closes the non-Kitty ones, returning the
// slots the caller must finish closing after releasing stateMu. Callers hold
// stateMu.
func (h *Handler) releaseEndedRuntimeLocked(ended map[int]string, now time.Time) map[int]struct{} {
	closed := make(map[int]struct{})
	for slot, runtime := range ended {
		if b, ok := h.runtimeRoutes.binding(slot); !ok || b.runtime != runtime {
			continue
		}
		h.runtimeRoutes.release(slot)
		if isRuntimeSyntheticSlot(slot) {
			closed[slot] = struct{}{}
		}
	}
	h.dropClosedWindowsLocked(now, closed)
	return closed
}

// runtimeSlotsLocked projects the slot table for state-query clients. Callers
// hold stateMu.
func (h *Handler) runtimeSlotsLocked() map[int]protocol.RuntimeSlot {
	if len(h.runtimeRoutes.bySlot) == 0 {
		return nil
	}
	slots := make(map[int]protocol.RuntimeSlot, len(h.runtimeRoutes.bySlot))
	for slot, b := range h.runtimeRoutes.bySlot {
		slots[slot] = protocol.RuntimeSlot{RuntimeSessionID: b.runtime, Backend: b.target.backend}
	}
	return slots
}

// runtimeSessionSlot returns the slot bound to a runtime session, or zero.
func (h *Handler) runtimeSessionSlot(id string) int {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.runtimeRoutes.byRuntime[id]
}

// operatorRuntimeUpdate addresses an operator state update (clear, mark
// active) to the slot of the runtime session it names. Any other action, and
// a session with no slot, is refused: hooks reach a slot only through
// translateHookOrigin.
func (h *Handler) operatorRuntimeUpdate(req protocol.Request) (protocol.Request, bool) {
	if req.Action != "user_clear" && req.Action != "user_active" {
		return req, false
	}
	slot := h.runtimeSessionSlot(req.RuntimeSessionID)
	if slot <= 0 {
		return req, false
	}
	req.WindowID = slot
	return req, true
}
