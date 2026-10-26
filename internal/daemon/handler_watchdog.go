package daemon

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/gitstate"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// SetKittyClient injects the Kitty client used by the stale sweep to clear
// stranded tab-title icons. Passing nil disables the title-clear step
// (notifications still fire). Safe to call once before the sweep runs.
func (h *Handler) SetKittyClient(client kittyTitleClient) {
	h.useKittyClient(client)
	// Prime the snapshot cache immediately so the hook path has something to
	// route against before the first render tick. The render/restore loop keeps
	// it fresh afterward; a list error here just leaves the cache cold. This
	// list is synchronous, so production startup uses useKittyClient + the
	// bounded restore instead, and only tests and simple callers rely on the
	// prime here.
	h.primeSnapshot()
}

// useKittyClient installs the Kitty client without priming the snapshot cache.
// Production startup uses this so an unbounded ListWindows can never block the
// daemon before its bounded initial restore runs.
func (h *Handler) useKittyClient(client kittyTitleClient) {
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
	h.staleThresholds = activity.NewStaleThresholds(global, perAccount)
}

// SetClaudeContextLimitProbe toggles the periodic Claude context-limit probe.
// Mirrors the [defaults.watchdog].claude_context_limit_probe config knob.
// Safe to call once before the probe ticker fires.
func (h *Handler) SetClaudeContextLimitProbe(enabled bool) {
	h.claudeContextLimitProbeEnabled = enabled
}

// SetGitProbe enables or disables the per-directory Git probe. Mirrors the
// [defaults].git_probe config knob. Disabling installs no cache at all rather
// than an idle one, so a user who turned it off pays nothing — not a
// subprocess, not a goroutine, not a map. ttl and timeout mirror
// git_probe_ttl and git_probe_timeout; zero keeps gitstate's defaults. Safe to
// call once before the watchdog loop starts.
func (h *Handler) SetGitProbe(enabled bool, ttl, timeout time.Duration) {
	if !enabled {
		h.gitState = nil
		return
	}
	h.gitState = gitstate.New(gitstate.Options{TTL: ttl, Timeout: timeout})
}

// setGitStateCache installs a specific probe cache. Tests use it to inject a
// scripted executor and clock; production goes through SetGitProbe.
func (h *Handler) setGitStateCache(cache *gitstate.Cache) {
	h.gitState = cache
}

// SetStopConfirm configures the confirmed-idle gate applied to native stops:
// delay is the quiet period a held stop must observe, timeout is the hard
// deadline past which it commits unconfirmed, and enabled false restores the
// immediate-stop behaviour. Mirrors the [defaults.stop_confirm] config block.
// Safe to call once before the hook path runs.
func (h *Handler) SetStopConfirm(delay, timeout time.Duration, enabled bool) {
	h.stopConfirmDelay = delay
	h.stopConfirmTimeout = timeout
	h.stopConfirmEnabled = enabled
}

// SetCopilotTranscriptHomes installs the Copilot home directories the transcript
// stop probe resolves a managed tab's interactive session transcript against.
// profileHomes is the ordered set of configured Copilot profile homes (the
// caller must ~-expand them; ResolveCopilotTranscript joins verbatim);
// defaultHome is harness.DefaultHomeDir(harness.Copilot). Safe to call once
// before the probe ticker fires.
func (h *Handler) SetCopilotTranscriptHomes(profileHomes []string, defaultHome string) {
	homes := make([]string, 0, len(profileHomes))
	for _, home := range profileHomes {
		if home != "" {
			homes = append(homes, home)
		}
	}
	h.copilotTranscriptHomes = homes
	h.copilotDefaultHome = defaultHome
}

// SetHookSilenceThresholds configures the global and per-account hook-silence
// thresholds consumed by SweepHookSilence. The global value applies whenever a
// per-account override is missing or zero. A global value of 0 disables the
// sweep unless a positive per-account override is supplied. Per-account keys
// are lowercased on insert; lookups also lowercase the account name.
func (h *Handler) SetHookSilenceThresholds(global time.Duration, perAccount map[string]time.Duration) {
	h.hookSilence = activity.NewHookSilenceThresholds(global, perAccount)
}

const (
	// windowTombstoneRetention bounds how long a closed window's durable
	// metadata survives for post-mortem export after its live state is GC'd.
	windowTombstoneRetention = 24 * time.Hour
	// maxWindowTombstones bounds the tombstone count; past it the oldest
	// tombstones (and their durable metadata) are evicted first.
	maxWindowTombstones = 1000
)

// Synthetic daemon-emitted event names. They never cross the IPC wire as
// requests; the watchdog, reconciliation, and closed-window GC append them so
// the per-window event log and the durable export metadata record why the
// daemon changed state on its own.
const (
	eventWatchdogStale     = "WatchdogStale"
	eventSourceExpired     = "SourceExpired"
	eventASLReconciled     = "ASLReconciled"
	eventWindowClosed      = "WindowClosed"
	eventAuthoritativeIdle = "AuthoritativeIdle"
)

// appendWatchdogEventLocked records one synthetic daemon event atomically in
// both diagnostic surfaces: the current per-window event ring (via
// appendEventLocked, which also maintains the durable Stats counters) and the
// durable watchdog section of the export metadata. Callers hold stateMu.
func (h *Handler) appendWatchdogEventLocked(windowID, originWindowID int, at time.Time, event, action, source, reason string) {
	if originWindowID <= 0 {
		originWindowID = windowID
	}
	h.appendEventLocked(windowID, protocol.SessionEvent{
		At:             at.Unix(),
		Event:          event,
		Action:         action,
		Source:         source,
		Reason:         reason,
		OriginWindowID: originWindowID,
	})
	meta := h.windowMetadata[windowID]
	meta.Watchdog.LastEvent = event
	meta.Watchdog.LastReason = reason
	meta.Watchdog.LastAt = at.Unix()
	switch event {
	case eventSourceExpired:
		meta.Watchdog.SourceExpiries++
	case eventWatchdogStale:
		meta.Watchdog.StaleSweeps++
	case eventASLReconciled:
		meta.Watchdog.Reconciles++
	}
	h.windowMetadata[windowID] = meta
}

// SweepHookSilence detects accounts whose most recent event predates the
// configured hook-silence threshold and fires a one-shot notification per
// silence stretch. Unlike SweepStale, which is per-window, this sweep is
// account-level: it iterates running Kitty windows (cc_account user var) and
// the sessions bound on other backends (the profile of their label) so
// notifications are not produced for accounts the user has not launched.
//
// Behavior:
//   - No-op when h.kittyClient is nil (test environments without Kitty).
//   - No-op when ListWindows fails — logged at debug and swallowed so other
//     ticks can retry.
//   - The "hook_silence" action is daemon-internal: not in
//     protocol.ActionPriority or protocol.IconState; only the notifier
//     recognizes it as an action name.
//   - The notifier's per-(windowID, action) cooldown is keyed by a synthetic
//     negative ID derived from the account name so it cannot collide with
//     real positive Kitty window IDs (see activity.HookSilenceWindowID).
func (h *Handler) SweepHookSilence(now time.Time) {
	client, _ := h.kittyListTarget()
	if client == nil {
		return
	}
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("hook-silence sweep: list windows failed", "err", err)
		return
	}
	h.SweepHookSilenceWithSnapshot(now, windows)
}

func (h *Handler) SweepHookSilenceWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.sweepHookSilence(now, windows, 0, false)
}

// sweepHookSilence evaluates account-level hook silence on the same monotonic
// accumulation the stale sweep uses: an account's silence grows only by
// in-process observed elapsed time (seeded from wall-clock age on first
// observation), so a system sleep cannot fire a false "hooks silent"
// notification the moment the daemon wakes.
func (h *Handler) sweepHookSilence(now time.Time, windows []kitty.OSWindow, credit time.Duration, gapTick bool) {
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

	type silentEntry struct {
		account string
	}
	var silent []silentEntry

	h.stateMu.Lock()
	for _, windows := range h.otherKittyTrees {
		for _, ow := range windows {
			for _, tab := range ow.Tabs {
				for _, win := range tab.Windows {
					if account := win.UserVars.Account; account != "" {
						running[account] = true
					}
				}
			}
		}
	}
	// A session on another backend is in no window listing; the profile its
	// pane named is its account, for as long as it holds a slot.
	for _, b := range h.runtimeRoutes.bySlot {
		if b.target.backend == string(runtimesession.BackendKitty) {
			continue
		}
		if profile := h.runtimeLabels[b.runtime].profile; profile != "" && !runningAccount(running, profile) {
			running[profile] = true
		}
	}
	for account := range running {
		threshold := h.hookSilence.Resolve(account)
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
		entry := h.hookSilenceIdle[accountKey]
		if entry == nil || !entry.Anchor.Equal(last) {
			entry = &activity.AccountIdle{Anchor: last}
			if !gapTick {
				if age := now.Sub(last); age > 0 {
					entry.Idle = age
				}
			}
			h.hookSilenceIdle[accountKey] = entry
		} else {
			entry.Idle += credit
		}
		if entry.Idle < threshold {
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
			h.notifier.MaybeNotifyTarget(notification.NotificationTarget{
				WindowID: activity.HookSilenceWindowID(s.account), Account: s.account,
			}, s.account, "hook_silence", false)
		}
	}
}

// runningAccount reports whether account is already in running under another
// spelling: thresholds and silence are kept per lowercased account, so two
// spellings of one must not be judged, or notified, twice.
func runningAccount(running map[string]bool, account string) bool {
	for known := range running {
		if strings.EqualFold(known, account) {
			return true
		}
	}
	return false
}

// SweepStale detects "active"-class sessions whose last incoming event predates
// the per-action threshold and resolves them: it fires a single-shot "Session
// idle" notification, clears the in-memory state so future events restart
// cleanly, and clears the stale icon in the tab title.
//
// Behavior is gated on at least one configured threshold (global or
// per-account) being > 0. The "stale" action is daemon-internal: it never
// crosses the IPC wire and is not present in protocol.ActionPriority or
// protocol.IconState. The notifier is the only subsystem that recognizes it
// as an action name.
func (h *Handler) SweepStale(now time.Time) {
	var windows []kitty.OSWindow
	var listedSocket string
	// Prune eventLog entries for windows that Kitty no longer reports as live.
	// Runs before the staleness scan so an event-log GC happens every sweep
	// regardless of whether anyStaleThresholdSet() is true below.
	if client, socket := h.kittyListTarget(); client != nil {
		if snapshot, err := client.ListWindows(); err == nil {
			windows = snapshot
			listedSocket = socket
			h.pruneClosedWindowsFor(now, liveWindowIDs(windows), socket)
		} else {
			slog.Debug("sweep: list windows failed for GC prune", "err", err)
		}
	}
	changed := h.SweepStaleWithSnapshot(now, windows)
	if changed && len(windows) > 0 {
		h.renderSnapshotFor(windows, listedSocket)
	}
}

// SweepStaleWithSnapshot runs one staleness pass without advancing the
// monotonic idle accumulation (credit 0): entries not yet tracked are seeded
// from their wall-clock age, so a one-shot call over pre-aged state expires it
// immediately. The production watchdog loop calls sweepStale directly with the
// tick's observed elapsed credit instead.
func (h *Handler) SweepStaleWithSnapshot(now time.Time, _ []kitty.OSWindow) bool {
	return h.sweepStale(now, 0, false, true)
}

// sweepStale evaluates reducer retention plus per-fact and per-window
// staleness. Acknowledged retention is fixed internal policy and runs even when
// every configurable stale threshold is disabled; otherwise an idle window
// could retain an acknowledged cutoff forever after it crosses the one-hour
// boundary. Idleness is the accumulated in-process observed time (see
// advanceWindowIdle/advanceFactIdle) — activity timestamps are never rewritten
// and never compared across a scheduling gap. Individual source facts expire
// on their own thresholds (SourceExpired); the whole-window legacy scalar
// clears — with its one-shot "Session idle" notification — only when the
// window as a whole has been idle past its threshold (WatchdogStale).
//
// kittySlots is false while no Kitty snapshot is published: the pass then
// covers only the slots of sessions on other backends, which no snapshot
// describes, and leaves every Kitty window as it is.
func (h *Handler) sweepStale(now time.Time, credit time.Duration, gapTick bool, kittySlots bool) bool {
	staleThresholdsEnabled := h.staleThresholds.AnySet()
	type staleEntry struct {
		windowID int
		account  string
		title    string
		pipeMode bool
		notify   bool
	}
	var stale []staleEntry
	changedWindows := 0

	h.stateMu.Lock()
	for windowID, st := range h.store {
		if !kittySlots && !isRuntimeSyntheticSlot(windowID) {
			continue
		}
		compacted := 0
		if st.Reducer != nil {
			compacted = st.Reducer.Compact(now.Unix())
			if compacted > 0 {
				// Retention compaction is a reducer mutation even though acknowledged
				// facts do not contribute visible state. Advance the receipt so a
				// screen observation captured before compaction cannot settle over a
				// different persisted fact set.
				st.ShadowSeq++
			}
		}

		account := st.LastAccount
		// A stop withheld while its pane still shows background work running is
		// bounded by its own deadline, which the probe pushes back only while
		// that work is on screen: the ⚡ it keeps is not an idle window.
		backgroundHeld := h.stopHoldWaitsOnBackgroundLocked(windowID)
		expired, aslExecuting := 0, false
		if staleThresholdsEnabled && !backgroundHeld {
			expired, aslExecuting = h.expireStaleFactsLocked(windowID, st, now, credit, gapTick)
		}

		action := st.LastAction
		windowStale := false
		if staleThresholdsEnabled && (action == "active" || action == "subagent" || action == "compact") {
			if threshold := h.staleThresholds.Resolve(account, action); threshold > 0 {
				idle := st.AdvanceIdle(now, credit, gapTick)
				// An authoritative ASL turn or verification lease is live
				// evidence the loop is still working this window; the bolt is
				// not cleared out from under it.
				windowStale = idle >= threshold && (action != "active" || !aslExecuting) && !backgroundHeld
			}
		} else if staleThresholdsEnabled {
			st.ResetIdle()
		}

		switch {
		case windowStale:
			title := st.LastSessionTitle
			pipe := st.LastPipeMode
			notify := !st.NotifiedStale
			if notify {
				st.NotifiedStale = true
			}
			// Reset so the next idle/clear/active does not try to re-clear an
			// icon we just cleared, and so the lastKnownAction fallback returns
			// "". eventLog is intentionally preserved across the sweep so
			// detail-pane history survives until the Kitty window closes.
			st.LastAction = ""
			st.LastActiveEvent = ""
			st.CopilotToolPermissionState = state.CopilotToolPermissionNone
			st.SubagentCount = 0
			st.DesiredIcon = ""
			st.StateSet = true
			if st.Reducer != nil {
				// The stale facts were already expired individually above;
				// re-reduce so surviving facts (attention/terminal icons, ASL
				// leases) keep the tab rather than being wiped with the scalar.
				// The busy anchor is left to publishEffectiveLocked's invariant:
				// clearing it here would restart the timer on a window whose
				// surviving fact keeps it in the active class without interruption.
				h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
			} else {
				// A nil-reducer window never reaches publishEffectiveLocked, so
				// the anchor would otherwise stay live on a swept window.
				st.BusySince = time.Time{}
			}
			h.appendWatchdogEventLocked(windowID, windowID, now, eventWatchdogStale, "stale", "",
				"no activity for "+st.IdleDuration().Truncate(time.Second).String()+
					" (threshold "+h.staleThresholds.Resolve(account, action).String()+")")
			h.syncRendererMetadataLocked(windowID)
			h.markWindowDirtyLocked(windowID)
			stale = append(stale, staleEntry{
				windowID: windowID,
				account:  account,
				title:    title,
				pipeMode: pipe,
				notify:   notify,
			})
		case compacted > 0 || expired > 0:
			// Retention compaction or per-fact expiry without a whole-window
			// clear: publish the new reduction; only an actual visible change
			// forces a Kitty write. An invisible retention-only mutation still
			// marks persistence dirty so amx_state drops the expired cutoffs.
			before := st.Effective
			h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
			h.syncRendererMetadataLocked(windowID)
			if st.Effective != before {
				h.markWindowDirtyLocked(windowID)
			} else {
				h.markWindowActivityDirtyLocked(windowID)
			}
			changedWindows++
		}
	}
	h.stateMu.Unlock()

	for _, s := range stale {
		if h.guard != nil {
			h.guard.Clear(s.windowID)
		}
		if s.notify {
			h.maybeNotify(s.windowID, s.title, s.account, "stale", s.pipeMode)
		}
	}
	return len(stale) > 0 || changedWindows > 0
}

// expireStaleFactsLocked advances the per-fact idle accumulators for one
// window and expires the individual source facts whose accumulated idle time
// exceeded their per-icon threshold. It reports how many facts expired and
// whether an authoritative ASL lease is currently executing (a live
// turn/verification lease the watchdog has NOT judged stale) — while one is,
// bolt facts from other sources are protected: a long tool call that emits no
// hooks must not lose its ⚡ while the loop's heartbeats prove work is still
// running. The ASL fact itself still expires on its own threshold (heartbeats
// keep a healthy run's fact fresh); it then loses ⚡ but keeps 🔁 — only a
// finish event or two-miss reconciliation releases the lease. Callers hold
// stateMu.
func (h *Handler) expireStaleFactsLocked(windowID int, st *state.WindowState, now time.Time, credit time.Duration, gapTick bool) (expired int, aslExecuting bool) {
	if st.Reducer == nil {
		return 0, false
	}
	type pendingExpiry struct {
		fact      protocol.SourceFactSnapshot
		threshold time.Duration
		idle      time.Duration
	}
	var expiries []pendingExpiry
	seen := make(map[protocol.SourceID]bool)
	for _, fact := range st.Reducer.Facts() {
		src := fact.Source()
		action := activity.StaleActionForIcon(fact.Icon)
		if action == "" || fact.Acked {
			continue
		}
		seen[src] = true
		threshold := h.staleThresholds.Resolve(st.LastAccount, action)
		idle := st.AdvanceFactIdle(fact, now, credit, gapTick)
		factStale := threshold > 0 && idle >= threshold
		if fact.Kind == protocol.SourceASL && fact.LeaseLive && !factStale &&
			(fact.Phase == state.ASLPhaseTurn || fact.Phase == state.ASLPhaseVerification) {
			aslExecuting = true
		}
		if factStale {
			expiries = append(expiries, pendingExpiry{fact: fact, threshold: threshold, idle: idle})
		}
	}
	// Accumulators for facts that no longer contribute an active-class icon
	// (retired, acked, or advanced to another state) are dropped so the map
	// cannot grow with dead sources.
	st.RetainFactIdle(seen)
	for _, p := range expiries {
		if aslExecuting && p.fact.Icon == "bolt" && p.fact.Kind != protocol.SourceASL {
			continue
		}
		if !st.Reducer.ExpireFact(p.fact.Source(), p.fact.Seq, now.Unix()) {
			continue
		}
		st.ForgetFactIdle(p.fact.Source())
		expired++
		h.appendWatchdogEventLocked(windowID, p.fact.OriginWindowID, now, eventSourceExpired, p.fact.Action,
			string(p.fact.Kind)+":"+p.fact.Key,
			p.fact.Icon+" fact idle for "+p.idle.Truncate(time.Second).String()+
				" (threshold "+p.threshold.String()+")")
	}
	// A watchdog expiry acknowledges live source facts in place, a real reducer
	// mutation that carries no per-source sequence of its own. Advance the
	// window-wide receipt generation once so an authoritative idle observation
	// captured before this sweep is discarded rather than allowed to settle over
	// the freshly self-healed state.
	if expired > 0 {
		st.ShadowSeq++
	}
	return expired, aslExecuting
}

// pruneClosedWindows drops per-window state for windows that Kitty no longer
// reports as live. Called from the maintenance tick once the live set has
// been materialised from kitty.ListWindows().
//
// The live per-window store must be GC'd here — a session that closes without
// a terminal hook event (e.g. user closes the tab with Cmd+W, daemon misses
// the SessionEnd) would otherwise leak an entry per killed window for the
// daemon's lifetime, which can also let notifiedStale latches survive into a
// future Kitty-ID reuse and silently suppress a notification. Alongside the
// store, the close cancels everything keyed by the window: its pending render
// queue entry, notifier timers/cooldowns, guard entry, remembered Kitty
// socket, and ASL miss streaks.
//
// windowMetadata is preserved behind a bounded tombstone so post-mortem
// `amx daemon export-events` invocations still have identity/context for
// sessions whose tabs have already disappeared; the tombstone (and the
// metadata with it) expires after windowTombstoneRetention or once
// maxWindowTombstones is exceeded.
func (h *Handler) pruneClosedWindows(now time.Time, liveIDs map[int]bool) {
	h.pruneClosedWindowsFor(now, liveIDs, "")
}

// pruneClosedWindowsFor is pruneClosedWindows for a listing read through
// listedSocket. Window IDs mean something only inside the Kitty instance that
// issued them, so a listing of one instance says nothing about a window whose
// hooks named another: such a window is left for pruneForeignWindows, which
// asks its own instance. An empty listedSocket judges every window, as a
// listing of unknown provenance always has.
func (h *Handler) pruneClosedWindowsFor(now time.Time, liveIDs map[int]bool, listedSocket string) {
	h.stateMu.Lock()
	closed := make(map[int]struct{})
	for id := range h.store {
		// A non-Kitty runtime slot is never in a Kitty listing; its runtime
		// session's end retires it (retireEndedRuntimeSessions).
		if isRuntimeSyntheticSlot(id) {
			continue
		}
		windowID := id
		if key, ok := h.runtimeRoutes.kittyWindow(id); ok {
			windowID = key.windowID
		}
		if !liveIDs[windowID] && !h.windowOfOtherInstanceLocked(id, listedSocket) {
			closed[id] = struct{}{}
		}
	}
	h.dropClosedWindowsLocked(now, closed)
	// A tombstoned ID that Kitty reports live again is a reused window, not a
	// closed one: resurrect it so its diagnostics resume live and it can count
	// as renderer drift again.
	for id := range liveIDs {
		if slot := h.runtimeRoutes.kittyByKey[kittySlotKey{listedSocket, id}]; slot > 0 {
			h.reviveTombstoneLocked(slot)
		} else if key, mapped := h.runtimeRoutes.kittyWindow(id); !mapped || key.socket == listedSocket {
			h.reviveTombstoneLocked(id)
		}
	}
	h.expireTombstonesLocked(now)
	h.stateMu.Unlock()
	h.finishClosedWindows(closed)
}

// windowOfOtherInstanceLocked reports whether windowID's hooks named a Kitty
// socket other than listedSocket. Callers hold stateMu.
func (h *Handler) windowOfOtherInstanceLocked(windowID int, listedSocket string) bool {
	if listedSocket == "" {
		return false
	}
	socket := h.kittySockets[windowID]
	if key, ok := h.runtimeRoutes.kittyWindow(windowID); ok {
		socket = key.socket
	}
	return socket != "" && socket != listedSocket
}

// maxForeignInstanceLists bounds the extra Kitty listings one maintenance pass
// may spend on windows of instances other than the listed one.
const maxForeignInstanceLists = 4

// pruneForeignWindows closes windows of Kitty instances other than the one
// listedSocket names, each judged by a listing of its own instance: a window
// that instance no longer reports is closed, and so is every window of an
// instance whose socket file is gone, since Kitty removes it on exit. A
// listing that fails for any other reason closes nothing, so a slow or busy
// instance never loses its state. The listings run without stateMu.
func (h *Handler) pruneForeignWindows(now time.Time, listedSocket string) {
	if listedSocket == "" {
		return
	}
	h.stateMu.Lock()
	bySocket := make(map[string][]int)
	for id := range h.store {
		if h.windowOfOtherInstanceLocked(id, listedSocket) {
			socket := h.kittySockets[id]
			if key, mapped := h.runtimeRoutes.kittyWindow(id); mapped {
				socket = key.socket
			}
			bySocket[socket] = append(bySocket[socket], id)
		}
	}
	client := h.kittyClient
	h.stateMu.Unlock()
	if len(bySocket) == 0 || client == nil {
		h.setOtherKittyTrees(nil, nil)
		return
	}
	sockets := make([]string, 0, len(bySocket))
	for socket := range bySocket {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)
	if len(sockets) > maxForeignInstanceLists {
		sockets = sockets[:maxForeignInstanceLists]
	}

	candidates := make(map[int]string)
	trees := make(map[string][]kitty.OSWindow)
	failed := make(map[string]bool)
	for _, socket := range sockets {
		if kittySocketGone(socket) {
			for _, id := range bySocket[socket] {
				candidates[id] = socket
			}
			continue
		}
		windows, err := scopedKittyClient(client, socket).ListWindows()
		if err != nil {
			slog.Debug("maintenance: list windows of another kitty instance failed", "socket", socket, "err", err)
			failed[socket] = true
			continue
		}
		trees[socket] = windows
		live := liveWindowIDs(windows)
		for _, id := range bySocket[socket] {
			windowID := id
			h.stateMu.Lock()
			if key, ok := h.runtimeRoutes.kittyWindow(id); ok {
				windowID = key.windowID
			}
			h.stateMu.Unlock()
			if !live[windowID] {
				candidates[id] = socket
			}
		}
	}
	h.setOtherKittyTrees(trees, failed)
	if len(candidates) == 0 {
		return
	}

	h.stateMu.Lock()
	closed := make(map[int]struct{})
	for id, socket := range candidates {
		// A hook that moved the window to another socket while the listing
		// ran has made this verdict about a different window.
		if _, ok := h.store[id]; ok && h.kittySockets[id] == socket {
			closed[id] = struct{}{}
		}
	}
	h.dropClosedWindowsLocked(now, closed)
	h.stateMu.Unlock()
	h.finishClosedWindows(closed)
}

// setOtherKittyTrees replaces the other instances' trees with listed, keeping
// the previous tree of an instance whose listing failed this pass so one busy
// listing does not strip its sessions of their titles. Every other instance's
// tree is dropped. Fresh listings clear unseen attention for focused panes;
// retained trees after failed listings are not new focus observations. The
// companion is told whenever there is a set to show or one to withdraw; the
// hub skips a publish that changed nothing.
func (h *Handler) setOtherKittyTrees(listed map[string][]kitty.OSWindow, failed map[string]bool) {
	h.stateMu.Lock()
	next := make(map[string][]kitty.OSWindow, len(listed))
	observedAt := time.Now()
	for socket, windows := range listed {
		next[socket] = windows
		h.refreshFocusFromSnapshotForLocked(windows, socket, observedAt)
	}
	for socket := range failed {
		if previous, ok := h.otherKittyTrees[socket]; ok {
			next[socket] = previous
		}
	}
	changed := len(next) != 0 || len(h.otherKittyTrees) != 0
	h.otherKittyTrees = next
	if changed {
		h.companionDirty()
	}
	h.stateMu.Unlock()
}

// kittySocketGone reports whether a unix-domain Kitty socket's file no longer
// exists. Abstract sockets and TCP addresses have no file and are never gone.
func kittySocketGone(socket string) bool {
	path, ok := strings.CutPrefix(socket, "unix:")
	if !ok || path == "" || strings.HasPrefix(path, "@") {
		return false
	}
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// dropClosedWindowsLocked removes every closed window's live state. Callers
// hold stateMu and run finishClosedWindows after releasing it.
func (h *Handler) dropClosedWindowsLocked(now time.Time, closed map[int]struct{}) {
	for id := range closed {
		delete(h.store, id)
		delete(h.kittySockets, id)
		delete(h.kittyRestored, id)
		h.runtimeRoutes.releaseKittySlot(id)
		h.recordWindowClosedLocked(id, now)
		// A stop still held for the window has nothing left to paint. Left
		// armed it would commit after the close, bring the window's state
		// back, and notify about a session that is gone: for a runtime slot,
		// with the binding that named it already released.
		if pending, held := h.pendingStopHolds[id]; held {
			if pending.timer != nil {
				pending.timer.Stop()
			}
			delete(h.pendingStopHolds, id)
		}
	}
	// ASL miss streaks and supervision records are per-window leases; a closed
	// window can never corroborate a run again, so both go with it.
	stillOpen := func(windowID int) bool {
		_, gone := closed[windowID]
		return !gone
	}
	h.aslMissStreak.Retain(func(ref asl.LeaseRef) bool { return stillOpen(ref.WindowID) })
	h.aslSupervisors.RetainWindows(stillOpen)
	for runID, slot := range h.aslRuntimeRuns {
		if !stillOpen(slot) {
			delete(h.aslRuntimeRuns, runID)
		}
	}
	for id := range closed {
		// Idle-sample stabilization streaks are per-window: a closed window's
		// pane can produce no further screen evidence, so its streaks (and the
		// evidence generations they track) go with it.
		h.idleSamples.ClearWindow(id)
		// Screen-probe decline streaks have the same property for the same
		// reason: a closed pane will never be classified again, so a streak left
		// behind would be a permanently frozen count that no later frame can ever
		// advance or clear. No sweep beyond this one is needed — unlike
		// aslMissStreak, whose leases can vanish while their window lives, a
		// decline ref is keyed by window and a fixed probe source key, so window
		// closure is the only way an entry can become unreachable.
		h.declines.RetractWindow(id)
	}
}

// finishClosedWindows cancels the work keyed by each closed window that lives
// outside stateMu. Callers have released stateMu.
func (h *Handler) finishClosedWindows(closed map[int]struct{}) {
	for id := range closed {
		// The pending render entry for a closed window is cancelled outright;
		// an in-flight render cannot be cancelled, but its completion
		// bookkeeping already tolerates the deleted state. Persist the pending
		// queued-stage trace only after both stateMu and the queue mutex are free.
		if h.renderQueue != nil {
			if trace, purged := h.renderQueue.Purge(id); purged {
				h.recordRenderGenerationBounded(trace, id)
			}
		}
		// Notifier cooldown/pending entries are keyed by window ID too. Without
		// cleanup the notifier's per-(window, action) cooldown map grows
		// linearly with closed windows, and a stale pending permission timer
		// for a dead window would fire well after the window itself is gone.
		if h.notifier != nil {
			h.notifier.PurgeWindow(id)
		}
		// State-guard entries are keyed by window ID and otherwise only removed
		// by idle/clear/user_clear events. A window closed without firing a
		// terminal hook would leak its guardEntry for the daemon's lifetime.
		if h.guard != nil {
			h.guard.Clear(id)
		}
	}
}

// recordWindowClosedLocked stamps the durable diagnostics for a closed window
// and starts (or refreshes) its bounded tombstone. Callers hold stateMu.
func (h *Handler) recordWindowClosedLocked(windowID int, now time.Time) {
	meta := h.windowMetadata[windowID]
	meta.Watchdog.LastEvent = eventWindowClosed
	meta.Watchdog.LastReason = "window no longer reported by kitty; live state garbage-collected"
	meta.Watchdog.LastAt = now.Unix()
	meta.Watchdog.ClosedAt = now.Unix()
	// The live decline maps are purged for this window in the same critical
	// section, so a retained streak here would be a count the daemon can no
	// longer explain, advance, or clear.
	meta.Watchdog.ScreenDeclines = nil
	h.windowMetadata[windowID] = meta
	if h.windowTombstones == nil {
		h.windowTombstones = make(map[int]time.Time)
	}
	if _, ok := h.windowTombstones[windowID]; !ok {
		h.tombstoneOrder = append(h.tombstoneOrder, windowID)
	}
	h.windowTombstones[windowID] = now
}

// reviveTombstoneLocked clears a window's tombstone when the ID is live again
// (Kitty ID reuse, or an event racing the close re-created state). Callers
// hold stateMu.
func (h *Handler) reviveTombstoneLocked(windowID int) {
	if _, ok := h.windowTombstones[windowID]; !ok {
		return
	}
	delete(h.windowTombstones, windowID)
	for i, id := range h.tombstoneOrder {
		if id == windowID {
			h.tombstoneOrder = append(h.tombstoneOrder[:i:i], h.tombstoneOrder[i+1:]...)
			break
		}
	}
	meta := h.windowMetadata[windowID]
	meta.Watchdog.ClosedAt = 0
	h.windowMetadata[windowID] = meta
}

// expireTombstonesLocked enforces the tombstone bound: entries older than
// windowTombstoneRetention, or beyond maxWindowTombstones (oldest first), are
// dropped together with their durable metadata. Callers hold stateMu.
func (h *Handler) expireTombstonesLocked(now time.Time) {
	keep := h.tombstoneOrder[:0]
	for _, id := range h.tombstoneOrder {
		closedAt, ok := h.windowTombstones[id]
		if !ok {
			continue
		}
		if now.Sub(closedAt) >= windowTombstoneRetention {
			delete(h.windowTombstones, id)
			delete(h.windowMetadata, id)
			continue
		}
		keep = append(keep, id)
	}
	h.tombstoneOrder = keep
	for len(h.tombstoneOrder) > maxWindowTombstones {
		id := h.tombstoneOrder[0]
		h.tombstoneOrder = h.tombstoneOrder[1:]
		delete(h.windowTombstones, id)
		delete(h.windowMetadata, id)
	}
}
