package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

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
	var windows []kitty.OSWindow
	// Prune eventLog entries for windows that Kitty no longer reports as live.
	// Runs before the staleness scan so an event-log GC happens every sweep
	// regardless of whether anyStaleThresholdSet() is true below.
	if h.kittyClient != nil {
		if snapshot, err := h.kittyClient.ListWindows(); err == nil {
			windows = snapshot
			h.pruneClosedWindows(now, liveWindowIDs(windows))
		} else {
			slog.Debug("sweep: list windows failed for GC prune", "err", err)
		}
	}
	changed := h.SweepStaleWithSnapshot(now, windows)
	if changed && len(windows) > 0 {
		h.renderSnapshot(windows)
	}
}

// SweepStaleWithSnapshot runs one staleness pass without advancing the
// monotonic idle accumulation (credit 0): entries not yet tracked are seeded
// from their wall-clock age, so a one-shot call over pre-aged state expires it
// immediately. The production watchdog loop calls sweepStale directly with the
// tick's observed elapsed credit instead.
func (h *Handler) SweepStaleWithSnapshot(now time.Time, windows []kitty.OSWindow) bool {
	return h.sweepStale(now, windows, 0, false)
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
func (h *Handler) sweepStale(now time.Time, _ []kitty.OSWindow, credit time.Duration, gapTick bool) bool {
	staleThresholdsEnabled := h.anyStaleThresholdSet()
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
		expired, aslExecuting := 0, false
		if staleThresholdsEnabled {
			expired, aslExecuting = h.expireStaleFactsLocked(windowID, st, now, credit, gapTick)
		}

		action := st.LastAction
		windowStale := false
		if staleThresholdsEnabled && (action == "active" || action == "subagent" || action == "compact") {
			if threshold := h.resolveThreshold(account, action); threshold > 0 {
				idle := advanceWindowIdle(st, now, credit, gapTick)
				// An authoritative ASL turn or verification lease is live
				// evidence the loop is still working this window; the bolt is
				// not cleared out from under it.
				windowStale = idle >= threshold && (action != "active" || !aslExecuting)
			}
		} else if staleThresholdsEnabled {
			st.watchdogSeeded = false
			st.watchdogIdle = 0
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
			st.CopilotToolPermissionState = copilotToolPermissionNone
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
				"no activity for "+st.watchdogIdle.Truncate(time.Second).String()+
					" (threshold "+h.resolveThreshold(account, action).String()+")")
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

// staleActionForIcon maps an active-class icon back to the threshold action
// name used by [defaults.watchdog]. Non-active-class icons return "" — they
// are never expired by the watchdog.
func staleActionForIcon(icon string) string {
	switch icon {
	case "bolt":
		return "active"
	case "subagent":
		return "subagent"
	case "compact":
		return "compact"
	}
	return ""
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
func (h *Handler) expireStaleFactsLocked(windowID int, st *WindowState, now time.Time, credit time.Duration, gapTick bool) (expired int, aslExecuting bool) {
	if st.Reducer == nil {
		return 0, false
	}
	type pendingExpiry struct {
		fact      SourceFactSnapshot
		threshold time.Duration
		idle      time.Duration
	}
	var expiries []pendingExpiry
	seen := make(map[SourceID]bool)
	for _, fact := range st.Reducer.Facts() {
		src := fact.Source()
		action := staleActionForIcon(fact.Icon)
		if action == "" || fact.Acked {
			continue
		}
		seen[src] = true
		threshold := h.resolveThreshold(st.LastAccount, action)
		idle := advanceFactIdle(st, fact, now, credit, gapTick)
		factStale := threshold > 0 && idle >= threshold
		if fact.Kind == SourceASL && fact.LeaseLive && !factStale &&
			(fact.Phase == ASLPhaseTurn || fact.Phase == ASLPhaseVerification) {
			aslExecuting = true
		}
		if factStale {
			expiries = append(expiries, pendingExpiry{fact: fact, threshold: threshold, idle: idle})
		}
	}
	// Accumulators for facts that no longer contribute an active-class icon
	// (retired, acked, or advanced to another state) are dropped so the map
	// cannot grow with dead sources.
	for src := range st.watchdogFactIdle {
		if !seen[src] {
			delete(st.watchdogFactIdle, src)
		}
	}
	for _, p := range expiries {
		if aslExecuting && p.fact.Icon == "bolt" && p.fact.Kind != SourceASL {
			continue
		}
		if !st.Reducer.ExpireFact(p.fact.Source(), p.fact.Seq, now.Unix()) {
			continue
		}
		delete(st.watchdogFactIdle, p.fact.Source())
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
