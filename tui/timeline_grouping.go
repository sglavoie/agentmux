package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// groupIntoPhases walks events (oldest-first) and groups them into timeline phases.
func groupIntoPhases(events []protocol.SessionEvent) []timelinePhase {
	if len(events) == 0 {
		return []timelinePhase{{kind: "preamble"}}
	}

	var phases []timelinePhase
	var cur *timelinePhase
	var lastEnd time.Time // end time of the last appended phase

	maybeGap := func(at time.Time) {
		if !lastEnd.IsZero() && at.Sub(lastEnd) >= 60*time.Second {
			phases = append(phases, timelinePhase{kind: "gap", startAt: lastEnd, endAt: at})
			lastEnd = time.Time{}
		}
	}

	closeCur := func(at time.Time) {
		if cur != nil {
			cur.endAt = at
			phases = append(phases, *cur)
			lastEnd = at
			cur = nil
		}
	}

	ensureCur := func(kind string, at time.Time) {
		if cur == nil {
			maybeGap(at)
			cur = &timelinePhase{kind: kind, startAt: at}
		}
	}

	for i := range events {
		ev := events[i]
		at := time.Unix(ev.At, 0)

		if ev.ResetFlow {
			closeCur(at)
			phases = append(phases, timelinePhase{kind: "separator", startAt: at, endAt: at})
			lastEnd = time.Time{} // separator fills the visual gap; reset so no gap follows it
		}

		switch ev.Action {
		case "active", "subagent", "compact":
			if cur == nil || cur.kind != ev.Action {
				closeCur(at)
				maybeGap(at)
				cur = &timelinePhase{kind: ev.Action, startAt: at}
			}
		case "stop", "stop_failure", "failure", "permission", "elicitation":
			ensureCur("preamble", at)
			evCopy := ev
			cur.termEvent = &evCopy
			cur.contextLimit = ev.ContextLimit
			cur.events = append(cur.events, ev)
			closeCur(at)
			continue
		case "idle", "clear", "user_clear":
			ensureCur("preamble", at)
			cur.events = append(cur.events, ev)
			closeCur(at)
			continue
		default:
			ensureCur("preamble", at)
		}

		if cur != nil {
			cur.events = append(cur.events, ev)
		}
	}

	if cur != nil {
		phases = append(phases, *cur)
	}

	if len(phases) == 0 {
		return []timelinePhase{{kind: "preamble"}}
	}

	return phases
}
