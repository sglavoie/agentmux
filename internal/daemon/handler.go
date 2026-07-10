package daemon

import (
	"os"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
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

	// startedAt records the daemon start time so health queries can report
	// uptime. Zero value means uptime should be reported as 0.
	startedAt time.Time

	// kittyClient is used by the stale sweep to clear stranded tab-title icons.
	// Injected by daemon.Run; nil in unit tests that don't exercise the
	// title-clear path.
	kittyClient kittyTitleClient

	renderCh chan struct{}

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
	stateMu           sync.Mutex
	store             map[int]*WindowState
	pendingResetStops map[int]pendingResetStop
	nextResetStopSeq  uint64

	kittySockets      map[int]string
	lastKittyListenOn string
	lastRenderTickAt  time.Time

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

	// claudeContextLimitProbeEnabled gates ProbeClaudeContextLimit. Set from
	// [defaults.watchdog].claude_context_limit_probe at daemon startup.
	// Defaults to true; an explicit `false` skips the probe entirely.
	claudeContextLimitProbeEnabled bool

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

	// windowMetadata stores the durable per-window export snapshot. Unlike the
	// live watchdog maps, terminal cleanup and closed-window GC intentionally do
	// not clear this so post-mortem exports still have identity/context even
	// after the original Kitty tab is gone.
	windowMetadata map[int]WindowDebugMetadata

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

// NewHandler creates a Handler with the given guard and notifier.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	return &Handler{
		guard:                          guard,
		notifier:                       notifier,
		startedAt:                      time.Now(),
		staleThresholds:                make(map[string]time.Duration),
		perAccountThresholds:           make(map[string]map[string]time.Duration),
		perAccountHookSilence:          make(map[string]time.Duration),
		store:                          make(map[int]*WindowState),
		pendingResetStops:              make(map[int]pendingResetStop),
		renderCh:                       make(chan struct{}, 1),
		kittySockets:                   make(map[int]string),
		claudeContextLimitProbeEnabled: true,
		lastSeenEventAt:                make(map[string]time.Time),
		notifiedHookSilence:            make(map[string]bool),
		windowMetadata:                 make(map[int]WindowDebugMetadata),
	}
}

// Handle processes a Request and returns a Response.
func (h *Handler) Handle(req Request) Response {
	var resp Response
	switch req.Type {
	case TypeHookEvent:
		resp = h.handleHookEvent(req)
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
	return Response{OK: true, PID: os.Getpid(), UptimeSeconds: uptime}
}

func (h *Handler) handleStateQuery() Response {
	h.stateMu.Lock()
	states := make(map[int]WindowStateSnapshot, len(h.store))
	snapshot := make(map[int]int64, len(h.store))
	for id, st := range h.store {
		state := WindowStateSnapshot{
			IconState:   desiredIconForState(st),
			Looping:     st.Looping,
			LastAction:  st.LastAction,
			GuardAction: st.GuardAction,
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
	copy(events, src)
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
			copy(cp, st.EventLog)
			out[req.WindowID] = cp
		}
		if snapshot, ok := h.windowMetadata[req.WindowID]; ok {
			meta[req.WindowID] = cloneWindowDebugMetadata(snapshot)
		}
	} else {
		out = make(map[int][]SessionEvent, len(h.store))
		for id, st := range h.store {
			if len(st.EventLog) == 0 {
				continue
			}
			cp := make([]SessionEvent, len(st.EventLog))
			copy(cp, st.EventLog)
			out[id] = cp
		}
		meta = make(map[int]WindowDebugMetadata, len(h.windowMetadata))
		for id, snapshot := range h.windowMetadata {
			meta[id] = cloneWindowDebugMetadata(snapshot)
		}
	}
	h.stateMu.Unlock()
	return Response{OK: true, EventsByWindow: out, WindowMetadata: meta}
}

func cloneWindowDebugMetadata(in WindowDebugMetadata) WindowDebugMetadata {
	out := in
	if in.Stats.SuppressionReasons == nil {
		return out
	}
	out.Stats.SuppressionReasons = make(map[string]int, len(in.Stats.SuppressionReasons))
	for reason, count := range in.Stats.SuppressionReasons {
		out.Stats.SuppressionReasons[reason] = count
	}
	return out
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

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		h.shutdownOnce.Do(func() { go h.onShutdown() })
	}
	return Response{OK: true}
}
