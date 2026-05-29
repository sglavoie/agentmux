package daemon

import (
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// SetKittyClient injects the Kitty client used by the stale sweep to clear
// stranded tab-title icons. Passing nil disables the title-clear step
// (notifications still fire). Safe to call once before the sweep runs.
func (h *Handler) SetKittyClient(client kittyTitleClient) {
	h.kittyClient = client
}

// SetStaleThresholds configures the per-action sweep thresholds. The global
// map keys are action names ("active", "subagent", "compact"); the
// perAccount map is keyed first by account name (case-insensitive — names
// are lowercased on insert) and then by action name. A zero or missing
// inner entry falls back to the global default for that action. The sweep
// is disabled entirely when every global value is <= 0 AND no per-account
// override supplies a positive duration.
func (h *Handler) SetStaleThresholds(global map[string]time.Duration, perAccount map[string]map[string]time.Duration) {
	gs := make(map[string]time.Duration, len(global))
	for action, d := range global {
		gs[action] = d
	}
	h.staleThresholds = gs

	pa := make(map[string]map[string]time.Duration, len(perAccount))
	for account, m := range perAccount {
		if len(m) == 0 {
			continue
		}
		inner := make(map[string]time.Duration, len(m))
		for action, d := range m {
			inner[action] = d
		}
		pa[strings.ToLower(account)] = inner
	}
	h.perAccountThresholds = pa
}

// resolveThreshold returns the sweep duration to use for the given account
// and action. Per-account overrides win over the global default; a zero
// value at either level means "no threshold configured for this action".
func (h *Handler) resolveThreshold(account, action string) time.Duration {
	if action == "" {
		return 0
	}
	if account != "" {
		if inner, ok := h.perAccountThresholds[strings.ToLower(account)]; ok {
			if d, ok := inner[action]; ok && d > 0 {
				return d
			}
		}
	}
	return h.staleThresholds[action]
}

// SetClaudeContextLimitProbe toggles the periodic Claude context-limit probe.
// Mirrors the [defaults.watchdog].claude_context_limit_probe config knob.
// Safe to call once before the probe ticker fires.
func (h *Handler) SetClaudeContextLimitProbe(enabled bool) {
	h.claudeContextLimitProbeEnabled = enabled
}

// SetHookSilenceThresholds configures the global and per-account hook-silence
// thresholds consumed by SweepHookSilence. The global value applies whenever a
// per-account override is missing or zero. A global value of 0 disables the
// sweep unless a positive per-account override is supplied. Per-account keys
// are lowercased on insert; lookups also lowercase the account name.
func (h *Handler) SetHookSilenceThresholds(global time.Duration, perAccount map[string]time.Duration) {
	h.hookSilenceThreshold = global
	pa := make(map[string]time.Duration, len(perAccount))
	for account, d := range perAccount {
		if d <= 0 {
			continue
		}
		pa[strings.ToLower(account)] = d
	}
	h.perAccountHookSilence = pa
}

// resolveHookSilenceThreshold returns the hook-silence duration to use for the
// given account. A positive per-account override wins over the global default;
// a zero value at both levels means the sweep is disabled for that account.
func (h *Handler) resolveHookSilenceThreshold(account string) time.Duration {
	if account != "" {
		if d, ok := h.perAccountHookSilence[strings.ToLower(account)]; ok && d > 0 {
			return d
		}
	}
	return h.hookSilenceThreshold
}

// hookSilenceWindowID maps an account name to a stable synthetic window ID
// used as the notifier dedup key for hook-silence dispatches. Real Kitty
// window IDs are positive ints; negating the FNV-32 hash places the synthetic
// IDs in a non-overlapping negative range. The -1 offset prevents collision
// with the zero value (uint32 hashes are 32-bit, so a 64-bit int can hold the
// full negation without overflow).
func hookSilenceWindowID(account string) int {
	hh := fnv.New32a()
	_, _ = hh.Write([]byte(strings.ToLower(account)))
	return -int(hh.Sum32()) - 1
}

// anyStaleThresholdSet reports whether at least one global or per-account
// threshold is positive. The sweep early-exits when this returns false.
func (h *Handler) anyStaleThresholdSet() bool {
	for _, d := range h.staleThresholds {
		if d > 0 {
			return true
		}
	}
	for _, inner := range h.perAccountThresholds {
		for _, d := range inner {
			if d > 0 {
				return true
			}
		}
	}
	return false
}

// pruneClosedWindows drops per-window state for windows that Kitty no longer
// reports as live. Called from SweepStale once the live set has been
// materialised from kitty.ListWindows().
//
// windowMetadata is intentionally preserved across closes so post-mortem
// `amx daemon export-events` invocations still have identity/context for
// sessions whose tabs have already disappeared. Every other per-window map
// must be GC'd here — a session that closes without a terminal hook event
// (e.g. user closes the tab with Cmd+W, daemon misses the SessionEnd) would
// otherwise leak an entry per killed window for the daemon's lifetime, which
// can also let notifiedStale latches survive into a hypothetical future
// Kitty-ID reuse and silently suppress a notification.
func (h *Handler) pruneClosedWindows(liveIDs map[int]bool) {
	h.stateMu.Lock()
	closed := make(map[int]struct{})
	collect := func(id int) {
		if !liveIDs[id] {
			closed[id] = struct{}{}
		}
	}
	for id := range h.eventLog {
		collect(id)
	}
	for id := range h.activity {
		collect(id)
	}
	for id := range h.lastAction {
		collect(id)
	}
	for id := range h.lastAccount {
		collect(id)
	}
	for id := range h.lastActiveEvent {
		collect(id)
	}
	for id := range h.copilotToolPermissionState {
		collect(id)
	}
	for id := range h.subagentCount {
		collect(id)
	}
	for id := range h.notifiedStale {
		collect(id)
	}
	for id := range h.contextLimitNotified {
		collect(id)
	}
	for id := range h.lastSessionTitle {
		collect(id)
	}
	for id := range h.lastPipeMode {
		collect(id)
	}
	for id := range h.pendingReset {
		collect(id)
	}
	for id := range h.mutedWindows {
		collect(id)
	}
	for id := range closed {
		delete(h.eventLog, id)
		delete(h.activity, id)
		delete(h.lastAction, id)
		delete(h.lastAccount, id)
		delete(h.lastActiveEvent, id)
		delete(h.copilotToolPermissionState, id)
		delete(h.subagentCount, id)
		delete(h.notifiedStale, id)
		delete(h.contextLimitNotified, id)
		delete(h.lastSessionTitle, id)
		delete(h.lastPipeMode, id)
		delete(h.pendingReset, id)
		delete(h.mutedWindows, id)
	}
	h.stateMu.Unlock()
	// Notifier cooldown/pending entries are keyed by window ID too. Without
	// cleanup the notifier's per-(window, action) cooldown map grows linearly
	// with closed windows, and a stale pending permission timer for a dead
	// window would fire (and harmlessly dispatch under the old session title)
	// well after the window itself is gone.
	if h.notifier != nil {
		for id := range closed {
			h.notifier.PurgeWindow(id)
		}
	}
	// State-guard entries are keyed by window ID and otherwise only removed by
	// idle/clear/user_clear events. A window closed without firing a terminal
	// hook would leak its guardEntry for the daemon's lifetime.
	if h.guard != nil {
		for id := range closed {
			h.guard.Clear(id)
		}
	}
}

// SweepStale detects "active"-class sessions whose last incoming event predates
// the per-action threshold and resolves them: it fires a single-shot "Session
// idle" notification, clears the in-memory state so future events restart
// cleanly, and clears the stale icon in the tab title.
//
// Behavior is gated on at least one configured threshold (global or
// per-account) being > 0. The "stale" action is daemon-internal: it never
// crosses the IPC wire and is not present in actionPriority or
// actionIconState. The notifier is the only subsystem that recognizes it as
// an action name.
func (h *Handler) SweepStale(now time.Time) {
	// Prune eventLog entries for windows that Kitty no longer reports as live.
	// Runs before the staleness scan so an event-log GC happens every sweep
	// regardless of whether anyStaleThresholdSet() is true below.
	if h.kittyClient != nil {
		if windows, err := h.kittyClient.ListWindows(); err == nil {
			liveIDs := make(map[int]bool)
			for _, ow := range windows {
				for _, tab := range ow.Tabs {
					for _, w := range tab.Windows {
						liveIDs[w.ID] = true
					}
				}
			}
			h.pruneClosedWindows(liveIDs)
		} else {
			slog.Debug("sweep: list windows failed for GC prune", "err", err)
		}
	}

	if !h.anyStaleThresholdSet() {
		return
	}
	type staleEntry struct {
		windowID int
		account  string
		title    string
		pipeMode bool
		notify   bool
	}
	var stale []staleEntry

	h.stateMu.Lock()
	for windowID, last := range h.activity {
		action := h.lastAction[windowID]
		if action != "active" && action != "subagent" && action != "compact" {
			continue
		}
		account := h.lastAccount[windowID]
		threshold := h.resolveThreshold(account, action)
		if threshold <= 0 {
			continue
		}
		if now.Sub(last) < threshold {
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
		// eventLog is intentionally preserved across the sweep so detail-pane
		// history survives until the underlying Kitty window actually closes.
		delete(h.lastAction, windowID)
		delete(h.lastActiveEvent, windowID)
		delete(h.copilotToolPermissionState, windowID)
		delete(h.subagentCount, windowID)
		stale = append(stale, staleEntry{
			windowID: windowID,
			account:  account,
			title:    title,
			pipeMode: pipe,
			notify:   notify,
		})
	}
	h.stateMu.Unlock()

	for _, s := range stale {
		if h.guard != nil {
			h.guard.Clear(s.windowID)
		}
		if s.notify && h.notifier != nil {
			h.notifier.MaybeNotify(s.windowID, s.title, s.account, "stale", s.pipeMode)
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
	base, _, starred, muted := palette.ParseTitleFull(tab.Title)
	newTitle := palette.BuildTitleFull(base, "", starred, muted)
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

// SweepHookSilence detects accounts whose most recent event predates the
// configured hook-silence threshold and fires a one-shot notification per
// silence stretch. Unlike SweepStale, which is per-window, this sweep is
// account-level: it iterates running Kitty windows (cc_account user var) so
// notifications are not produced for accounts the user has not launched.
//
// Behavior:
//   - No-op when h.kittyClient is nil (test environments without Kitty).
//   - No-op when ListWindows fails — logged at debug and swallowed so other
//     ticks can retry.
//   - The "hook_silence" action is daemon-internal: not in actionPriority or
//     actionIconState; only the notifier recognizes it as an action name.
//   - The notifier's per-(windowID, action) cooldown is keyed by a synthetic
//     negative ID derived from the account name so it cannot collide with
//     real positive Kitty window IDs (see hookSilenceWindowID).
func (h *Handler) SweepHookSilence(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("hook-silence sweep: list windows failed", "err", err)
		return
	}

	running := make(map[string]bool)
	for _, ow := range windows {
		for _, tab := range ow.Tabs {
			for _, w := range tab.Windows {
				if acc := w.UserVars.Account; acc != "" {
					running[acc] = true
				}
			}
		}
	}
	if len(running) == 0 {
		return
	}

	type silentEntry struct {
		account string
	}
	var silent []silentEntry

	h.stateMu.Lock()
	for account := range running {
		threshold := h.resolveHookSilenceThreshold(account)
		if threshold <= 0 {
			continue
		}
		accountKey := strings.ToLower(account)
		last, ok := h.lastSeenEventAt[accountKey]
		if !ok {
			// No event has ever been seen for this account during the
			// daemon's lifetime — skip. The first event from this account
			// will seed lastSeenEventAt and arm the detector.
			continue
		}
		if now.Sub(last) < threshold {
			continue
		}
		if h.notifiedHookSilence[accountKey] {
			continue
		}
		h.notifiedHookSilence[accountKey] = true
		silent = append(silent, silentEntry{account: account})
	}
	h.stateMu.Unlock()

	for _, s := range silent {
		if h.notifier != nil {
			h.notifier.MaybeNotify(hookSilenceWindowID(s.account), s.account, s.account, "hook_silence", false)
		}
	}
}
