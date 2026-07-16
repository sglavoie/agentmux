package daemon

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// WindowState is the daemon's mutable per-window state. Handler.stateMu remains
// the single guard for all access.
type WindowState struct {
	Activity                   time.Time
	LastAction                 string
	LastAccount                string
	LastHarness                string
	LastActiveEvent            string
	CopilotToolPermissionState copilotToolPermissionState
	SubagentCount              int
	PendingReset               resetFlowState
	NotifiedStale              bool
	ContextLimitNotified       bool
	LastSessionTitle           string
	LastPipeMode               bool
	Muted                      bool
	EventLog                   []SessionEvent
	Looping                    bool
	LoopingSet                 bool
	DesiredIcon                string
	StateSet                   bool
	Dirty                      bool
	Seq                        uint64
	RenderFailures             int
	LastRenderError            string
	GuardAction                string
	GuardExpiresAt             time.Time

	// Render-generation diagnostics. Seq is the window's generation counter;
	// these track how far each stage of the render pipeline has advanced so
	// exports can show where a window is stuck. DirtySince is when the current
	// dirty stretch began (zero when clean); LastRenderLatency measures
	// mark-dirty → fully-rendered for the most recent successful pass.
	QueuedGen         uint64
	AttemptedGen      uint64
	PersistedGen      uint64
	RenderedGen       uint64
	FailedGen         uint64
	DirtySince        time.Time
	LastRenderLatency time.Duration
	RenderGenerations []RenderGenerationTrace

	// Shadow reducer state (source-scoped facts).
	// Reducer is the authoritative source for this window's visible icon and
	// loop state: handleStateUpdate feeds it, and reduceStateUpdate publishes
	// its reduced effective state through publishEffectiveLocked as the visible
	// icon and looping. Schema-v4 persistence carries it across restarts. The
	// legacy scalar machine above (LastAction, Looping, DesiredIcon, …) is
	// retained for wire/restore compatibility and as divergence evidence
	// (mismatches recorded against the reduced outcome), not as the visible-state
	// writer. Reducer is nil until the first shadow-reduced event (or a v2/v1
	// restore) touches this window.
	Reducer *Reducer
	// Effective is the last shadow-reduced outcome; EffectiveGen increments
	// each time it changes so restarts and exports can order generations.
	Effective    EffectiveState
	EffectiveGen uint64
	// ShadowSeq is the daemon-assigned per-window receipt generation. It
	// advances for every receipt-relevant state transition, including a
	// reset-sensitive stop held for deferred application; reduced events that
	// carry no source_sequence also use it as their per-source sequence.
	ShadowSeq uint64

	// notifiedSourceEvents dedupes reducer-transition notification decisions
	// by upstream source event ID; notifiedSourceOrder is its bounded eviction
	// ring. Unexported and never persisted: a daemon restart re-arms at most
	// one duplicate notification per retried upstream event.
	notifiedSourceEvents map[string]struct{}
	notifiedSourceOrder  []string

	// Watchdog idle accounting (unexported, never persisted). The watchdog
	// measures staleness by accumulating in-process observed elapsed time
	// instead of comparing wall-clock timestamps, so a system sleep or clock
	// jump can never expire a session that produced no in-process idle time.
	// watchdogIdle accumulates while watchdogAnchor (the Activity value the
	// accumulation is anchored to) is unchanged; fresh activity re-anchors and
	// reseeds. watchdogFactIdle tracks the same accumulation per source fact.
	watchdogSeeded   bool
	watchdogAnchor   time.Time
	watchdogIdle     time.Duration
	watchdogFactIdle map[SourceID]*factIdle
}

// CurrentConversation derives this window's current parent conversation from
// the reducer facts. It deliberately does not consult LastHarness or any
// separately persisted identity: both are descriptive metadata and can outlive
// a reused Kitty pane.
func (s WindowState) CurrentConversation(now int64) (conversation.Identity, bool) {
	if s.Reducer == nil {
		return conversation.Identity{}, false
	}
	return s.Reducer.CurrentConversation(now)
}

// CurrentInteractiveConversation derives this window's current interactive parent
// conversation, never falling back to a sole pipe-mode child. It shares the
// descriptive-metadata caveats of CurrentConversation.
func (s WindowState) CurrentInteractiveConversation(now int64) (conversation.Identity, bool) {
	if s.Reducer == nil {
		return conversation.Identity{}, false
	}
	return s.Reducer.CurrentInteractiveConversation(now)
}

// amx_state schema versions. v1 payloads predate the discriminator entirely
// (decoded Schema stays 0). v2 introduced source facts; v3 added durable route
// origins and acknowledgement timestamps; v4 adds validated harness-process
// ownership to eligible facts.
const (
	stateUserVarSchemaV2 = 2
	stateUserVarSchemaV3 = 3
	stateUserVarSchemaV4 = 4

	// harnessProcessTokenVersionUTC distinguishes process fingerprints written
	// after both ps producers began pinning TZ=UTC. Early schema-v4 payloads
	// omitted this marker and may contain tokens rendered in any timezone; their
	// underlying facts remain valid, but lifecycle ownership must fail open.
	harnessProcessTokenVersionUTC = 1
)

// legacyV1MigrationKey is the synthetic source key given to facts migrated
// from a schema-v1 payload, whose scalar action carries no source identity.
const legacyV1MigrationKey = "v1"

type windowStateUserVar struct {
	Schema                     int                        `json:"v,omitempty"`
	Action                     string                     `json:"a,omitempty"`
	GuardAction                string                     `json:"g,omitempty"`
	GuardExpiresAt             int64                      `json:"gx,omitempty"`
	CopilotToolPermissionState copilotToolPermissionState `json:"cp,omitempty"`
	SubagentCount              int                        `json:"sc,omitempty"`
	LastActiveEvent            string                     `json:"lae,omitempty"`
	Looping                    bool                       `json:"l,omitempty"`
	NotifiedStale              bool                       `json:"ns,omitempty"`
	ContextLimitNotified       bool                       `json:"cln,omitempty"`
	PendingReset               resetFlowState             `json:"pr,omitempty"`
	PipeMode                   bool                       `json:"pm,omitempty"`
	Account                    string                     `json:"acct,omitempty"`
	Harness                    string                     `json:"h,omitempty"`
	LastEventAt                int64                      `json:"t,omitempty"`
	ProcessTokenVersion        int                        `json:"pv,omitempty"`

	// Schema v2+: bounded source facts plus the shadow effective generation.
	Facts            []SourceFactSnapshot `json:"f,omitempty"`
	EffectiveIcon    string               `json:"ei,omitempty"`
	EffectiveLooping bool                 `json:"el,omitempty"`
	EffectiveKind    string               `json:"ek,omitempty"`
	EffectiveKey     string               `json:"ey,omitempty"`
	EffectiveGen     uint64               `json:"eg,omitempty"`
	ShadowSeq        uint64               `json:"sq,omitempty"`
	// Conversation is a redundant, reader-friendly projection of Facts. It is
	// written for durable diagnostics but never trusted on restore; Facts remain
	// the authoritative source and prevent a stale projection from claiming a
	// reused Kitty window.
	Conversation *conversation.Identity `json:"c,omitempty"`
}

// MarshalUserVar returns compact JSON for Kitty's amx_state user var. The
// event ring buffer is intentionally not persisted. It always writes schema
// v4: the reducer's facts and effective generation are the authoritative record
// of the visible icon and looping, and the legacy scalar fields ride alongside
// as compatibility metadata and divergence evidence (older readers and v1
// restore still parse them; they no longer write the visible state). The facts
// were already bounded by Compact on the shadow path, so only live/recent facts
// reach the payload.
func (s WindowState) MarshalUserVar() (string, error) {
	uv := windowStateUserVar{
		Schema:                     stateUserVarSchemaV4,
		Action:                     s.LastAction,
		GuardAction:                s.GuardAction,
		CopilotToolPermissionState: s.CopilotToolPermissionState,
		SubagentCount:              s.SubagentCount,
		LastActiveEvent:            s.LastActiveEvent,
		Looping:                    s.Looping,
		NotifiedStale:              s.NotifiedStale,
		ContextLimitNotified:       s.ContextLimitNotified,
		PendingReset:               s.PendingReset,
		PipeMode:                   s.LastPipeMode,
		Account:                    s.LastAccount,
		Harness:                    s.LastHarness,
	}
	if !s.GuardExpiresAt.IsZero() {
		uv.GuardExpiresAt = s.GuardExpiresAt.Unix()
	}
	if !s.Activity.IsZero() {
		uv.LastEventAt = s.Activity.Unix()
	}
	if s.Reducer != nil {
		uv.Facts = s.Reducer.Facts()
		for _, fact := range uv.Facts {
			if fact.HarnessProcessHarness != "" || fact.HarnessProcessPID != 0 ||
				fact.HarnessProcessStartToken != "" {
				uv.ProcessTokenVersion = harnessProcessTokenVersionUTC
				break
			}
		}
	}
	uv.EffectiveIcon = s.Effective.Icon
	uv.EffectiveLooping = s.Effective.Looping
	uv.EffectiveKind = string(s.Effective.Source.Kind)
	uv.EffectiveKey = s.Effective.Source.Key
	uv.EffectiveGen = s.EffectiveGen
	uv.ShadowSeq = s.ShadowSeq
	if identity, ok := s.CurrentConversation(time.Now().Unix()); ok {
		identityCopy := identity
		uv.Conversation = &identityCopy
	}
	data, err := json.Marshal(uv)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UnmarshalUserVar restores WindowState from Kitty's amx_state user var.
// The event ring buffer is intentionally left empty. All wire schemas are
// readable: v2/v3/v4 payloads restore the reducer's source facts (including
// acknowledgement cutoffs and ASL leases) and the effective generation, while
// a v1 payload — which predates the schema discriminator — migrates its
// scalar action and loop marker into synthetic legacy-source facts. The v2+
// conversation projection is intentionally re-derived from Facts rather than
// trusted directly. Writing always produces v4 (see MarshalUserVar), so v1-v3
// exist only on the read side. Process ownership is admitted only from v4
// payloads whose token-version marker proves their ps lstart values were
// rendered under UTC. Older schemas and pre-marker v4 payloads keep their
// watchdog-only fallback even if process fields are present.
func (s *WindowState) UnmarshalUserVar(value string) error {
	if strings.TrimSpace(value) == "" {
		*s = WindowState{}
		return nil
	}
	var uv windowStateUserVar
	if err := json.Unmarshal([]byte(value), &uv); err != nil {
		return err
	}
	*s = WindowState{
		LastAction:                 uv.Action,
		GuardAction:                uv.GuardAction,
		CopilotToolPermissionState: uv.CopilotToolPermissionState,
		SubagentCount:              uv.SubagentCount,
		LastActiveEvent:            uv.LastActiveEvent,
		Looping:                    uv.Looping,
		NotifiedStale:              uv.NotifiedStale,
		ContextLimitNotified:       uv.ContextLimitNotified,
		PendingReset:               uv.PendingReset,
		LastPipeMode:               uv.PipeMode,
		LastAccount:                uv.Account,
		LastHarness:                uv.Harness,
	}
	if uv.GuardExpiresAt != 0 {
		s.GuardExpiresAt = time.Unix(uv.GuardExpiresAt, 0)
	}
	if uv.LastEventAt != 0 {
		s.Activity = time.Unix(uv.LastEventAt, 0)
	}
	s.LoopingSet = true
	s.StateSet = true
	s.DesiredIcon = IconState(s.LastAction)
	if uv.Schema >= stateUserVarSchemaV2 {
		s.restoreReducerV2(uv)
		// The reducer is authoritative for visible state, so a v2/v3/v4 restore with
		// facts resumes from the persisted effective state rather than the
		// legacy scalar (they only diverge for payloads written before the
		// cutover). A fact-less payload keeps the legacy-derived view.
		if s.Reducer != nil {
			s.DesiredIcon = s.Effective.Icon
			s.Looping = s.Effective.Looping
		}
	} else {
		s.migrateReducerV1(uv)
	}
	return nil
}

// restoreReducerV2 rebuilds the shadow reducer from a schema-v2/v3/v4 payload. The
// reducer is created with zero thresholds; the restore path re-binds the
// daemon's configured ReducerConfig afterward (restoreFromUserVars), since a
// deserializer has no access to the owning Handler.
func (s *WindowState) restoreReducerV2(uv windowStateUserVar) {
	if len(uv.Facts) > 0 {
		facts := uv.Facts
		if uv.Schema < stateUserVarSchemaV4 || uv.ProcessTokenVersion != harnessProcessTokenVersionUTC {
			facts = append([]SourceFactSnapshot(nil), facts...)
			for i := range facts {
				facts[i].clearHarnessProcessIdentity()
			}
		}
		r := NewReducer(ReducerConfig{})
		r.Restore(facts)
		s.Reducer = r
	}
	s.Effective = EffectiveState{
		Icon:    uv.EffectiveIcon,
		Looping: uv.EffectiveLooping,
	}
	if uv.EffectiveKind != "" {
		s.Effective.Source = SourceID{Kind: SourceKind(uv.EffectiveKind), Key: uv.EffectiveKey}
	}
	s.EffectiveGen = uv.EffectiveGen
	s.ShadowSeq = uv.ShadowSeq
	// Never hand out a receipt sequence at or below a restored fact's: the
	// reducer would discard the next live event as reordered.
	if s.Reducer != nil {
		if maxSeq := s.Reducer.MaxSeq(); s.ShadowSeq < maxSeq {
			s.ShadowSeq = maxSeq
		}
	}
}

// migrateReducerV1 converts a schema-v1 payload into synthetic reducer facts.
// The scalar action carries no source identity, so it becomes a legacy-kind
// fact (guard-grace terminal suppression); a persisted loop marker becomes a
// between-turns ASL lease so the shadow loop marker survives the migration.
func (s *WindowState) migrateReducerV1(uv windowStateUserVar) {
	var facts []SourceFactSnapshot
	if uv.Action != "" {
		fact := SourceFactSnapshot{
			Kind:   SourceLegacy,
			Key:    legacyV1MigrationKey,
			Action: uv.Action,
			Icon:   reducerActionIcon(uv.Action),
			Seq:    1,
			At:     uv.LastEventAt,
		}
		if isTerminalAction(uv.Action) {
			fact.Terminated = true
			fact.TerminalAt = uv.LastEventAt
		}
		facts = append(facts, fact)
	}
	if uv.Looping {
		facts = append(facts, SourceFactSnapshot{
			Kind:      SourceASL,
			Key:       legacyV1MigrationKey,
			Action:    "idle",
			Seq:       1,
			At:        uv.LastEventAt,
			Phase:     ASLPhaseBetween,
			LeaseLive: true,
		})
	}
	if len(facts) == 0 {
		return
	}
	r := NewReducer(ReducerConfig{})
	r.Restore(facts)
	s.Reducer = r
	s.ShadowSeq = 1
	s.Effective = EffectiveState{Looping: uv.Looping}
	if icon := reducerActionIcon(uv.Action); icon != "" {
		s.Effective.Icon = icon
		s.Effective.Source = SourceID{Kind: SourceLegacy, Key: legacyV1MigrationKey}
	}
}

func (h *Handler) ensureWindowStateLocked(windowID int) *WindowState {
	if h.store == nil {
		h.store = make(map[int]*WindowState)
	}
	st := h.store[windowID]
	if st == nil {
		st = &WindowState{}
		h.store[windowID] = st
		// A window ID coming back to life (Kitty ID reuse, or an event racing
		// the close) is no longer a tombstone; its diagnostics resume live.
		h.reviveTombstoneLocked(windowID)
	}
	return st
}

func (h *Handler) windowStateLocked(windowID int) (*WindowState, bool) {
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

func (h *Handler) markWindowStateDirtyLocked(windowID int) *WindowState {
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
				var st WindowState
				if err := st.UnmarshalUserVar(win.UserVars.AMXState); err != nil {
					slog.Warn("renderer: discarded invalid restored state", "window_id", win.ID, "err", err)
					continue
				}
				if desired := desiredIconForState(&st); desired != palette.ParseTitleParts(tab.Title).State {
					slog.Warn("renderer: restored state differs from live title", "window_id", win.ID, "desired_icon", desired, "title_icon", palette.ParseTitleParts(tab.Title).State)
				}
				if st.GuardExpiresAt.IsZero() || !st.GuardExpiresAt.After(now) {
					st.GuardAction = ""
					st.GuardExpiresAt = time.Time{}
				} else if h.guard != nil {
					h.guard.Restore(win.ID, st.GuardAction, st.GuardExpiresAt)
				}
				if st.Reducer == nil && st.DesiredIcon == "" && st.LastAction != "" {
					st.DesiredIcon = IconState(st.LastAction)
				}
				if st.Reducer != nil {
					// Deserialization built the reducer with zero thresholds;
					// re-bind the daemon's configured ones so staleness and the
					// legacy grace behave identically after a restart, and move
					// v1-migrated facts onto the window's live legacy key so
					// post-restart events collide with the restored fact
					// (same-source terminal suppression, ASL lease release).
					st.Reducer.SetConfig(h.reducerCfg)
					if st.Reducer.RekeyMigratedFacts(win.ID) &&
						st.Effective.Source.Key == legacyV1MigrationKey {
						st.Effective.Source.Key = windowKey(win.ID)
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
					h.publishEffectiveLocked(win.ID, &st, st.Reducer.Reduce(now.Unix()))
				}
				st.Dirty = true
				st.DirtySince = now
				st.Seq++
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
func (h *Handler) mergePersistedFactsLocked(windowID int, st *WindowState, win kitty.Window, now time.Time) {
	if win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
		return
	}
	if strings.TrimSpace(win.UserVars.AMXState) == "" {
		return
	}
	var persisted WindowState
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
		st.Reducer = NewReducer(h.reducerCfg)
	}
	if st.Reducer.MergeDisjoint(facts) == 0 {
		return
	}
	// Re-bind the daemon's configured thresholds (the merged facts arrived from a
	// zero-threshold deserializer) and move any v1-migrated legacy/ASL facts onto
	// the window's live key so post-restart events collide with them exactly as a
	// full restore would.
	st.Reducer.SetConfig(h.reducerCfg)
	if st.Reducer.RekeyMigratedFacts(windowID) && st.Effective.Source.Key == legacyV1MigrationKey {
		st.Effective.Source.Key = windowKey(windowID)
	}
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
