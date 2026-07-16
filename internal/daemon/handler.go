package daemon

import (
	"os"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// kittyTitleClient is the narrow subset of kitty.KittyClient that the daemon
// sweeps need. Declaring it locally lets tests provide a focused fake instead
// of all methods on kitty.KittyClient. kitty.SubprocessClient satisfies this
// interface implicitly.
type kittyTitleClient interface {
	ListWindows() ([]kitty.OSWindow, error)
	GetText(matchExpr, extent string) (string, error)
	SetTabTitle(matchExpr, title string) error
	SetUserVars(matchExpr string, vars map[string]string) error
}

// Handler processes daemon requests.
type Handler struct {
	guard    *StateGuard
	notifier *Notifier
	// decisionJournal receives complete sanitized core decision records after
	// the in-memory SessionEvent has been finalized. Nil keeps diagnostics
	// disabled and every write remains fail-open.
	decisionJournal *diagnostics.Journal
	// captureStore is consulted only for explicitly authorized temporary raw
	// captures. Nil and every write error are fail-open for hook delivery.
	captureStore             *diagnostics.CaptureStore
	captureMaintenanceMu     sync.Mutex
	lastCaptureMaintenanceAt time.Time
	asyncMu                  sync.Mutex
	notificationEffects      map[string]NotificationDisposition
	notificationEffectOrder  []string

	// startedAt records the daemon start time so health queries can report
	// uptime. Zero value means uptime should be reported as 0.
	startedAt time.Time

	// buildInfo identifies the artifact this daemon is running. Stamped once
	// by daemon.Run at startup (before the on-disk binary can be replaced)
	// and reported from TypeHealth so clients can detect a CLI/daemon build
	// mismatch. Nil in unit tests that never set it.
	buildInfo *buildinfo.Info

	// metricsFn returns the owning server's request-outcome counters (queue
	// saturation, queue latency) for TypeHealth and export surfaces. Installed
	// by NewServer; nil when the handler runs without a server.
	metricsFn func() MetricsSnapshot

	// eventSeq is the daemon's global monotonic receipt sequence, stamped onto
	// every event appended to a per-window ring so exports can totally order
	// receipts across windows. Guarded by stateMu; restarts from zero with the
	// daemon (receipt order is a process-lifetime notion).
	eventSeq uint64

	// kittyClient is used by the stale sweep to clear stranded tab-title icons.
	// Injected by daemon.Run; nil in unit tests that don't exercise the
	// title-clear path.
	kittyClient kittyTitleClient

	// renderQueue is the latest-generation per-window render queue consumed by
	// the render worker (renderqueue.go), the daemon's single Kitty writer.
	renderQueue *renderQueue

	// staleThresholds holds per-action stale-detection durations, keyed by
	// the action name ("active", "subagent", "compact"). A missing or zero
	// value for an action disables the sweep for that action. The sweep is
	// disabled entirely when every value is <= 0.
	staleThresholds map[string]time.Duration

	// perAccountThresholds overrides staleThresholds for specific accounts.
	// The outer map is keyed by lowercased account name; the inner map is
	// keyed by action name. A zero or missing inner entry falls back to the
	// matching staleThresholds entry.
	perAccountThresholds map[string]map[string]time.Duration

	// hookSilenceThreshold is the global account-level threshold for the
	// hook-silence sweep: when an account's most recent event is older than
	// this duration, the sweep fires a one-shot notification. A zero value
	// disables the sweep entirely (per-account overrides aside).
	hookSilenceThreshold time.Duration

	// perAccountHookSilence overrides hookSilenceThreshold for specific
	// accounts. Keyed by lowercased account name. A zero or missing entry
	// falls back to hookSilenceThreshold.
	perAccountHookSilence map[string]time.Duration

	// stateMu protects store, the account-level hook-silence maps, and
	// windowMetadata. Per-window state lives under store so handlers mutate a
	// single WindowState entry instead of a set of parallel maps.
	stateMu sync.Mutex
	store   map[int]*WindowState

	// resetStopCommitMu linearizes a claimed deferred reset-sensitive Stop's
	// final state application with events that supersede it. Callers acquire it
	// before stateMu; the callback does not hold either lock while invoking the
	// test seam below.
	resetStopCommitMu             sync.Mutex
	pendingResetStops             map[int]pendingResetStop
	nextResetStopSeq              uint64
	afterDeferredStopClaimForTest func(windowID int, seq uint64)

	kittySockets      map[int]string
	lastKittyListenOn string

	// snapshots holds the immutable Kitty snapshot cache. The render/restore
	// loop is the single lister and publishes into it; hook routing reads it
	// without any Kitty I/O so a slow or wedged ListWindows can never delay a
	// hook acknowledgement. Its own RWMutex keeps snapshot reads off stateMu.
	snapshots snapshotStore

	// readiness tracks whether the daemon completed its bounded initial
	// snapshot+restore before binding the public socket, and, if not, why it
	// started degraded. Exposed via TypeHealth. Guarded by its own mutex.
	readiness readinessState

	// probeRunner runs an asynchronous context-limit screen probe off the hook
	// goroutine. Nil means spawn a goroutine (production); tests inject a
	// synchronous runner for deterministic assertions.
	probeRunner func(func())

	// activeLoopCache memoizes agent-session-loop discovery for the ASL
	// sibling-pane routing exception (handler_asl_routing.go). Discovery scans
	// on-disk loop state, so a hook burst from the same pane must not rescan on
	// every event. Entries are valid for at most renderInterval and are
	// invalidated whenever a loop lifecycle event (started, turn started,
	// finished) changes the active set. The cache is rebuilt lazily, including
	// after a daemon restart, and is never persisted in amx_state. Guarded by
	// stateMu.
	activeLoopCache      []agentloop.Candidate
	activeLoopCacheAt    time.Time
	activeLoopCacheValid bool
	// activeLoopCacheGen counts lifecycle invalidations. A scan captures the
	// generation before releasing stateMu to discover, and only publishes its
	// result if the generation still matches on re-lock. This prevents a slow
	// scan that started before invalidateActiveLoopCache from overwriting the
	// newer (empty) state with a stale candidate set.
	activeLoopCacheGen uint64

	// aslMissStreak counts, per window lease, how many consecutive successful
	// discoveries missed the lease's run. Reconciliation releases the lease at
	// aslMissingRunThreshold; a discovery failure leaves the counters (and the
	// leases) untouched. lastASLReconcileAt gates the periodic reconciliation
	// cadence. Both guarded by stateMu.
	aslMissStreak      map[aslLeaseRef]int
	lastASLReconcileAt time.Time

	// idleSampleStreak counts, per window probe source, how many consecutive
	// stabilized idle screen samples an adapter has observed before an
	// AuthoritativeIdle may settle stale interactive work. Screen adapters that
	// require stabilized evidence (Codex/Claude reads) accumulate here; the
	// count is cleared after activity, a classifier failure, a protected-state
	// change, or window closure. Guarded by stateMu.
	idleSampleStreak map[idleSampleRef]int

	// idleSampleGen records, per window probe source, the evidence generation
	// (e.g. a Copilot transcript's byte size) the current stabilization streak is
	// accumulating against. A sample observing a different generation restarts the
	// streak, so consecutive agreeing samples must describe the SAME evidence
	// state — a probe source whose evidence advances on every new record cannot
	// converge across two distinct intermediate terminals. Guarded by stateMu.
	idleSampleGen map[idleSampleRef]int64

	// reducerCfg holds the deterministic thresholds handed to each window's
	// shadow reducer. Set once at startup from [defaults.watchdog] and
	// guard_duration (SetReducerConfig); the zero value disables the reducer's
	// staleness self-heals and legacy grace, which is what unit tests want.
	// Guarded by stateMu (per-window reducers are only touched under it).
	reducerCfg ReducerConfig

	// claudeContextLimitProbeEnabled gates ProbeClaudeContextLimit. Set from
	// [defaults.watchdog].claude_context_limit_probe at daemon startup.
	// Defaults to true; an explicit `false` skips the probe entirely.
	claudeContextLimitProbeEnabled bool

	// copilotTranscriptHomes and copilotDefaultHome resolve a managed Copilot
	// tab's interactive session transcript for the transcript stop probe.
	// copilotTranscriptHomes is the ordered set of configured Copilot profile
	// home directories (already ~-expanded); copilotDefaultHome is the default
	// Copilot home used when no configured profile holds the transcript. Both
	// are set once at startup (SetCopilotTranscriptHomes) before the probe loop
	// starts and are never mutated afterward, so the probe reads them without a
	// lock — matching claudeContextLimitProbeEnabled.
	copilotTranscriptHomes []string
	copilotDefaultHome     string

	// copilotLifecycleReader reads a resolved Copilot transcript's terminal
	// evidence for the transcript stop probe. Defaults to
	// transcript.ReadCopilotLifecycle (NewHandler); tests inject a reader to
	// model a slow read racing a concurrent prompt, or to drive specific
	// evidence, without threading fixture timing through the resolver.
	copilotLifecycleReader func(path string) (transcript.CopilotLifecycleEvidence, error)

	// lastSeenEventAt records the most recent event timestamp per account
	// (any action, any window). The hook-silence sweep compares this against
	// the configured threshold to detect accounts whose hooks have stopped
	// reaching the daemon entirely. Keys are lowercased account names so that
	// hook payloads and cc_account user vars with differing case still match.
	lastSeenEventAt map[string]time.Time

	// notifiedHookSilence is a one-shot latch per account: once the
	// hook-silence sweep fires for an account, this prevents re-notification
	// until a fresh event for that account re-arms the entry by deleting it
	// in handleStateUpdate. Keys are lowercased account names — see
	// lastSeenEventAt for rationale.
	notifiedHookSilence map[string]bool

	// hookSilenceIdle accumulates in-process observed idle time per account
	// (lowercased), anchored to that account's lastSeenEventAt value. The
	// hook-silence sweep expires on the accumulated duration rather than a
	// wall-clock difference so a system sleep cannot fire a false silence
	// notification on wake. Guarded by stateMu.
	hookSilenceIdle map[string]*accountIdle

	// windowMetadata stores the durable per-window export snapshot. Terminal
	// cleanup intentionally does not clear this so post-mortem exports still
	// have identity/context even after the original Kitty tab is gone;
	// closed-window GC bounds it via windowTombstones below.
	windowMetadata map[int]WindowDebugMetadata

	// windowTombstones records when each closed window was garbage-collected.
	// The tombstone is the retention lease for the window's durable metadata:
	// entries expire after windowTombstoneRetention or once
	// maxWindowTombstones is exceeded (oldest first, tracked by
	// tombstoneOrder), and eviction deletes the windowMetadata entry too. A
	// tombstoned window never counts as renderer drift. Guarded by stateMu.
	windowTombstones map[int]time.Time
	tombstoneOrder   []int

	// onShutdown is called when a TypeShutdown request is received.
	onShutdown func()

	// shutdownOnce gates onShutdown so back-to-back shutdown requests cannot
	// race the daemon's cancel func. context.CancelFunc is already idempotent,
	// so this is a defence-in-depth guard rather than a correctness fix.
	shutdownOnce sync.Once
}

// maxEventsPerWindow caps the per-window ring buffer used by handleSessionDetail.
// At ~50 windows × ~200 B/event, the upper bound stays around ~5 MB worst case.
const maxEventsPerWindow = 5000

// NewHandler creates a Handler with the given guard and notifier. The
// reducer's trailing-event grace defaults to the guard duration so legacy
// guard semantics and reducer suppression agree; daemon.Run overrides the
// full threshold set via SetReducerConfig.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	var cfg ReducerConfig
	if guard != nil {
		cfg.GuardDuration = int64(guard.duration / time.Second)
	}
	h := &Handler{
		guard:                          guard,
		notifier:                       notifier,
		reducerCfg:                     cfg,
		startedAt:                      time.Now(),
		staleThresholds:                make(map[string]time.Duration),
		perAccountThresholds:           make(map[string]map[string]time.Duration),
		perAccountHookSilence:          make(map[string]time.Duration),
		store:                          make(map[int]*WindowState),
		pendingResetStops:              make(map[int]pendingResetStop),
		renderQueue:                    newRenderQueue(),
		kittySockets:                   make(map[int]string),
		claudeContextLimitProbeEnabled: true,
		copilotLifecycleReader:         transcript.ReadCopilotLifecycle,
		aslMissStreak:                  make(map[aslLeaseRef]int),
		idleSampleStreak:               make(map[idleSampleRef]int),
		idleSampleGen:                  make(map[idleSampleRef]int64),
		lastSeenEventAt:                make(map[string]time.Time),
		notifiedHookSilence:            make(map[string]bool),
		hookSilenceIdle:                make(map[string]*accountIdle),
		windowMetadata:                 make(map[int]WindowDebugMetadata),
		windowTombstones:               make(map[int]time.Time),
		notificationEffects:            make(map[string]NotificationDisposition),
	}
	if notifier != nil {
		notifier.SetObserver(h.recordNotificationDispositionBounded)
	}
	return h
}

// Handle processes a Request and returns a Response.
func (h *Handler) Handle(req Request) Response {
	req = normalizeProducerFields(req)
	req = ensureRequestTraceID(req)
	var resp Response
	switch req.Type {
	case TypeHookEvent:
		h.captureHookPayload(req)
		resp = h.handleHookEvent(req)
		resp = h.attachHookDeliveryAcknowledgement(req, resp)
	case TypeDiagnosticProbe:
		resp = h.handleDiagnosticProbe(req)
	case TypeStateUpdate:
		resp = h.handleStateUpdate(req)
	case TypeStateQuery:
		resp = h.handleStateQuery()
	case TypeSessionDetail:
		resp = h.handleSessionDetail(req)
	case TypeEventsExport:
		resp = h.handleEventsExport(req)
	case TypeHealth:
		resp = h.handleHealth()
	case TypeShutdown:
		resp = h.handleShutdown()
	default:
		resp = Response{OK: false, Error: "unknown request type: " + string(req.Type)}
	}
	return resp
}

func (h *Handler) handleHealth() Response {
	var uptime int64
	if !h.startedAt.IsZero() {
		uptime = int64(time.Since(h.startedAt).Seconds())
	}
	ready, degraded := h.Readiness()
	resp := Response{OK: true, PID: os.Getpid(), UptimeSeconds: uptime, Ready: ready, DegradedReason: degraded}
	if h.buildInfo != nil {
		build := *h.buildInfo
		resp.Build = &build
	}
	if h.metricsFn != nil {
		metrics := h.metricsFn()
		resp.Metrics = &metrics
	}
	return resp
}

// SetBuildInfo stamps the daemon's artifact identity for TypeHealth. Called
// once at startup, before any request is served.
func (h *Handler) SetBuildInfo(info buildinfo.Info) {
	h.buildInfo = &info
}

// setMetricsSource installs the server's metrics snapshot function so health
// responses carry request-outcome counters. Called by NewServer before the
// socket accepts connections.
func (h *Handler) setMetricsSource(fn func() MetricsSnapshot) {
	h.metricsFn = fn
}

func (h *Handler) handleStateQuery() Response {
	h.stateMu.Lock()
	now := time.Now().Unix()
	states := make(map[int]WindowStateSnapshot, len(h.store))
	snapshot := make(map[int]int64, len(h.store))
	for id, st := range h.store {
		state := WindowStateSnapshot{
			IconState:   desiredIconForState(st),
			Looping:     st.Looping,
			LastAction:  st.LastAction,
			GuardAction: st.GuardAction,
		}
		if identity, ok := st.CurrentConversation(now); ok {
			identityCopy := identity
			state.Conversation = &identityCopy
		}
		if st.Activity.IsZero() {
			states[id] = state
			continue
		}
		snapshot[id] = st.Activity.Unix()
		state.LastEventAt = st.Activity.Unix()
		states[id] = state
	}
	watchdog := make(map[int]WatchdogState, len(h.store))
	for id, st := range h.store {
		action := st.LastAction
		if action != "active" && action != "subagent" && action != "compact" {
			continue
		}
		entry := WatchdogState{LastAction: action}
		if !st.Activity.IsZero() {
			entry.LastEventAt = st.Activity.Unix()
		}
		if d := h.resolveThreshold(st.LastAccount, action); d > 0 {
			entry.ThresholdSec = int(d / time.Second)
		}
		watchdog[id] = entry
	}
	h.stateMu.Unlock()
	return Response{OK: true, Activity: snapshot, Watchdog: watchdog, States: states}
}

// handleSessionDetail returns the per-window event ring buffer for req.WindowID.
// An unknown window yields a non-nil empty slice rather than nil so the TUI can
// distinguish "no daemon error" from "no events yet" without a nil check.
func (h *Handler) handleSessionDetail(req Request) Response {
	h.stateMu.Lock()
	var src []SessionEvent
	if st, ok := h.windowStateLocked(req.WindowID); ok {
		src = st.EventLog
	}
	events := make([]SessionEvent, len(src))
	for i := range src {
		events[i] = h.enrichNotificationEvent(req.WindowID, cloneSessionEvent(src[i]))
	}
	h.stateMu.Unlock()
	return Response{OK: true, Events: events}
}

// handleEventsExport returns a deep copy of the requested window's event ring
// buffer plus the durable per-window export snapshot. When req.WindowID is
// zero, every exported window is returned. Mirrors handleSessionDetail's
// locking and defensive copies so callers cannot mutate the daemon's internal
// state.
func (h *Handler) handleEventsExport(req Request) Response {
	h.stateMu.Lock()
	out := make(map[int][]SessionEvent)
	meta := make(map[int]WindowDebugMetadata)
	if req.WindowID != 0 {
		if st, ok := h.windowStateLocked(req.WindowID); ok && len(st.EventLog) > 0 {
			cp := make([]SessionEvent, len(st.EventLog))
			for i := range st.EventLog {
				cp[i] = h.enrichNotificationEvent(req.WindowID, cloneSessionEvent(st.EventLog[i]))
			}
			out[req.WindowID] = cp
		}
		if snapshot, ok := h.windowMetadata[req.WindowID]; ok {
			meta[req.WindowID] = h.exportEnrichLocked(req.WindowID, cloneWindowDebugMetadata(snapshot))
		}
	} else {
		out = make(map[int][]SessionEvent, len(h.store))
		for id, st := range h.store {
			if len(st.EventLog) == 0 {
				continue
			}
			cp := make([]SessionEvent, len(st.EventLog))
			for i := range st.EventLog {
				cp[i] = h.enrichNotificationEvent(id, cloneSessionEvent(st.EventLog[i]))
			}
			out[id] = cp
		}
		meta = make(map[int]WindowDebugMetadata, len(h.windowMetadata))
		for id, snapshot := range h.windowMetadata {
			meta[id] = h.exportEnrichLocked(id, cloneWindowDebugMetadata(snapshot))
		}
	}
	h.stateMu.Unlock()
	return Response{OK: true, EventsByWindow: out, WindowMetadata: meta}
}

// exportEnrichLocked stamps live-only state onto an export snapshot: the
// renderer dirty age (DirtySince in the metadata is copied at mutation time;
// the age is computed at export time so a long-stuck window reads truthfully)
// and the reducer's current source facts, so diagnostics can hold the visible
// icon accountable to the authoritative fact set. Callers hold stateMu.
func (h *Handler) exportEnrichLocked(windowID int, meta WindowDebugMetadata) WindowDebugMetadata {
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
	return meta
}

func cloneWindowDebugMetadata(in WindowDebugMetadata) WindowDebugMetadata {
	out := in
	if in.Stats.SuppressionReasons != nil {
		out.Stats.SuppressionReasons = make(map[string]int, len(in.Stats.SuppressionReasons))
		for reason, count := range in.Stats.SuppressionReasons {
			out.Stats.SuppressionReasons[reason] = count
		}
	}
	if in.Shadow.Mismatches != nil {
		out.Shadow.Mismatches = make([]ShadowMismatch, len(in.Shadow.Mismatches))
		copy(out.Shadow.Mismatches, in.Shadow.Mismatches)
	}
	out.Renderer.Generations = cloneRenderGenerationTraces(in.Renderer.Generations)
	return out
}

// SetReducerConfig installs the shadow reducer thresholds. Existing per-window
// reducers are re-bound too, so a restore that ran before configuration (unit
// tests, degraded startups) still ends up on the configured thresholds.
func (h *Handler) SetReducerConfig(cfg ReducerConfig) {
	h.stateMu.Lock()
	h.reducerCfg = cfg
	for _, st := range h.store {
		if st.Reducer != nil {
			st.Reducer.SetConfig(cfg)
		}
	}
	h.stateMu.Unlock()
}

// IsMuted reports whether the given window is currently marked as muted. The
// daemon refreshes this cache from Kitty snapshots; sweep paths that lack a
// request consult it via the Notifier closure installed in daemon.Run.
func (h *Handler) IsMuted(windowID int) bool {
	h.stateMu.Lock()
	muted := false
	if st, ok := h.windowStateLocked(windowID); ok {
		muted = st.Muted
	}
	h.stateMu.Unlock()
	return muted
}

func (h *Handler) cancelPendingNotification(windowID int) {
	if h.notifier == nil {
		return
	}
	h.notifier.CancelPending(windowID)
}

func (h *Handler) maybeNotify(windowID int, sessionTitle, account, action string, pipeMode bool) {
	if h.notifier == nil {
		return
	}
	h.notifier.MaybeNotify(windowID, sessionTitle, account, action, pipeMode)
}

func (h *Handler) maybeNotifyTraced(windowID int, sessionTitle, account, action string, pipeMode bool, trace NotificationTrace) {
	if h.notifier == nil {
		return
	}
	h.notifier.MaybeNotifyTraced(windowID, sessionTitle, account, action, pipeMode, trace)
}

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		h.shutdownOnce.Do(func() { go h.onShutdown() })
	}
	return Response{OK: true}
}
