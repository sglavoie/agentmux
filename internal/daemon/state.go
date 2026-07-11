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

	// Shadow reducer state (source-scoped facts). The reducer runs beside the
	// legacy scalar machine above: handleStateUpdate feeds it, schema-v2
	// persistence carries it across restarts, and its effective state is
	// compared against the legacy render decision — but it never drives the
	// visible icon. Reducer is nil until the first shadow-reduced event (or a
	// v2/v1 restore) touches this window.
	Reducer *Reducer
	// Effective is the last shadow-reduced outcome; EffectiveGen increments
	// each time it changes so restarts and exports can order generations.
	Effective    EffectiveState
	EffectiveGen uint64
	// ShadowSeq is the daemon-assigned per-window receipt sequence stamped
	// onto reduced events that carry no source_sequence, so per-source
	// monotonicity still holds for today's sequence-less harness payloads.
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

// stateUserVarSchemaV2 marks the current amx_state wire schema. v1 payloads
// predate the field entirely (decoded Schema stays 0), so dual-read migration
// keys off its absence.
const stateUserVarSchemaV2 = 2

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

	// Schema v2: bounded source facts plus the shadow effective generation.
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
// v2: the legacy scalar fields stay authoritative for the visible icon, and
// the shadow reducer's facts and effective generation ride alongside. The
// facts were already bounded by Compact on the shadow path, so only
// live/recent facts reach the payload.
func (s WindowState) MarshalUserVar() (string, error) {
	uv := windowStateUserVar{
		Schema:                     stateUserVarSchemaV2,
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
// The event ring buffer is intentionally left empty. Both wire schemas are
// readable: a v2 payload restores the reducer's source facts (including
// acknowledgement cutoffs and ASL leases) and the effective generation, while
// a v1 payload — which predates the schema discriminator — migrates its
// scalar action and loop marker into synthetic legacy-source facts. The v2
// conversation projection is intentionally re-derived from Facts rather than
// trusted directly. Writing always produces v2 (see MarshalUserVar), so v1
// exists only on the read side.
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
		// The reducer is authoritative for visible state, so a v2 restore with
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

// restoreReducerV2 rebuilds the shadow reducer from a schema-v2 payload. The
// reducer is created with zero thresholds; the restore path re-binds the
// daemon's configured ReducerConfig afterward (restoreFromUserVars), since a
// deserializer has no access to the owning Handler.
func (s *WindowState) restoreReducerV2(uv windowStateUserVar) {
	if len(uv.Facts) > 0 {
		r := NewReducer(ReducerConfig{})
		r.Restore(uv.Facts)
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
