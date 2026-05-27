package daemon

import (
	"os"
	"sync"
	"time"

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

	// stateMu protects activity, lastAction, and lastAccount.
	stateMu  sync.Mutex
	activity map[int]time.Time

	// lastAction records the most recent non-idle/clear action per window.
	// It outlives the state guard so that an idle event arriving after the
	// guard expires can still detect a stale bolt and clear it.
	lastAction map[int]string

	// lastAccount records the most recent profile/account name per window so
	// the sweep can resolve per-account threshold overrides without keeping a
	// reference to the original Request. Lowercase-keyed lookups happen at
	// read time, not write time.
	lastAccount map[int]string

	// lastActiveEvent records the hook event name that last set the "active"
	// state, so isAutoApprovedPerm can distinguish PreToolUse (same-tool
	// auto-approval) from PostToolUse (prior tool completed).
	lastActiveEvent map[int]string

	// copilotToolPermissionState tracks Copilot's tool-permission lifecycle per
	// window. permissionRequest fires before Copilot decides whether a tool call
	// is auto-allowed or needs a real prompt, so the daemon keeps a short-lived
	// state machine:
	//   - pending: permissionRequest fired; wait for either a real prompt or
	//     later tool progress.
	//   - autoApproved: the turn continued without user input, so any later
	//     notification.permission_prompt is stale noise and should not replace the
	//     current icon.
	copilotToolPermissionState map[int]copilotToolPermissionState

	// subagentCount tracks the number of active sub-agents per window.
	// It is incremented on SubagentStart and decremented on SubagentStop.
	// While the count is > 0, SubagentStop events are suppressed so the
	// robot icon persists until all concurrent agents finish.
	subagentCount map[int]int

	// pendingReset tracks short-lived user-initiated session reset flows.
	// requested means a slash-command reset was submitted and we are waiting for
	// the stop/cleanup lifecycle; clearing means we have already translated at
	// least one stop-like event to user_clear and are holding the marker long
	// enough to absorb any late cleanup noise before the next normal turn clears
	// it.
	pendingReset map[int]resetFlowState

	// notifiedStale tracks windows for which a stale-active notification has
	// already fired in the current stall. Cleared on the next real state update
	// for that window so a future stall produces a fresh notification.
	notifiedStale map[int]bool

	// contextLimitNotified marks windows already swapped to the error icon by
	// ProbeClaudeContextLimit during the current stall episode. Cleared in
	// handler_state_update.go when an "active"-class event re-enters the window
	// (S3) and in pruneClosedWindows when the window closes (S3).
	contextLimitNotified map[int]bool

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

	// lastSessionTitle records the session title from the most recent state
	// update. Used by the sweep as a fallback if the live kitten lookup fails.
	lastSessionTitle map[int]string

	// lastPipeMode tracks whether the latest event for a window was pipe-mode
	// so stale notifications honor the same silence rule.
	lastPipeMode map[int]bool

	// eventLog stores a per-window ring buffer of recent state events. Bounded
	// by maxEventsPerWindow; older entries are dropped from the front. Preserved
	// across terminal transitions so export-events can inspect ended sessions;
	// SweepStale drops entries once Kitty no longer reports the window.
	eventLog map[int][]SessionEvent

	// windowMetadata stores the durable per-window export snapshot. Unlike the
	// live watchdog maps, terminal cleanup and closed-window GC intentionally do
	// not clear this so post-mortem exports still have identity/context even
	// after the original Kitty tab is gone.
	windowMetadata map[int]WindowDebugMetadata

	// onActivity is called on every successfully handled request.
	// The daemon uses this to reset the idle timer.
	onActivity func()

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
		activity:                       make(map[int]time.Time),
		lastAction:                     make(map[int]string),
		lastAccount:                    make(map[int]string),
		lastActiveEvent:                make(map[int]string),
		copilotToolPermissionState:     make(map[int]copilotToolPermissionState),
		subagentCount:                  make(map[int]int),
		pendingReset:                   make(map[int]resetFlowState),
		notifiedStale:                  make(map[int]bool),
		contextLimitNotified:           make(map[int]bool),
		claudeContextLimitProbeEnabled: true,
		lastSeenEventAt:                make(map[string]time.Time),
		notifiedHookSilence:            make(map[string]bool),
		lastSessionTitle:               make(map[int]string),
		lastPipeMode:                   make(map[int]bool),
		eventLog:                       make(map[int][]SessionEvent),
		windowMetadata:                 make(map[int]WindowDebugMetadata),
	}
}

// Handle processes a Request and returns a Response.
func (h *Handler) Handle(req Request) Response {
	if h.onActivity != nil {
		h.onActivity()
	}

	switch req.Type {
	case TypeStateUpdate:
		return h.handleStateUpdate(req)
	case TypeStateQuery:
		return h.handleStateQuery()
	case TypeSessionDetail:
		return h.handleSessionDetail(req)
	case TypeEventsExport:
		return h.handleEventsExport(req)
	case TypeHealth:
		return h.handleHealth()
	case TypeShutdown:
		return h.handleShutdown()
	default:
		return Response{OK: false, Error: "unknown request type: " + string(req.Type)}
	}
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
	snapshot := make(map[int]int64, len(h.activity))
	for id, t := range h.activity {
		snapshot[id] = t.Unix()
	}
	watchdog := make(map[int]WatchdogState, len(h.lastAction))
	for id, action := range h.lastAction {
		if action != "active" && action != "subagent" && action != "compact" {
			continue
		}
		entry := WatchdogState{LastAction: action}
		if t, ok := h.activity[id]; ok {
			entry.LastEventAt = t.Unix()
		}
		if d := h.resolveThreshold(h.lastAccount[id], action); d > 0 {
			entry.ThresholdSec = int(d / time.Second)
		}
		watchdog[id] = entry
	}
	h.stateMu.Unlock()
	return Response{OK: true, Activity: snapshot, Watchdog: watchdog}
}

// handleSessionDetail returns the per-window event ring buffer for req.WindowID.
// An unknown window yields a non-nil empty slice rather than nil so the TUI can
// distinguish "no daemon error" from "no events yet" without a nil check.
func (h *Handler) handleSessionDetail(req Request) Response {
	h.stateMu.Lock()
	src := h.eventLog[req.WindowID]
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
		if src, ok := h.eventLog[req.WindowID]; ok {
			cp := make([]SessionEvent, len(src))
			copy(cp, src)
			out[req.WindowID] = cp
		}
		if snapshot, ok := h.windowMetadata[req.WindowID]; ok {
			meta[req.WindowID] = snapshot
		}
	} else {
		out = make(map[int][]SessionEvent, len(h.eventLog))
		for id, src := range h.eventLog {
			cp := make([]SessionEvent, len(src))
			copy(cp, src)
			out[id] = cp
		}
		meta = make(map[int]WindowDebugMetadata, len(h.windowMetadata))
		for id, snapshot := range h.windowMetadata {
			meta[id] = snapshot
		}
	}
	h.stateMu.Unlock()
	return Response{OK: true, EventsByWindow: out, WindowMetadata: meta}
}

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		h.shutdownOnce.Do(func() { go h.onShutdown() })
	}
	return Response{OK: true}
}
