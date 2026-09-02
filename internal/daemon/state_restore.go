package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func (h *Handler) ensureWindowStateLocked(windowID int) *state.WindowState {
	if h.store == nil {
		h.store = make(map[int]*state.WindowState)
	}
	st := h.store[windowID]
	if st == nil {
		st = &state.WindowState{}
		h.store[windowID] = st
		// A window ID coming back to life (Kitty ID reuse, or an event racing
		// the close) is no longer a tombstone; its diagnostics resume live.
		h.reviveTombstoneLocked(windowID)
	}
	return st
}

func (h *Handler) windowStateLocked(windowID int) (*state.WindowState, bool) {
	st, ok := h.store[windowID]
	return st, ok
}

// markWindowDirtyLocked records a visible-state change (effective icon or
// loop marker) and queues the window for an immediate render.
func (h *Handler) markWindowDirtyLocked(windowID int) {
	st := h.markWindowStateDirtyLocked(windowID)
	h.enqueueRenderLocked(windowID, st)
}

// markWindowActivityDirtyLocked records a durable change that does not alter
// the visible state (activity timestamps, bookkeeping). The window is NOT
// queued: batched persistence lands on the next maintenance flush, so a busy
// session cannot force a Kitty write per event.
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

func (h *Handler) rememberKittySocketLocked(windowID int, socket string) {
	socket = strings.TrimSpace(socket)
	// Never let an unusable value (empty or a process-local "fd:" descriptor)
	// replace an existing stable socket in per-window or fallback storage.
	if !isReusableKittySocket(socket) {
		return
	}
	if h.kittySockets == nil {
		h.kittySockets = make(map[int]string)
	}
	h.kittySockets[windowID] = socket
	h.lastKittyListenOn = socket
	h.syncRendererMetadataLocked(windowID)
}

// isReusableKittySocket reports whether socket is a transferable Kitty
// remote-control endpoint that a different process (the detached daemon) can
// dial. It defers to kitty.IsReusableListenAddress so per-window/fallback
// storage and the listener normalization path share one validation rule:
// empty values, process-local "fd:" descriptors, prefix-only "unix:"/"tcp:"
// endpoints, and unknown forms are all rejected rather than guessed at.
func isReusableKittySocket(socket string) bool {
	return kitty.IsReusableListenAddress(socket)
}

func (h *Handler) restoreFromUserVars(windows []kitty.OSWindow, now time.Time) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	for i := range windows {
		for j := range windows[i].Tabs {
			tab := windows[i].Tabs[j]
			for k := range windows[i].Tabs[j].Windows {
				win := windows[i].Tabs[j].Windows[k]
				if existing, live := h.store[win.ID]; live {
					// Restore is a one-shot startup seeding of durable state.
					// A window that already holds in-process state was populated
					// either by an earlier restore or, during a degraded startup,
					// by a hook event that landed after the bounded snapshot timed
					// out. A late-completing startup restore (or the maintenance
					// recovery restore) must never overwrite those newer reducer
					// facts, ASL leases, or activity, so the accepted in-process
					// state stays authoritative. But a hook creates state for its
					// own source only, so blindly skipping the window permanently
					// loses every disjoint durable fact the persisted amx_state
					// still carries (a live ASL lease, pipe child, subagent, or
					// attention/error contribution). Merge those disjoint facts
					// in without touching any same-source in-process fact; the
					// merge is idempotent, so repeated maintenance ticks and a
					// late startup completion cannot duplicate or regress it.
					h.mergePersistedFactsLocked(win.ID, existing, win, now)
					continue
				}
				if win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
					continue
				}
				if strings.TrimSpace(win.UserVars.AMXState) == "" {
					continue
				}
				var st state.WindowState
				if err := st.UnmarshalUserVar(win.UserVars.AMXState); err != nil {
					slog.Warn("renderer: discarded invalid restored state", "window_id", win.ID, "err", err)
					continue
				}
				if desired := rendering.DesiredIcon(&st); desired != palette.ParseTitleParts(tab.Title).State {
					slog.Warn("renderer: restored state differs from live title", "window_id", win.ID, "desired_icon", desired, "title_icon", palette.ParseTitleParts(tab.Title).State)
				}
				if st.GuardExpiresAt.IsZero() || !st.GuardExpiresAt.After(now) {
					st.GuardAction = ""
					st.GuardExpiresAt = time.Time{}
				} else if h.guard != nil {
					h.guard.Restore(win.ID, st.GuardAction, st.GuardExpiresAt)
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
					st.RekeyMigratedFacts(win.ID)
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
					h.publishEffectiveLocked(win.ID, &st, st.Reducer.Reduce(now.Unix()))
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
				h.store[win.ID] = &st
				h.syncRendererMetadataLocked(win.ID)
			}
		}
	}
}

// mergePersistedFactsLocked folds the disjoint still-live reducer facts carried
// by a window's persisted amx_state into the accepted in-process state that a
// degraded-window hook already created. It is the merge half of the restore
// skip guard: the in-process state stays authoritative (Reducer.MergeDisjoint
// only adds sources the window does not already hold), so no accepted hook
// fact, sequence, reset flow, conversation identity, render generation, or
// timestamp is ever rolled backward. Merging nothing (no persisted facts, all
// same-source, or a malformed payload) leaves the window exactly as the hook
// left it, which keeps the operation idempotent across repeated maintenance
// ticks and a late-completing startup restore. Callers hold stateMu.
func (h *Handler) mergePersistedFactsLocked(windowID int, st *state.WindowState, win kitty.Window, now time.Time) {
	if win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
		return
	}
	if strings.TrimSpace(win.UserVars.AMXState) == "" {
		return
	}
	var persisted state.WindowState
	if err := persisted.UnmarshalUserVar(win.UserVars.AMXState); err != nil {
		slog.Warn("renderer: discarded invalid restored state on merge", "window_id", windowID, "err", err)
		return
	}
	if persisted.Reducer == nil {
		return
	}
	facts := persisted.Reducer.Facts()
	if len(facts) == 0 {
		return
	}
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	if st.Reducer.MergeDisjoint(facts) == 0 {
		return
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
}
