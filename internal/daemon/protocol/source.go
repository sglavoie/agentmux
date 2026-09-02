package protocol

import (
	"sort"
	"strconv"
	"strings"
)

// SourceKind categorizes the origin of a fact. Identity matters for terminal
// suppression: identified sources suppress only their own same-turn trailing
// events, while a legacy source falls back to the guard_duration grace window.
type SourceKind string

const (
	// SourceHarness is the managed harness session (Claude/Codex/Copilot).
	SourceHarness SourceKind = "harness"
	// SourceSubagent is one spawned subagent, keyed by its agent id.
	SourceSubagent SourceKind = "subagent"
	// SourceASL is one agent-session-loop run, keyed by its run id.
	SourceASL SourceKind = "asl"
	// SourceProbe is a daemon-side screen probe (e.g. context-limit banner).
	SourceProbe SourceKind = "probe"
	// SourceLegacy is an unidentified pre-identity client. It carries no turn or
	// source key, so terminal suppression degrades to a guard_duration grace.
	SourceLegacy SourceKind = "legacy"
)

// SourceInvalid is the synthetic source kind stamped onto a quarantined event's
// trace. It is never stored as a live fact; it exists only so audit traces can
// attribute the drop, and so the persistence seam can refuse to restore one. It
// is intentionally outside the five real SourceKinds.
const SourceInvalid SourceKind = "invalid_payload"

// SourceID uniquely identifies a fact producer within one window. Kind plus Key
// is the map key, so two subagents (distinct Keys) are distinct sources while a
// duplicate start for the same agent id collapses onto one fact.
type SourceID struct {
	Kind SourceKind
	Key  string
}

// WindowKey is the per-window fallback source key, used whenever a producer
// carries no session or run identity of its own. Event normalization stamps it
// and the state package rekeys migrated facts onto it, so the construction rule
// lives beside SourceID rather than in either consumer.
func WindowKey(windowID int) string { return "win:" + strconv.Itoa(windowID) }

// SourceIDLess is the total order over source identities: kind first, then key.
// Every ordered source slice the daemon emits — settlement receipts, lifecycle
// evidence — uses it, so the ordering is part of the wire contract rather than
// an implementation detail of any one producer.
func SourceIDLess(a, b SourceID) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Key < b.Key
}

// SortSourceIDs sorts ids in place under SourceIDLess.
func SortSourceIDs(ids []SourceID) {
	sort.Slice(ids, func(i, j int) bool { return SourceIDLess(ids[i], ids[j]) })
}

// UserActionSource is the explicit synthetic source for window-level user
// actions (F8 user_clear). The reducer ignores the source on a user_clear, so
// this only labels the audit trace; it deliberately reuses SourceProbe (the
// daemon-originated, non-harness bucket) with a distinct key.
func UserActionSource() SourceID {
	return SourceID{Kind: SourceProbe, Key: "user"}
}

// ProbeSource is the explicit synthetic source for the hook-path context-limit
// probe, which attributes its daemon-synthesized stop_failure promotions to the
// probe rather than to the harness session.
func ProbeSource() SourceID {
	return SourceID{Kind: SourceProbe, Key: "context_limit"}
}

// legacySubagentBucketPrefix marks a legacy-kind source as an identity-less
// subagent multiset bucket (as opposed to a plain per-window legacy fact),
// so session-end clearing can find the buckets without a separate registry.
const legacySubagentBucketPrefix = "session:"

// LegacySubagentBucketKey builds the legacy multiset bucket key for a session
// (or window fallback) identity.
func LegacySubagentBucketKey(sessionOrWindow string) string {
	return legacySubagentBucketPrefix + sessionOrWindow
}

// IsLegacySubagentBucket reports whether a legacy-kind source key names an
// identity-less subagent multiset bucket.
func IsLegacySubagentBucket(key string) bool {
	return strings.HasPrefix(key, legacySubagentBucketPrefix)
}

// reducerIconPrecedence encodes the target icon precedence from the plan. It is
// intentionally distinct from actionPriority (the legacy guard order): here a
// recoverable warning outranks a subagent, permission and question share a tier,
// and stop sits just above idle. Higher wins; ties break on recency (emitted
// time, then source-independent causal application order).
//
//	error > (key | question) > warning > subagent > compact > bolt > stop > idle
var reducerIconPrecedence = map[string]int{
	"":         0,
	"stop":     1,
	"bolt":     2,
	"compact":  3,
	"subagent": 4,
	"warning":  5,
	"key":      6,
	"question": 6,
	"error":    7,
}

// SourceFactSnapshot is the exported, durable view of one source fact. Field
// names mirror sourceFact; the compact JSON tags are the schema-v4 user-var
// wire form (amx_state must stay small), so this one struct is both the
// snapshot seam and the persisted shape.
type SourceFactSnapshot struct {
	Kind   SourceKind `json:"k"`
	Key    string     `json:"y,omitempty"`
	Action string     `json:"a,omitempty"`
	Icon   string     `json:"i,omitempty"`
	Seq    uint64     `json:"q,omitempty"`
	// CausalOrder is the reducer-wide application generation. Seq remains
	// source-local deduplication metadata; this independent order is what makes
	// equal-timestamp comparisons valid across sources. Older payloads omit it
	// and restore with the deterministic legacy fallback.
	CausalOrder uint64 `json:"o,omitempty"`
	TurnID      string `json:"tn,omitempty"`
	At          int64  `json:"t,omitempty"`
	Acked       bool   `json:"ak,omitempty"`
	AckedAt     int64  `json:"aa,omitempty"`
	// OriginWindowID is the pane that produced a routed fact. It differs from
	// the reducer/window carrying this snapshot when an unmanaged sibling borrows
	// a managed title owner.
	OriginWindowID int    `json:"ow,omitempty"`
	ToolUse        string `json:"tu,omitempty"`

	// Harness process ownership is safe lifecycle metadata, never command-line
	// content. The three fields are deliberately flat and compact because this
	// snapshot is persisted in Kitty's size-sensitive amx_state user var.
	// Partial or source-incompatible triples are discarded during restoration
	// without invalidating the containing fact.
	HarnessProcessHarness    string `json:"ph,omitempty"`
	HarnessProcessPID        int    `json:"pp,omitempty"`
	HarnessProcessStartToken string `json:"pt,omitempty"`

	// PipeMode records whether this harness source is a non-interactive
	// `claude -p` child. Absent (false) restores as interactive, so a payload
	// written before this field decodes with the backward-compatible default;
	// ASL and subagent facts remain independently typed by their source kind.
	PipeMode bool `json:"pm,omitempty"`

	// Subagent aggregation: the parent harness session an identified agent
	// belongs to, and the balanced identity-less agent multiset carried by a
	// legacy subagent bucket.
	SessionID string `json:"sid,omitempty"`
	Agents    int    `json:"ag,omitempty"`

	// ASL lease bookkeeping.
	RunID     string `json:"r,omitempty"`
	Phase     string `json:"p,omitempty"`
	LeaseLive bool   `json:"ll,omitempty"`

	// Terminal suppression bookkeeping.
	Terminated      bool   `json:"tm,omitempty"`
	TerminalTurn    string `json:"tt,omitempty"`
	TerminalToolUse string `json:"ttu,omitempty"`
	TerminalAt      int64  `json:"ta,omitempty"`
}

// Source reconstructs the fact's map key.
func (s SourceFactSnapshot) Source() SourceID { return SourceID{Kind: s.Kind, Key: s.Key} }

// HasMalformedHarnessProcessIdentity reports a partial, source-incompatible,
// or non-canonical persisted process fingerprint. It performs validation over
// the exported snapshot only and never consults the live process table.
func (s SourceFactSnapshot) HasMalformedHarnessProcessIdentity() bool {
	present := s.HarnessProcessHarness != "" || s.HarnessProcessPID != 0 || s.HarnessProcessStartToken != ""
	if !present {
		return false
	}
	identity := FactProcessIdentity(s.Source(), HarnessProcessIdentity{
		Harness:    s.HarnessProcessHarness,
		PID:        s.HarnessProcessPID,
		StartToken: s.HarnessProcessStartToken,
	})
	return identity == (HarnessProcessIdentity{}) ||
		identity.Harness != s.HarnessProcessHarness ||
		identity.PID != s.HarnessProcessPID ||
		identity.StartToken != s.HarnessProcessStartToken
}

func (s *SourceFactSnapshot) ClearHarnessProcessIdentity() {
	s.HarnessProcessHarness = ""
	s.HarnessProcessPID = 0
	s.HarnessProcessStartToken = ""
}

// ReducerEvent is one normalized, source-scoped fact delivered to the reducer.
// Seq is the per-source monotonic sequence used for dedup and reordering: only
// the highest Seq seen for a source takes effect, so replay is order-independent
// and duplicates are no-ops. At is the emitted unix timestamp used for staleness
// and the legacy grace. TurnID scopes identified terminal suppression.
type ReducerEvent struct {
	Source SourceID
	Action string // active|compact|subagent|stop|elicitation|permission|failure|stop_failure|idle|clear|user_clear
	Seq    uint64
	TurnID string
	At     int64
	// OriginWindowID identifies the pane that produced this fact. It can differ
	// from the reducer's managed window when an unmanaged sibling borrows it.
	OriginWindowID int

	// Prompt marks a direct user prompt submission. A prompt is authoritative
	// recovery: it bypasses terminal trailing suppression for its own source and
	// acknowledges stuck terminal facts on every other source.
	Prompt bool
	// ToolUse is the tool-use identity carried by the event, used alongside
	// TurnID to distinguish an identifiable trailing completion of the
	// terminated turn from a genuinely new tool call.
	ToolUse string
	// PipeMode marks a non-interactive `claude -p` hook. It stamps interactive
	// ownership on the fact: an interactive (non-pipe) harness session may
	// retire an older interactive session's stale active fact, while pipe-mode
	// facts are protected from that supersession. Absent (false) is interactive.
	PipeMode bool
	// HarnessProcess is the validated, non-sensitive process fingerprint that
	// produced this event. Ordinary harness, legacy, and subagent sources retain
	// it; the zero value deliberately clears any older ownership on an accepted
	// event. ASL and probe sources never retain process ownership.
	HarnessProcess HarnessProcessIdentity

	// ASL-only. Phase selects the contributed icon and lease; for ASLPhasePaused
	// the attention icon comes from Action.
	RunID string
	Phase string

	// SessionID is the parent harness session the event belongs to. Subagent
	// facts remember it so a session's completion clears only its own agents.
	SessionID string
	// EndsSession marks a session-terminating event: after it applies, the
	// session's remaining subagent contributions (identified facts and its
	// legacy multiset bucket) are ended. With an empty SessionID the clear is
	// window-wide — identity-less clients have no session boundary.
	EndsSession bool
	// SubagentDelta adjusts the source's balanced agent multiset (+1 start,
	// -1 stop) for identity-less subagent lifecycle events. The count never
	// goes negative, and while any agent remains the source contributes 🤖.
	SubagentDelta int
}

// NormalizedEvent is the schema-stable, source-scoped view of one hook event and
// the ONLY input the reducer accepts (via ApplyNormalized). It deliberately
// omits prompt bodies and other free text — only identity, timing, the resolved
// action, and the ASL phase survive. Deliverable events fold into the reducer;
// Invalid events are quarantined and inert events (unknown/ignored) are no-ops.
type NormalizedEvent struct {
	Source SourceID
	Action string // resolved canonical action, "" for inert (unknown/ignored) events
	Seq    uint64 // per-source monotonic sequence parsed from source_sequence
	TurnID string // scopes identified terminal suppression
	At     int64  // emitted unix timestamp for staleness/recency ordering
	// OriginWindowID is the pane that produced the event. It deliberately stays
	// distinct from the reducer window: unmanaged panes can borrow a managed
	// target while their facts retain the producer pane for route continuity.
	OriginWindowID int

	// Prompt marks a direct user prompt submission — authoritative recovery
	// from terminal states. ToolUse carries the event's tool-use identity so a
	// trailing completion of a terminated turn can be told apart from a new
	// tool call.
	Prompt  bool
	ToolUse string

	// PipeMode marks a hook fired by a non-interactive `claude -p` invocation.
	// It stamps interactive ownership onto the reducer fact: an interactive
	// (non-pipe) harness session owns the tab and may retire an older
	// interactive session's stale active fact, while pipe-mode children are
	// protected and never masked by interactive supersession. Absent (false) is
	// interactive, the backward-compatible default.
	PipeMode bool
	// HarnessProcess is populated only by events.NormalizedFromHookRequest
	// after the daemon validates the hook-supplied fingerprint. Its zero value
	// means no process ownership. ASL lifecycle and daemon-synthesized probe
	// facts never receive this identity.
	HarnessProcess HarnessProcessIdentity

	// ASL-only. Phase selects the reducer icon + loop lease; for a paused run the
	// attention icon comes from Action.
	RunID string
	Phase string

	// SessionID is the parent harness session carried by the payload. Subagent
	// facts group by it, so completing one session clears only its own agents.
	SessionID string
	// EndsSession marks a session-terminating event (SessionEnd): when it
	// applies, the reducer ends the session's remaining subagent contributions.
	EndsSession bool
	// SubagentDelta is the balanced-multiset adjustment for identity-less
	// subagent lifecycle events (+1 start, -1 stop) aggregated on a legacy
	// per-session bucket. Identified agents carry 0: their per-agent facts
	// aggregate by source key instead.
	SubagentDelta int

	// Event is the raw hook event name, retained for audit/logging only — never
	// an input to icon logic.
	Event string
	// AgentType is retained for audit/diagnostic attribution only. It never
	// affects the icon.
	AgentType string
	// SourceEventID is the upstream event identity. Notification dedup keys on
	// it (shouldNotifySourceEventLocked) so a redelivered event never notifies
	// twice. It does not affect reduction.
	SourceEventID string

	// Invalid marks a quarantined payload. Reason carries the diagnostic. An
	// invalid event's Action is forced empty so even a mishandled conversion can
	// never produce a terminal state.
	Invalid bool
	Reason  string
}

// Deliverable reports whether the event should fold into the reducer. Invalid
// (quarantined) and inert (unknown/ignored → empty action, no ASL phase) events
// are not deliverable: they must not mutate any source fact.
func (n NormalizedEvent) Deliverable() bool {
	if n.Invalid {
		return false
	}
	return n.Action != "" || n.Phase != ""
}

// ReducerEvent projects the normalized event onto the reducer's fact contract.
// It is only meaningful for deliverable events; callers gate on Deliverable().
func (n NormalizedEvent) ReducerEvent() ReducerEvent {
	return ReducerEvent{
		Source:         n.Source,
		Action:         n.Action,
		Seq:            n.Seq,
		TurnID:         n.TurnID,
		At:             n.At,
		OriginWindowID: n.OriginWindowID,
		Prompt:         n.Prompt,
		ToolUse:        n.ToolUse,
		PipeMode:       n.PipeMode,
		HarnessProcess: n.HarnessProcess,
		RunID:          n.RunID,
		Phase:          n.Phase,
		SessionID:      n.SessionID,
		EndsSession:    n.EndsSession,
		SubagentDelta:  n.SubagentDelta,
	}
}
