package protocol

import (
	"github.com/sglavoie/agentmux/internal/conversation"
)

// AgentTurnSnapshot describes agent work without ASL's contributions or stop
// masking. A nil snapshot means this daemon cannot supply that distinction.
type AgentTurnSnapshot struct {
	IconState  string `json:"icon_state"`
	FinishedAt int64  `json:"finished_at,omitempty"`
}

// WindowStateSnapshot is the daemon-owned state needed by dashboard clients.
// It is deliberately a value copy: callers must never observe or mutate the
// handler's live WindowState entries.
type WindowStateSnapshot struct {
	IconState   string             `json:"icon_state"`
	Looping     bool               `json:"looping"`
	LastAction  string             `json:"last_action"`
	LastEventAt int64              `json:"last_event_at"`
	GuardAction string             `json:"guard_action"`
	AgentTurn   *AgentTurnSnapshot `json:"agent_turn,omitempty"`
	// ASLPhases contains the distinct phases of runs retaining a live lease.
	ASLPhases []string `json:"asl_phases,omitempty"`
	// ASLRunIDs names those runs. A Kitty window also carries its run in a
	// user variable; a session with no Kitty window is tied to its run only
	// here.
	ASLRunIDs []string `json:"asl_run_ids,omitempty"`
	// BusySinceAt is when the window's current active-class stretch began (unix
	// seconds). Absent while the window is not busy, which also preserves the
	// former response shape for clients that do not render the stretch.
	BusySinceAt int64 `json:"busy_since_at,omitempty"`
	// PresentationChangedAtMS is the last dashboard-visible icon or unseen
	// transition in unix milliseconds. Its absence is the backward-compatible
	// zero value for state restored or served by a daemon predating the field.
	PresentationChangedAtMS int64 `json:"presentation_changed_at_ms,omitempty"`
	// Unseen reports that the window came to rest on a turn the user has not
	// looked at yet. The polarity is inverted for the same reason it is inverted
	// on the durable state.WindowState field: absent means SEEN, so a daemon too
	// old to carry the bit, or a client reading a response written before it
	// existed, demotes the row quietly instead of flooding Needs Attention.
	// Absent for every seen window, which also preserves the former response
	// shape for clients that do not group on it.
	Unseen bool `json:"unseen,omitempty"`
	// Reason names WHY a blocked window is blocked: the harness prompt kind a
	// screen probe classified onto state.WindowState.PromptKind, projected here
	// verbatim. It is a bare string rather than an enum for the same reason the
	// durable field is — Claude and Codex kinds share it without a shared type —
	// so the SET of values is the union of screenstate.ClaudePromptKind and
	// screenstate.CodexPromptKind and a client must map it totally.
	//
	// Absent is the ordinary steady state, not an error: a blocked window whose
	// icon came from the hook path, a Copilot window (no screen adapter at all),
	// and a pattern override that dropped a kind clause all reach a client with
	// an empty reason. Every non-blocked window is absent too, because the daemon
	// clears the kind at the publish choke point the moment the icon leaves the
	// blocking class. Omitting it preserves the response shape for clients that
	// do not gloss it, exactly as BusySinceAt and Unseen do.
	Reason string `json:"reason,omitempty"`
	// Branch and Dirty are the advisory Git facts the daemon's per-directory
	// probe (internal/gitstate) last read for the checkout this window is
	// sitting in, projected from state.WindowState.GitBranch/GitDirty. Branch is
	// the short ref name; Dirty reports tracked-file modifications.
	//
	// Absent is an ordinary steady state with several honest causes, none of
	// them an error: the directory is not a repository, the probe is switched
	// off (`git_probe = false` leaves the daemon with no prober at all), git is
	// missing or timed out, HEAD is detached, or no sweep has reached the window
	// yet. internal/gitstate deliberately collapses all of them to an empty
	// branch, and a client must render the absence as "nothing to say" rather
	// than as a failure. Dirty is meaningful on its own: a detached HEAD reports
	// its dirty bit with no branch to hang it on, because the probe reports what
	// it saw rather than gating one fact on the other.
	//
	// Omitting them preserves the response shape for clients that do not paint a
	// branch, exactly as BusySinceAt, Unseen, and Reason do. GitDirectory is not
	// projected: it is write-back bookkeeping that lets the daemon notice a pane
	// moved while git was running, not a display fact.
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
	// Conversation is derived from live parent-harness reducer facts. It is
	// absent for identity-less state, preserving the former response shape for
	// clients that do not use live-conversation continuity.
	Conversation *conversation.Identity `json:"conversation,omitempty"`
}

// WindowDebugMetadata is the daemon's per-window export snapshot. The live
// Kitty-joined section is added later; for now exports can rely on the durable
// fields below even after a session ends or the tab disappears.
type WindowDebugMetadata struct {
	Stats    WindowDebugStats    `json:"stats"`
	Durable  WindowDebugDurable  `json:"durable"`
	Renderer WindowDebugRenderer `json:"renderer"`
	Shadow   WindowDebugShadow   `json:"shadow"`
	Watchdog WindowDebugWatchdog `json:"watchdog"`
}

// WindowDebugWatchdog records the daemon's own synthetic interventions for a
// window: watchdog expiries, ASL reconciliation, and window-close GC. The
// fields are updated atomically (under the same lock) with the per-window
// event ring so the durable export and the live event log can never disagree
// about why the daemon changed state on its own.
type WindowDebugWatchdog struct {
	// LastEvent/LastReason/LastAt describe the most recent synthetic event
	// (WatchdogStale, SourceExpired, ASLReconciled, WindowClosed,
	// HarnessProcessExited).
	LastEvent  string `json:"last_event,omitempty"`
	LastReason string `json:"last_reason,omitempty"`
	LastAt     int64  `json:"last_at,omitempty"`
	// SourceExpiries counts individual source-fact expiries; StaleSweeps
	// counts whole-window stale clears; Reconciles counts ASL lease
	// seeds/releases performed by reconciliation.
	SourceExpiries int `json:"source_expiries,omitempty"`
	StaleSweeps    int `json:"stale_sweeps,omitempty"`
	Reconciles     int `json:"reconciles,omitempty"`
	// ProcessExits counts committed exact-fingerprint settlements. The last
	// process fields remain separate from LastEvent/LastReason so a later
	// unrelated watchdog intervention cannot erase the lifecycle diagnosis.
	ProcessExits               int    `json:"process_exits,omitempty"`
	LastProcessExitReason      string `json:"last_process_exit_reason,omitempty"`
	LastProcessExitObservation string `json:"last_process_exit_observation,omitempty"`
	LastProcessExitAt          int64  `json:"last_process_exit_at,omitempty"`
	// ClosedAt is non-zero while the window is a tombstone: its live state was
	// garbage-collected and this metadata is retained (bounded) for
	// post-mortem export only. Tombstones never count as renderer drift.
	ClosedAt int64 `json:"closed_at,omitempty"`
	// ScreenDeclines publishes the screen classifier's live decline counters for
	// this window, one entry per probe source that currently holds a streak.
	// Empty (and so omitted) for every window whose probe either settled or was
	// never eligible, which is the ordinary case.
	ScreenDeclines []ScreenDeclineSnapshot `json:"screen_declines,omitempty"`
}

// ScreenDeclineSnapshot is one screen-probe source's current decline streak
// plus the evidence from the frame that last extended it. It is the exported
// half of the daemon's answer to the question the classifier cannot answer for
// itself: a harness that redesigns its TUI does not report the change, it just
// stops matching, and the tab holds ⚡ forever. A long streak carried by a
// STABLE fingerprint is that failure's signature — the screen went quiet and
// Layer A vetoed it anyway — so Streak and StableFingerprint are only ever
// meaningful together.
type ScreenDeclineSnapshot struct {
	// Source is the SourceProbe key the streak is keyed by (one per harness
	// probe), so a window that somehow carries both never reports a merged
	// count.
	Source string `json:"source"`
	// Streak counts consecutive declines of the SAME screen; it restarts at one
	// whenever the fingerprint changes.
	Streak int `json:"streak"`
	// StableFingerprint reports whether the streak has now refused the same
	// unchanging screen at least twice. A repainting pane is healthy live work
	// and never reaches it.
	StableFingerprint bool `json:"stable_fingerprint,omitempty"`
	// Fingerprint is the normalized-screen hash the streak accumulates against.
	Fingerprint int64 `json:"fingerprint,omitempty"`
	// Vetoes is screenstate.Veto.String() for the last declined frame: the named
	// reasons Layer A refused, not a bitset.
	Vetoes string `json:"vetoes,omitempty"`
	// StatusLine is the harness status line the last declined frame carried. It
	// is what an operator diffs against the shipped patterns to see which copy
	// changed.
	StatusLine string `json:"status_line,omitempty"`
	// At is when the last decline was credited (unix seconds).
	At int64 `json:"at,omitempty"`
}

// WindowDebugShadow reports the shadow reducer's view of a window: its current
// effective state/generation and every recorded divergence from the legacy
// state machine. The reducer's effective state is the authoritative visible
// icon and loop state (published by reduceStateUpdate through
// publishEffectiveLocked); the recorded mismatches are diagnostic evidence of
// where the retired legacy scalar machine would have differed, never a separate
// visible-state writer or a behavior change.
type WindowDebugShadow struct {
	EffectiveIcon    string           `json:"effective_icon"`
	EffectiveLooping bool             `json:"effective_looping"`
	EffectiveGen     uint64           `json:"effective_gen"`
	MismatchCount    int              `json:"mismatch_count"`
	Mismatches       []ShadowMismatch `json:"mismatches,omitempty"`
	// Facts is the reducer's live source-fact set, stamped at export time from
	// the window's reducer. Diagnostics use it to hold the visible
	// icon accountable to the authoritative facts: an active ASL lease showing
	// stop/empty, or a live subagent without 🤖, is a health failure.
	Facts []SourceFactSnapshot `json:"facts,omitempty"`
	// PendingTerminalStop is present only while the handler is holding a
	// terminal stop for this window. The hold lives in daemon memory, so a
	// restart drops it and this section disappears.
	PendingTerminalStop *PendingTerminalStop `json:"pending_terminal_stop,omitempty"`
	// BusySinceAt is when the window's current active-class stretch began (unix
	// seconds). Absent while the window is not busy, which also preserves the
	// former response shape for clients that do not render the stretch.
	BusySinceAt int64 `json:"busy_since_at,omitempty"`
}

// Stop-hold class names are the stable spelling of a hold's release rule in
// exported diagnostics.
const (
	StopHoldClassReset    = "reset"
	StopHoldClassTerminal = "terminal"
)

// PendingTerminalStop is the diagnostic view of one terminal stop the handler
// is withholding from the reducer. ArmedAt, QuietUntil, and Deadline are unix
// seconds; Confirmed reports whether the harness's idle probe has already
// settled the quiet period.
type PendingTerminalStop struct {
	ArmedAt    int64  `json:"armed_at"`
	QuietUntil int64  `json:"quiet_until"`
	Deadline   int64  `json:"deadline"`
	Confirmed  bool   `json:"confirmed"`
	Class      string `json:"class"`
}

// ShadowMismatch is one recorded divergence between the legacy render decision
// and the shadow reducer's effective state, captured after both saw the same
// event. The ring is bounded (maxShadowMismatches); MismatchCount stays
// cumulative.
type ShadowMismatch struct {
	At            int64  `json:"at"`
	Event         string `json:"event,omitempty"`
	Action        string `json:"action,omitempty"`
	LegacyIcon    string `json:"legacy_icon"`
	ShadowIcon    string `json:"shadow_icon"`
	LegacyLooping bool   `json:"legacy_looping"`
	ShadowLooping bool   `json:"shadow_looping"`
	SourceKind    string `json:"source_kind,omitempty"`
	SourceKey     string `json:"source_key,omitempty"`
}

// WindowDebugRenderer is the daemon-owned rendering state for a window. It
// makes drift and repeated Kitty write failures visible without asking callers
// to infer desired state from the mutable tab title.
type WindowDebugRenderer struct {
	DesiredIcon     string `json:"desired_icon"`
	StateSet        bool   `json:"state_set"`
	Looping         bool   `json:"looping"`
	LoopingSet      bool   `json:"looping_set"`
	RenderFailures  int    `json:"render_failures"`
	LastRenderError string `json:"last_render_error"`
	KittyListenOn   string `json:"kitty_listen_on,omitempty"`

	// Render-generation pipeline diagnostics. The window's Seq counter is its
	// generation; each stage records the highest generation it has reached:
	// queued (handed to the render queue), attempted (candidate built),
	// persisted (amx_state landed), rendered (title landed or was already
	// converged), failed (a Kitty write for that generation errored).
	QueuedGen    uint64 `json:"queued_gen,omitempty"`
	AttemptedGen uint64 `json:"attempted_gen,omitempty"`
	PersistedGen uint64 `json:"persisted_gen,omitempty"`
	RenderedGen  uint64 `json:"rendered_gen,omitempty"`
	FailedGen    uint64 `json:"failed_gen,omitempty"`
	// DirtySince is when the current dirty stretch began (unix seconds, 0 when
	// clean); DirtyAgeSeconds is computed at export time from the live state.
	// LastRenderLatencyMs is mark-dirty → fully-rendered for the most recent
	// successful pass.
	DirtySince          int64 `json:"dirty_since,omitempty"`
	DirtyAgeSeconds     int64 `json:"dirty_age_seconds,omitempty"`
	LastRenderLatencyMs int64 `json:"last_render_latency_ms,omitempty"`

	// Generations is the bounded schema-v9 causal history for asynchronous
	// rendering. A latest-generation pass may contain several source generations
	// and request IDs when queue coalescing folded a burst into one Kitty write.
	Generations []RenderGenerationTrace `json:"generations,omitempty"`
}

// MaxRenderCausalIDs bounds the number of request/trace identities retained by
// one coalesced render generation. The renderer never waits on an unbounded
// amount of diagnostic work, even under a sustained hook burst.
const MaxRenderCausalIDs = 32

// RenderGenerationTrace describes one queued render generation and the bounded
// many-to-one causal set that produced it. SourceGenerations retains the
// generations folded into Generation; the ID slices are de-duplicated and
// capped at MaxRenderCausalIDs.
type RenderGenerationTrace struct {
	Generation        uint64   `json:"generation"`
	SourceGenerations []uint64 `json:"source_generations,omitempty"`
	TraceIDs          []string `json:"trace_ids,omitempty"`
	RequestIDs        []string `json:"request_ids,omitempty"`
	SourceEventIDs    []string `json:"source_event_ids,omitempty"`
	Stage             string   `json:"stage"`
	Outcome           string   `json:"outcome,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	UpdatedAt         int64    `json:"updated_at,omitempty"`
}
