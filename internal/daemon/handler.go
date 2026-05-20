package daemon

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// kittyTitleClient is the narrow subset of kitty.KittyClient that the stale
// sweep needs. Declaring it locally lets tests provide a fake with just two
// methods instead of all ten on kitty.KittyClient. kitty.SubprocessClient
// satisfies this interface implicitly.
type kittyTitleClient interface {
	ListWindows() ([]kitty.OSWindow, error)
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

	// staleThreshold is the duration after which an "active"-class session
	// with no incoming events is treated as stalled. Zero disables the sweep.
	staleThreshold time.Duration

	// stateMu protects activity and lastAction.
	stateMu  sync.Mutex
	activity map[int]time.Time

	// lastAction records the most recent non-idle/clear action per window.
	// It outlives the state guard so that an idle event arriving after the
	// guard expires can still detect a stale bolt and clear it.
	lastAction map[int]string

	// lastActiveEvent records the hook event name that last set the "active"
	// state, so isAutoApprovedPerm can distinguish PreToolUse (same-tool
	// auto-approval) from PostToolUse (prior tool completed).
	lastActiveEvent map[int]string

	// subagentCount tracks the number of active sub-agents per window.
	// It is incremented on SubagentStart and decremented on SubagentStop.
	// While the count is > 0, SubagentStop events are suppressed so the
	// robot icon persists until all concurrent agents finish.
	subagentCount map[int]int

	// notifiedStale tracks windows for which a stale-active notification has
	// already fired in the current stall. Cleared on the next real state update
	// for that window so a future stall produces a fresh notification.
	notifiedStale map[int]bool

	// lastSessionTitle records the session title from the most recent state
	// update. Used by the sweep as a fallback if the live kitten lookup fails.
	lastSessionTitle map[int]string

	// lastPipeMode tracks whether the latest event for a window was pipe-mode
	// so stale notifications honor the same silence rule.
	lastPipeMode map[int]bool

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

// NewHandler creates a Handler with the given guard and notifier.
func NewHandler(guard *StateGuard, notifier *Notifier) *Handler {
	return &Handler{
		guard:            guard,
		notifier:         notifier,
		startedAt:        time.Now(),
		activity:         make(map[int]time.Time),
		lastAction:       make(map[int]string),
		lastActiveEvent:  make(map[int]string),
		subagentCount:    make(map[int]int),
		notifiedStale:    make(map[int]bool),
		lastSessionTitle: make(map[int]string),
		lastPipeMode:     make(map[int]bool),
	}
}

// SetKittyClient injects the Kitty client used by the stale sweep to clear
// stranded tab-title icons. Passing nil disables the title-clear step
// (notifications still fire). Safe to call once before the sweep runs.
func (h *Handler) SetKittyClient(client kittyTitleClient) {
	h.kittyClient = client
}

// SetStaleThreshold configures the duration after which an "active"-class
// session with no incoming events is treated as stalled. A value of zero
// disables the sweep entirely.
func (h *Handler) SetStaleThreshold(d time.Duration) {
	h.staleThreshold = d
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

func (h *Handler) lastKnownAction(windowID int, currentIconState string) string {
	h.stateMu.Lock()
	last := h.lastAction[windowID]
	h.stateMu.Unlock()
	if last != "" {
		return last
	}
	return actionFromIconState(currentIconState)
}

func actionFromIconState(iconState string) string {
	switch iconState {
	case "bolt":
		return "active"
	case "compact":
		return "compact"
	case "subagent":
		return "subagent"
	case "stop":
		return "stop"
	case "key":
		return "permission"
	case "error":
		// The "error" glyph is shared by failure and stop_failure. Without a
		// daemon-side lastAction record (e.g. fresh daemon restart), we cannot
		// tell them apart from the title alone, so prefer the terminal
		// stop_failure: clearing a real stop_failure on a benign follow-up is
		// worse than over-preserving a transient failure.
		return "stop_failure"
	case "question":
		return "elicitation"
	default:
		return ""
	}
}

func (h *Handler) handleStateUpdate(req Request) Response {
	if req.Action == "" {
		return Response{OK: false, Error: "action is required for state_update"}
	}
	if _, ok := actionPriority[req.Action]; !ok {
		return Response{OK: false, Error: "unknown action: " + req.Action}
	}

	// Only terminal/idle transitions invalidate a pending permission notification.
	// PostToolUse/active/subagent events are normal companions to a real
	// permission_prompt and must not cancel the notification.
	// elicitation and failure are intentionally omitted: they are mid-session
	// states, not terminal, so they don't *invalidate* a permission here. When
	// either of them does fire a notification of its own, the notify layer's
	// cancelPendingLocked supersedes the pending entry (see notify.go MaybeNotify).
	switch req.Action {
	case "idle", "clear", "user_clear", "stop", "stop_failure":
		h.notifier.CancelPending(req.WindowID)
	}

	// SubagentStart: increment reference count so the robot icon persists
	// even when interleaved parent "active" events arrive.
	if req.Action == "subagent" {
		h.stateMu.Lock()
		h.subagentCount[req.WindowID]++
		h.stateMu.Unlock()
	}

	// SubagentStop: decrement counter; suppress "active" while other agents run.
	if req.Event == "SubagentStop" || req.Event == "subagentStop" {
		h.stateMu.Lock()
		wasTracked := h.subagentCount[req.WindowID] > 0
		if wasTracked {
			h.subagentCount[req.WindowID]--
		}
		remaining := h.subagentCount[req.WindowID]
		if wasTracked && remaining == 0 {
			// Drop the zero-valued entry so per-window state cannot accumulate
			// indefinitely across many sessions that never hit a terminal event.
			delete(h.subagentCount, req.WindowID)
		}
		h.stateMu.Unlock()

		if wasTracked && remaining > 0 && req.Action == "active" {
			// More agents still running — refresh the subagent guard (reset expiry)
			// and suppress this "active" transition. Only "active" is suppressed:
			// a SubagentStop carrying a real failure (action == "stop_failure",
			// e.g. stopReason = "error" / "max_tokens") must surface so the user
			// sees the failure even when sibling agents are still running.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			return Response{OK: true, Allowed: false}
		}
		if wasTracked && remaining == 0 {
			// Last agent finished — clear the subagent guard so the "active" below
			// is allowed immediately rather than waiting for the guard TTL to
			// expire. Only clear when the guard is still "subagent": a failure or
			// permission may have legitimately superseded it (e.g. one subagent
			// stop_failure while a sibling was still running), and that terminal
			// state must persist until the user acknowledges it.
			if h.guard.Current(req.WindowID) == "subagent" {
				h.guard.Clear(req.WindowID)
			}
		}
	}

	// Copilot mid-subagent suppression: while background subagents are running,
	// Copilot fires agentStop (per-turn signal) and speculative permissionRequest
	// events that would otherwise overwrite the 🤖 guard. Suppress them here so
	// the reference-counted subagent state remains the source of truth.
	//
	// Event-name match (not action) ensures only Copilot's specific events
	// trigger suppression; Claude's Stop / SessionEnd / PermissionRequest fall
	// through to the normal path. sessionEnd (also action "stop") is
	// intentionally NOT in this list so a real session end still clears state.
	//
	// permissionRequest is suppressed via CancelPending rather than MaybeNotify:
	// once subagents are running, the parent prompt has been approved and any
	// permissionRequest is for an auto-executing background tool. The existing
	// debounce cannot cancel itself here — postToolUse (action "active") returns
	// early from MaybeNotify before reaching cancelPendingLocked, and the
	// auto-approved-permission cancel path requires prevGuard == "active", not
	// "subagent". So we drop any pending permission entry outright.
	// stop_failure carries a real Copilot crash and must surface even mid-subagent,
	// mirroring the SubagentStop failure-surfacing rule above. Only the benign
	// per-turn agentStop (action stop) and speculative permissionRequest are
	// suppressed by this gate.
	if (req.Event == "agentStop" && req.Action != "stop_failure") || req.Event == "permissionRequest" {
		h.stateMu.Lock()
		running := h.subagentCount[req.WindowID] > 0
		count := h.subagentCount[req.WindowID]
		h.stateMu.Unlock()
		if running {
			// Refresh the subagent guard so it does not expire while agents run.
			h.guard.TryUpdate(req.WindowID, "subagent", false)
			if req.Event == "permissionRequest" {
				h.notifier.CancelPending(req.WindowID)
			}
			slog.Debug("copilot mid-subagent suppression",
				"window_id", req.WindowID,
				"event", req.Event,
				"subagent_count", count,
			)
			return Response{OK: true, Allowed: false}
		}
	}

	// Terminal / cleanup states reset the subagent counter.
	// This list intentionally includes "failure" even though the cancel-switch
	// above does not: counter cleanup is safe (losing the count just clears the
	// 🤖 over-suppression), but cancelling a pending permission is unsafe (the
	// user may never see the prompt). A tool failure is a strong signal that any
	// in-flight subagent bookkeeping is stale, hence reset; but it doesn't make
	// a pending permission stale, hence no cancel.
	if req.Action == "stop" || req.Action == "stop_failure" || req.Action == "failure" ||
		req.Action == "idle" || req.Action == "clear" || req.Action == "user_clear" {
		h.stateMu.Lock()
		delete(h.subagentCount, req.WindowID)
		delete(h.notifiedStale, req.WindowID)
		delete(h.lastSessionTitle, req.WindowID)
		delete(h.lastPipeMode, req.WindowID)
		h.stateMu.Unlock()
	}

	// Block non-prompt active events from overwriting a terminal stop state.
	// PostToolBatch can race with Stop in async execution; once the stop guard
	// expires, lastAction is the main barrier, with the current title icon as a
	// restart-safe fallback after daemon auto-starts or restarts. This check
	// runs BEFORE TryUpdate so a suppressed "active" cannot leave the guard
	// armed at "active" — otherwise a subsequent idle would see prevGuard
	// "active" and ask the indicator to clear the persisting stop icon.
	if req.Action == "active" && !req.IsPrompt {
		prev := h.lastKnownAction(req.WindowID, req.CurrentIconState)
		if prev == "stop" || prev == "stop_failure" {
			h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, req.Action, req.PipeMode)
			return Response{OK: true, Allowed: false}
		}
	}

	prevGuard := h.guard.Current(req.WindowID)
	allowed, effective := h.guard.TryUpdate(req.WindowID, req.Action, req.IsPrompt)
	slog.Debug("state transition",
		"window_id", req.WindowID,
		"profile", req.Account,
		"action", req.Action,
		"allowed", allowed,
	)
	if !allowed {
		// The guard suppressed the icon transition, but the user's intent may
		// still warrant a notification (permission/stop/failure/elicitation).
		// Pass req.Action — the notifier filters by its own per-action config and
		// cooldown, so this cannot double-fire with the success path below.
		h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, req.Action, req.PipeMode)
		return Response{OK: true, Allowed: false}
	}

	// Auto-approved permission: preToolUse fired before permissionRequest, so the
	// tool is already running. The key icon should not override the active bolt —
	// there is nothing for the user to approve.
	// Only treat it as auto-approved when the active guard was specifically set by
	// a PreToolUse hook event (not by PostToolUse of a prior tool).
	h.stateMu.Lock()
	prevEvent := h.lastActiveEvent[req.WindowID]
	h.stateMu.Unlock()
	isAutoApprovedPerm := effective == "permission" &&
		prevGuard == "active" &&
		(prevEvent == "PreToolUse" || prevEvent == "preToolUse")

	// Copilot's preToolUse → permissionRequest sequence is speculative: it
	// fires even for tools that auto-approve, producing a spurious "Permission
	// requested" notification when no user input is required. Claude's
	// PreToolUse → permission sequence is a *real* user-facing prompt and its
	// notification must still fire (TestHandler_PermissionSurvivesActiveFollowUp).
	// Event-name casing is the harness signal here: Claude declares PascalCase
	// hooks, Copilot declares camelCase hooks, and req.Event preserves the
	// original casing.
	isCopilotAutoApprovedPerm := isAutoApprovedPerm && prevEvent == "preToolUse"

	h.stateMu.Lock()
	h.activity[req.WindowID] = time.Now()
	h.lastSessionTitle[req.WindowID] = req.SessionTitle
	h.lastPipeMode[req.WindowID] = req.PipeMode
	if effective == "active" {
		h.lastActiveEvent[req.WindowID] = req.Event
	}
	// Record the last substantive action so idle/clear can detect a stale bolt
	// even after the state guard has expired.
	if effective != "idle" && effective != "clear" && effective != "user_clear" && !isAutoApprovedPerm {
		h.lastAction[req.WindowID] = effective
		delete(h.notifiedStale, req.WindowID)
	}
	h.stateMu.Unlock()

	iconState := IconState(effective)
	if req.PreservesIcon {
		iconState = IconStatePreserve
	} else if isAutoApprovedPerm {
		// Clear the permission guard immediately — it was auto-resolved, so a
		// subsequent idle can fall back to lastAction ("active") and clear the bolt.
		h.guard.Clear(req.WindowID)
		iconState = IconStatePreserve
	} else if effective == "idle" || effective == "clear" {
		// Prefer prevGuard (still within the guard window); fall back to
		// lastAction, then the currently displayed title icon, for the common case
		// where the guard already expired or the daemon restarted while the bolt
		// icon is still showing in the title.
		prev := prevGuard
		if prev == "" {
			prev = h.lastKnownAction(req.WindowID, req.CurrentIconState)
		}
		switch prev {
		case "active", "compact", "subagent":
			// Agent finished — clear the active indicator and reset tracking.
			iconState = ""
			h.stateMu.Lock()
			delete(h.lastAction, req.WindowID)
			delete(h.lastActiveEvent, req.WindowID)
			h.stateMu.Unlock()
		default:
			// stop/permission/failure icons persist until the user notices them.
			iconState = IconStatePreserve
		}
	} else if effective == "user_clear" {
		// TUI-initiated wipe — reset lastAction so a future idle doesn't try
		// to clear an icon that no longer exists.
		h.stateMu.Lock()
		delete(h.lastAction, req.WindowID)
		delete(h.lastActiveEvent, req.WindowID)
		h.stateMu.Unlock()
	}
	if isCopilotAutoApprovedPerm {
		h.notifier.CancelPending(req.WindowID)
	} else {
		h.notifier.MaybeNotify(req.WindowID, req.SessionTitle, effective, req.PipeMode)
	}

	return Response{OK: true, Allowed: true, IconState: iconState}
}

func (h *Handler) handleStateQuery() Response {
	h.stateMu.Lock()
	snapshot := make(map[int]int64, len(h.activity))
	for id, t := range h.activity {
		snapshot[id] = t.Unix()
	}
	h.stateMu.Unlock()
	return Response{OK: true, Activity: snapshot}
}

func (h *Handler) handleShutdown() Response {
	if h.onShutdown != nil {
		h.shutdownOnce.Do(func() { go h.onShutdown() })
	}
	return Response{OK: true}
}

// SweepStale detects "active"-class sessions whose last incoming event predates
// staleThreshold and resolves them: it fires a single-shot "Session idle"
// notification, clears the in-memory state so future events restart cleanly,
// and clears the stale icon in the tab title.
//
// Behavior is gated on h.staleThreshold > 0. The "stale" action is
// daemon-internal: it never crosses the IPC wire and is not present in
// actionPriority or actionIconState. The notifier is the only subsystem that
// recognizes it as an action name.
func (h *Handler) SweepStale(now time.Time) {
	if h.staleThreshold <= 0 {
		return
	}
	type staleEntry struct {
		windowID int
		title    string
		pipeMode bool
		notify   bool
	}
	var stale []staleEntry

	h.stateMu.Lock()
	for windowID, last := range h.activity {
		if now.Sub(last) < h.staleThreshold {
			continue
		}
		action := h.lastAction[windowID]
		if action != "active" && action != "subagent" && action != "compact" {
			continue
		}
		title := h.lastSessionTitle[windowID]
		pipe := h.lastPipeMode[windowID]
		notify := !h.notifiedStale[windowID]
		if notify {
			h.notifiedStale[windowID] = true
		}
		// Reset so the next idle/clear/active does not try to re-clear an icon
		// we just cleared, and so the lastKnownAction fallback returns "".
		delete(h.lastAction, windowID)
		delete(h.lastActiveEvent, windowID)
		delete(h.subagentCount, windowID)
		stale = append(stale, staleEntry{windowID, title, pipe, notify})
	}
	h.stateMu.Unlock()

	for _, s := range stale {
		h.guard.Clear(s.windowID)
		if s.notify {
			h.notifier.MaybeNotify(s.windowID, s.title, "stale", s.pipeMode)
		}
		if h.kittyClient != nil {
			h.clearStaleTitle(s.windowID, s.title)
		}
	}
}

// clearStaleTitle removes any state icon from the tab containing windowID
// while preserving the star glyph and base title. Best-effort: errors from
// the Kitty client are logged at debug and swallowed so the sweep continues
// to handle other windows.
func (h *Handler) clearStaleTitle(windowID int, fallbackTitle string) {
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("sweep: list windows failed", "window_id", windowID, "err", err)
		return
	}
	tab, _, ok := kitty.FindTabByWindowID(windows, windowID)
	if !ok {
		return
	}
	base, _, starred := palette.ParseTitle(tab.Title)
	newTitle := palette.BuildTitle(base, "", starred)
	if newTitle == tab.Title {
		return
	}
	if err := h.kittyClient.SetTabTitle(kitty.MatchTabByWindowID(windowID), newTitle); err != nil {
		slog.Debug("sweep: set tab title failed",
			"window_id", windowID,
			"fallback_title", fallbackTitle,
			"err", err,
		)
	}
}
