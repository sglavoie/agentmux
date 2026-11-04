package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func (h *Handler) ensureWindowStateLocked(windowID int) *state.WindowState {
	if h.store == nil {
		h.store = make(map[int]*state.WindowState)
	}
	st := h.store[windowID]
	if st == nil {
		if h.runtimeSlotClosedLocked(windowID) {
			// A slot no runtime session holds is closed for good:
			// retirement walks bindings, so state
			// stored there would never be dropped. Work that reaches it late (a
			// hook translated before its session ended, a probe or timer that
			// outlived it) mutates a detached state instead. A slot a session
			// claims is bound before its first update, and the checkpoint
			// restore stores its state directly.
			return &state.WindowState{}
		}
		st = &state.WindowState{}
		h.store[windowID] = st
		// A slot ID coming back to life (ID reuse, or an event racing
		// the close) is no longer a tombstone; its diagnostics resume live.
		h.reviveTombstoneLocked(windowID)
	}
	return st
}

func (h *Handler) windowStateLocked(windowID int) (*state.WindowState, bool) {
	st, ok := h.store[windowID]
	return st, ok
}

// markWindowDirtyLocked records a visible-state change (effective icon, loop
// marker, or the unseen bit that decides which dashboard group the row sits in)
// and queues its generation for checkpointing. The unseen bit is persisted with
// the reducer state so a row the user has just read survives a daemon restart.
func (h *Handler) markWindowDirtyLocked(windowID int) {
	st := h.markWindowStateDirtyLocked(windowID)
	h.enqueueRenderLocked(windowID, st)
}

// markWindowActivityDirtyLocked records a durable change that does not alter
// the visible state (activity timestamps, bookkeeping). The window is NOT
// queued: batched persistence lands on the next maintenance flush, so a busy
// session does not force a checkpoint write per event.
func (h *Handler) markWindowActivityDirtyLocked(windowID int) {
	h.markWindowStateDirtyLocked(windowID)
}

func (h *Handler) markWindowStateDirtyLocked(windowID int) *state.WindowState {
	st := h.ensureWindowStateLocked(windowID)
	if !st.Dirty {
		st.DirtySince = time.Now()
	}
	st.Dirty = true
	st.Seq++
	if h.guard != nil {
		if action, expiresAt, ok := h.guard.Snapshot(windowID); ok {
			st.GuardAction = action
			st.GuardExpiresAt = expiresAt
		} else {
			st.GuardAction = ""
			st.GuardExpiresAt = time.Time{}
		}
	}
	return st
}

// restoreWindowPayloadLocked rebuilds a slot's state from its runtime checkpoint
// payload and installs it. The caller has checked
// that the slot holds no in-process state, which would otherwise win. A slot
// the runtime routes already bind to a runtime session restores bound to it,
// so the session's next event keeps the restored facts. Callers hold stateMu.
func (h *Handler) restoreWindowPayloadLocked(slot int, payload string, now time.Time) (*state.WindowState, error) {
	var st state.WindowState
	if err := st.UnmarshalStatePayload(payload); err != nil {
		return nil, err
	}
	if st.GuardExpiresAt.IsZero() || !st.GuardExpiresAt.After(now) {
		st.GuardAction = ""
		st.GuardExpiresAt = time.Time{}
	} else if h.guard != nil {
		h.guard.Restore(slot, st.GuardAction, st.GuardExpiresAt)
	}
	if st.Reducer == nil && st.DesiredIcon == "" && st.LastAction != "" {
		st.DesiredIcon = protocol.IconState(st.LastAction)
	}
	if st.Reducer != nil {
		// Deserialization built the reducer with zero thresholds;
		// re-bind the daemon's configured ones so staleness and the
		// legacy grace behave identically after a restart, and move
		// v1-migrated facts onto the window's live legacy key so
		// post-restart events collide with the restored fact
		// (same-source terminal suppression, ASL lease release).
		st.Reducer.SetConfig(h.reducerCfg)
		st.RekeyMigratedFacts(slot)
		if b, ok := h.runtimeRoutes.binding(slot); ok {
			st.Reducer.BindRuntimeSession(b.runtime)
		}
		// Republish from the reducer so the restored visible state
		// is the reduced state, not the legacy scalar. Without
		// this, a window whose persisted terminal fact aged past
		// the tombstone TTL while the daemon was down restores
		// with an empty reducer but a stale LastAction icon, and —
		// because effective state is only published on the next
		// event — an idle window holds that shadow_current_
		// mismatch forever (observed live on the first installed
		// restart: three idle windows pinned "stop" against an
		// empty reducer, failing doctor/audit permanently).
		st.Reducer.Compact(now.Unix())
		h.publishEffectiveLocked(slot, &st, st.Reducer.Reduce(now.Unix()))
	}
	st.Dirty = true
	st.DirtySince = now
	st.Seq++
	// A fact-less payload never reaches publishEffectiveLocked above
	// yet still restores an icon, so re-assert the anchor invariant
	// against the icon this window actually restored with: a
	// pre-upgrade payload self-heals to now and a persisted anchor
	// that restored onto a non-active icon is dropped.
	stampBusySinceLocked(&st, rendering.DesiredIcon(&st), now)
	h.store[slot] = &st
	h.syncRendererMetadataLocked(slot)
	return &st, nil
}

// mergePersistedPayloadLocked is mergePersistedFactsLocked for a payload
// already read from wherever its backend persists it. It reports only a
// malformed payload; merging nothing is not an error. Callers hold stateMu.
func (h *Handler) mergePersistedPayloadLocked(windowID int, st *state.WindowState, payload string, now time.Time) error {
	var persisted state.WindowState
	if err := persisted.UnmarshalStatePayload(payload); err != nil {
		return err
	}
	if persisted.Reducer == nil {
		return nil
	}
	facts := persisted.Reducer.Facts()
	if len(facts) == 0 {
		return nil
	}
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	if st.Reducer.MergeDisjoint(facts) == 0 {
		return nil
	}
	// Re-bind the daemon's configured thresholds (the merged facts arrived from a
	// zero-threshold deserializer) and move any v1-migrated legacy/ASL facts onto
	// the window's live key so post-restart events collide with them exactly as a
	// full restore would.
	st.Reducer.SetConfig(h.reducerCfg)
	st.RekeyMigratedFacts(windowID)
	st.Reducer.Compact(now.Unix())
	// A nonzero merge imported disjoint facts — a real reducer mutation — so
	// advance the window-wide receipt generation at least once, independently of
	// the merged facts' per-source sequences: an authoritative idle observation
	// captured before this merge must be discarded rather than allowed to settle
	// over the newly surfaced state. Then never hand out a receipt sequence at or
	// below a merged fact's, or the reducer would discard the next live event for
	// that source as reordered.
	st.ShadowSeq++
	if maxSeq := st.Reducer.MaxSeq(); st.ShadowSeq < maxSeq {
		st.ShadowSeq = maxSeq
	}
	// Republish from the full merged fact set. publishEffectiveLocked only ever
	// advances the effective generation, so a merged ASL lease (🔁) or disjoint
	// attention/error fact surfaces without regressing the hook's own icon.
	h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
	if !st.Dirty {
		st.DirtySince = now
	}
	st.Dirty = true
	st.Seq++
	h.syncRendererMetadataLocked(windowID)
	return nil
}
