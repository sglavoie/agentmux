package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// This file is the backend-neutral reducer checkpoint. A Kitty window's
// reducer state persists in the window's own amx_state user var, which Kitty
// keeps across a daemon restart; nothing like it exists for a tmux pane, so
// the daemon checkpoints every runtime-session slot to one file instead:
//
//   - every slot bound to a runtime session is recorded with the session's
//     target, which is what lets a restarted daemon route the session's next
//     event to the restored state rather than adopt an empty slot;
//   - a non-Kitty slot also records its amx_state payload, in exactly the
//     schema the Kitty user var carries, so one codec restores both;
//   - a Kitty slot records no payload: amx_state stays its durable record, and
//     the existing Kitty restore keeps reading it.
//
// The checkpoint is derived state, not a source artifact. A corrupt one is
// quarantined and the next flush starts it over; a session the registry no
// longer lists as live, or lists at another target, is dropped on restore
// rather than resurrected; and restored state never overwrites in-process
// state a fresh event created first — it can only add the disjoint facts the
// accepted state does not hold, the same merge the Kitty restore performs.

// runtimeCheckpointSchemaVersion versions the checkpoint file.
const runtimeCheckpointSchemaVersion = 1

// runtimeCheckpointFileName sits beside the runtime-session registry.
const runtimeCheckpointFileName = "runtime-state.json"

// errRuntimeCheckpointCorrupt marks a checkpoint file no restore can trust.
var errRuntimeCheckpointCorrupt = errors.New("runtime checkpoint is corrupt")

// DefaultRuntimeCheckpointPath returns the checkpoint location: the
// runtime-session registry's directory.
func DefaultRuntimeCheckpointPath() string {
	return filepath.Join(filepath.Dir(runtimesession.DefaultRegistryPath()), runtimeCheckpointFileName)
}

type runtimeCheckpointFile struct {
	SchemaVersion int                      `json:"schema_version"`
	Sessions      []runtimeCheckpointEntry `json:"sessions"`
}

// runtimeCheckpointEntry is one runtime-session slot as last checkpointed.
type runtimeCheckpointEntry struct {
	RuntimeSessionID string `json:"runtime_session_id"`
	Backend          string `json:"backend"`
	Instance         string `json:"instance"`
	Generation       string `json:"generation"`
	Pane             string `json:"pane"`
	// State is the slot's amx_state payload. A Kitty slot has none: Kitty
	// persists its state in the window's own user var.
	State string `json:"state,omitempty"`
}

func (e runtimeCheckpointEntry) target() (runtimeTarget, bool) {
	return runtimeTargetOf(&runtimesession.Target{
		Instance: runtimesession.BackendInstance{
			Kind:       runtimesession.BackendKind(e.Backend),
			Address:    e.Instance,
			Generation: e.Generation,
		},
		Ref: e.Pane,
	})
}

// runtimeRestoreOutcome is what restore did with one checkpointed session.
type runtimeRestoreOutcome string

const (
	// runtimeRestoreRestored: the slot was rebuilt (or, for Kitty, rebound).
	runtimeRestoreRestored runtimeRestoreOutcome = "restored"
	// runtimeRestoreMerged: a fresh event had already created the slot's
	// state; it stayed authoritative and only disjoint facts were added.
	runtimeRestoreMerged runtimeRestoreOutcome = "merged"
	// runtimeRestoreStale: the registry no longer lists the session live, or
	// lists it at a different target generation or pane.
	runtimeRestoreStale runtimeRestoreOutcome = "stale"
	// runtimeRestoreDeferred: the registry could not answer; the entry is
	// retried on maintenance and kept in the checkpoint meanwhile.
	runtimeRestoreDeferred runtimeRestoreOutcome = "deferred"
	// runtimeRestoreConflict: another runtime session already holds the slot.
	runtimeRestoreConflict runtimeRestoreOutcome = "conflict"
	// runtimeRestoreInvalid: the entry's identity, target, or payload is
	// malformed.
	runtimeRestoreInvalid runtimeRestoreOutcome = "invalid"
)

// runtimeRestoreReport summarizes one restore pass. Outcomes is keyed by the
// checkpointed runtime-session ID.
type runtimeRestoreReport struct {
	Corrupt     bool
	Quarantined string
	Outcomes    map[string]runtimeRestoreOutcome
}

// SetRuntimeCheckpointPath enables the runtime checkpoint at path. Call it on
// the startup goroutine before restoreRuntimeCheckpoint; an empty path (every
// test handler's default) disables both restore and flush.
func (h *Handler) SetRuntimeCheckpointPath(path string) {
	h.runtimeCheckpointPath = path
}

// loadRuntimeCheckpoint reads the checkpoint. A missing file is an empty one.
// Anything a strict decode refuses — unknown fields, an unsupported schema,
// trailing data, duplicate sessions — is errRuntimeCheckpointCorrupt.
func loadRuntimeCheckpoint(path string) ([]runtimeCheckpointEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file runtimeCheckpointFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%w: %v", errRuntimeCheckpointCorrupt, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", errRuntimeCheckpointCorrupt)
	}
	if file.SchemaVersion != runtimeCheckpointSchemaVersion {
		return nil, fmt.Errorf("%w: schema version %d", errRuntimeCheckpointCorrupt, file.SchemaVersion)
	}
	seen := make(map[string]bool, len(file.Sessions))
	for _, e := range file.Sessions {
		if seen[e.RuntimeSessionID] {
			return nil, fmt.Errorf("%w: duplicate session %q", errRuntimeCheckpointCorrupt, e.RuntimeSessionID)
		}
		seen[e.RuntimeSessionID] = true
	}
	return file.Sessions, nil
}

// restoreRuntimeCheckpoint restores every checkpointed runtime-session slot
// the registry still lists live at the same target. It runs once at startup,
// after the Kitty restore so a Kitty slot's restored reducer can be rebound,
// and reads the registry without stateMu.
func (h *Handler) restoreRuntimeCheckpoint(now time.Time) runtimeRestoreReport {
	report := runtimeRestoreReport{Outcomes: map[string]runtimeRestoreOutcome{}}
	if h.runtimeCheckpointPath == "" {
		return report
	}
	entries, err := loadRuntimeCheckpoint(h.runtimeCheckpointPath)
	if errors.Is(err, errRuntimeCheckpointCorrupt) {
		report.Corrupt = true
		dest, qerr := cachefile.Quarantine(h.runtimeCheckpointPath)
		if qerr != nil {
			slog.Warn("daemon: corrupt runtime checkpoint could not be quarantined", "err", err, "quarantine_err", qerr)
		} else {
			report.Quarantined = dest
			slog.Warn("daemon: quarantined corrupt runtime checkpoint; runtime sessions restore empty", "err", err, "quarantined", dest)
		}
		return report
	}
	if err != nil {
		slog.Warn("daemon: runtime checkpoint unreadable; runtime sessions restore empty", "err", err)
		return report
	}
	h.reconcileRuntimeEntries(entries, now, report.Outcomes)
	return report
}

// reconcileRuntimeEntries verifies each entry against the registry and
// applies the live ones. Entries the registry could not answer for replace
// the pending list, so maintenance retries them and the flush keeps them.
func (h *Handler) reconcileRuntimeEntries(entries []runtimeCheckpointEntry, now time.Time, outcomes map[string]runtimeRestoreOutcome) {
	var deferred []runtimeCheckpointEntry
	type verified struct {
		entry  runtimeCheckpointEntry
		target runtimeTarget
	}
	var apply []verified
	for _, e := range entries {
		outcome, target := h.verifyRuntimeEntry(e)
		switch outcome {
		case runtimeRestoreDeferred:
			deferred = append(deferred, e)
		case "":
			apply = append(apply, verified{entry: e, target: target})
			continue
		}
		outcomes[e.RuntimeSessionID] = outcome
	}

	h.stateMu.Lock()
	restored := false
	for _, v := range apply {
		outcome := h.applyRuntimeEntryLocked(v.entry, v.target, now)
		outcomes[v.entry.RuntimeSessionID] = outcome
		restored = restored || outcome == runtimeRestoreRestored || outcome == runtimeRestoreMerged
	}
	h.runtimeRestorePending = deferred
	h.stateMu.Unlock()
	if restored {
		h.companionDirty()
	}
}

// verifyRuntimeEntry checks one entry against the registry, off stateMu. An
// empty outcome means the session is live at exactly the checkpointed target.
func (h *Handler) verifyRuntimeEntry(e runtimeCheckpointEntry) (runtimeRestoreOutcome, runtimeTarget) {
	id, err := runtimesession.ParseID(e.RuntimeSessionID)
	if err != nil {
		return runtimeRestoreInvalid, runtimeTarget{}
	}
	target, ok := e.target()
	if !ok {
		return runtimeRestoreInvalid, runtimeTarget{}
	}
	if h.runtimeDirectory == nil {
		return runtimeRestoreDeferred, target
	}
	record, found, err := h.runtimeDirectory.Lookup(id)
	switch {
	case !found && err != nil:
		return runtimeRestoreDeferred, target
	case !found:
		return runtimeRestoreStale, target
	}
	switch record.State {
	case runtimesession.StateLive:
	case runtimesession.StatePending:
		// A checkpointed session was live once; the registry not agreeing yet
		// is an inconsistency to wait out, not evidence that it ended.
		return runtimeRestoreDeferred, target
	default:
		return runtimeRestoreStale, target
	}
	if current, ok := runtimeTargetOf(record.Target); !ok || current != target {
		return runtimeRestoreStale, target
	}
	return "", target
}

// applyRuntimeEntryLocked binds a verified entry's slot to its session and
// restores its state. In-process state always wins: a slot another session
// holds is left alone, and a slot a fresh event of this same session already
// created only gains the disjoint persisted facts. Callers hold stateMu.
func (h *Handler) applyRuntimeEntryLocked(e runtimeCheckpointEntry, target runtimeTarget, now time.Time) runtimeRestoreOutcome {
	slot := h.runtimeSlotForTargetLocked(target)
	if current := h.slotRuntimeLocked(slot); current != "" && current != e.RuntimeSessionID {
		h.runtimeRoutes.forgetUnboundSynthetic(target)
		return runtimeRestoreConflict
	}
	if b, ok := h.runtimeRoutes.binding(slot); ok && b.target != target {
		return runtimeRestoreConflict
	}
	if target.backend == string(runtimesession.BackendKitty) {
		// Kitty's own restore owns the state; the checkpoint contributes only
		// whom the slot belongs to.
		h.runtimeRoutes.bind(slot, runtimeBinding{runtime: e.RuntimeSessionID, target: target})
		if st, ok := h.windowStateLocked(slot); ok && st.Reducer != nil {
			st.Reducer.BindRuntimeSession(e.RuntimeSessionID)
		}
		return runtimeRestoreRestored
	}
	outcome := runtimeRestoreRestored
	if st, ok := h.windowStateLocked(slot); ok {
		if strings.TrimSpace(e.State) != "" {
			if err := h.mergePersistedPayloadLocked(slot, st, e.State, now); err != nil {
				slog.Warn("daemon: discarded invalid runtime checkpoint state on merge", "runtime_session_id", e.RuntimeSessionID, "err", err)
			}
		}
		outcome = runtimeRestoreMerged
	} else if strings.TrimSpace(e.State) != "" {
		if _, err := h.restoreWindowPayloadLocked(slot, e.State, now); err != nil {
			slog.Warn("daemon: discarded invalid runtime checkpoint state", "runtime_session_id", e.RuntimeSessionID, "err", err)
			h.runtimeRoutes.forgetUnboundSynthetic(target)
			return runtimeRestoreInvalid
		}
	}
	h.runtimeRoutes.bind(slot, runtimeBinding{runtime: e.RuntimeSessionID, target: target})
	if st, ok := h.windowStateLocked(slot); ok && st.Reducer != nil {
		st.Reducer.BindRuntimeSession(e.RuntimeSessionID)
	}
	return outcome
}

// retryDeferredRuntimeRestore re-verifies the entries startup could not, once
// the registry answers. It runs on the maintenance schedule.
func (h *Handler) retryDeferredRuntimeRestore(now time.Time) {
	h.stateMu.Lock()
	pending := h.runtimeRestorePending
	h.stateMu.Unlock()
	if len(pending) == 0 {
		return
	}
	outcomes := map[string]runtimeRestoreOutcome{}
	h.reconcileRuntimeEntries(pending, now, outcomes)
	for id, outcome := range outcomes {
		if outcome != runtimeRestoreDeferred {
			slog.Info("daemon: deferred runtime session restore settled", "runtime_session_id", id, "outcome", string(outcome))
		}
	}
}

// runtimeCheckpointEntriesLocked captures what the checkpoint should hold:
// every bound slot plus the entries still awaiting verification, in runtime-ID
// order so an unchanged world encodes to the same bytes. Callers hold stateMu.
func (h *Handler) runtimeCheckpointEntriesLocked() []runtimeCheckpointEntry {
	entries := make([]runtimeCheckpointEntry, 0, len(h.runtimeRoutes.bySlot)+len(h.runtimeRestorePending))
	present := make(map[string]bool, len(h.runtimeRoutes.bySlot))
	for slot, b := range h.runtimeRoutes.bySlot {
		e := runtimeCheckpointEntry{
			RuntimeSessionID: b.runtime,
			Backend:          b.target.backend,
			Instance:         b.target.instance,
			Generation:       b.target.generation,
			Pane:             b.target.pane,
		}
		if isRuntimeSyntheticSlot(slot) {
			if st, ok := h.windowStateLocked(slot); ok {
				payload, err := st.MarshalUserVar()
				if err != nil {
					slog.Warn("daemon: runtime checkpoint could not encode slot state", "runtime_session_id", b.runtime, "err", err)
				} else {
					e.State = payload
				}
			}
		}
		present[b.runtime] = true
		entries = append(entries, e)
	}
	for _, e := range h.runtimeRestorePending {
		if !present[e.RuntimeSessionID] {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RuntimeSessionID < entries[j].RuntimeSessionID })
	return entries
}

// flushRuntimeCheckpoint writes the checkpoint when its content changed since
// the last successful write. The copy is taken under stateMu; encoding and the
// atomic write run off it.
func (h *Handler) flushRuntimeCheckpoint() error {
	h.stateMu.Lock()
	entries := h.runtimeCheckpointEntriesLocked()
	captured := h.runtimeSlotGenerationsLocked()
	capturedAt := time.Now()
	h.stateMu.Unlock()

	if err := h.writeRuntimeCheckpoint(entries); err != nil {
		return err
	}
	h.settleRuntimeSlots(captured, capturedAt)
	return nil
}

// writeRuntimeCheckpoint encodes entries and replaces the file when its
// content changed since the last successful write. No path means no
// checkpoint is kept, which is not a failure.
func (h *Handler) writeRuntimeCheckpoint(entries []runtimeCheckpointEntry) error {
	if h.runtimeCheckpointPath == "" {
		return nil
	}
	data, err := json.Marshal(runtimeCheckpointFile{SchemaVersion: runtimeCheckpointSchemaVersion, Sessions: entries})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	h.runtimeCheckpointMu.Lock()
	defer h.runtimeCheckpointMu.Unlock()
	if h.runtimeCheckpointWritten && sum == h.runtimeCheckpointDigest {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.runtimeCheckpointPath), 0o700); err != nil {
		return err
	}
	if err := cachefile.WriteFileAtomic(h.runtimeCheckpointPath, data, 0o600); err != nil {
		return err
	}
	h.runtimeCheckpointDigest = sum
	h.runtimeCheckpointWritten = true
	return nil
}

// A Kitty window's state is durable once it is written to the window, and
// that write is what ends its dirty stretch. A slot of another backend has no
// window to write to: its state is durable once the runtime checkpoint holds
// it, and it is presented by whoever reads the daemon (a companion snapshot),
// not by a tab title. So the checkpoint is what settles such a slot. Without
// this it would stay dirty for as long as it lived, and doctor would report
// every tmux session as a renderer that fell behind.

// runtimeSlotGenerationsLocked is the generation of every slot of another
// backend with unsettled state, as the checkpoint being built captures it.
// Callers hold stateMu.
func (h *Handler) runtimeSlotGenerationsLocked() map[int]uint64 {
	var captured map[int]uint64
	for slot := range h.runtimeRoutes.bySlot {
		if !isRuntimeSyntheticSlot(slot) {
			continue
		}
		st, ok := h.windowStateLocked(slot)
		if !ok || (!st.Dirty && st.RenderedGen >= st.Seq) {
			continue
		}
		if captured == nil {
			captured = make(map[int]uint64)
		}
		captured[slot] = st.Seq
	}
	return captured
}

// settleRuntimeSlots records that the checkpoint holds the captured
// generations. A slot that changed again since the capture stays dirty, from
// the capture on: what came before it is durable.
func (h *Handler) settleRuntimeSlots(captured map[int]uint64, capturedAt time.Time) {
	if len(captured) == 0 {
		return
	}
	type settled struct {
		slot  int
		trace protocol.RenderGenerationTrace
	}
	traces := make([]settled, 0, len(captured))
	h.stateMu.Lock()
	for slot, seq := range captured {
		st, ok := h.windowStateLocked(slot)
		if !ok {
			continue
		}
		st.PersistedGen = max(st.PersistedGen, seq)
		st.RenderedGen = max(st.RenderedGen, seq)
		if st.Seq == seq {
			st.Dirty = false
			st.DirtySince = time.Time{}
		} else if st.DirtySince.Before(capturedAt) {
			st.DirtySince = capturedAt
		}
		traces = append(traces, settled{slot, h.markRenderStageLocked(slot, seq, rendering.StageRendered, "success", "runtime_checkpoint", false)})
	}
	h.stateMu.Unlock()
	for _, s := range traces {
		h.recordRenderGenerationBounded(s.trace, s.slot)
	}
}

// maintainRuntimeSessions is the runtime-session half of maintenance:
// reconcile the registry with the backends, retire the sessions it no longer
// lists as live, retry deferred restores, adopt the live sessions no hook has
// claimed a slot for, checkpoint what is left, and render the settled state
// into the sessions' terminals. It
// needs no Kitty listing, so it runs whether or not Kitty answered.
func (h *Handler) maintainRuntimeSessions(now time.Time) {
	h.reconcileRuntimeSessions(now)
	h.retireEndedRuntimeSessions(now)
	h.retryDeferredRuntimeRestore(now)
	h.adoptRuntimeSessions()
	h.labelRuntimeSessions(now)
	if err := h.flushRuntimeCheckpoint(); err != nil {
		slog.Warn("maintenance: runtime checkpoint write failed", "err", err)
	}
	h.renderRuntimeStatuses(now)
}
