package daemon

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
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
