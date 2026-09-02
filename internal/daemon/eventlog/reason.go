package eventlog

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// StableReducerReason maps the reducer's human-readable trace prose onto the
// stable reason vocabulary a row carries. It lives with the log rather than
// with the reducer because the prose is a debugging aid the reducer is free to
// reword, while the value returned here is persisted onto every event row and
// into every journal record and must therefore stay pinned.
//
// An unmapped reason is passed through trimmed, so a newly worded reducer
// decision degrades to its own prose rather than to an empty reason.
func StableReducerReason(trace state.ReducerTrace) string {
	if trace.Reason == "" {
		return string(trace.Decision)
	}
	switch trace.Reason {
	case "duplicate sequence":
		return protocol.DecisionReasonDuplicateSequence
	case "older sequence arrived after a newer one":
		return protocol.DecisionReasonReorderedSequence
	case "idle preserves attention/terminal icon":
		return protocol.DecisionReasonIdlePreservesTerminal
	case "same identified turn after terminal":
		return protocol.DecisionReasonTerminalTurnSuppression
	case "stopped subagent never restarts", "same-source same-tool terminal suppression", "same-source same-turn terminal suppression":
		return protocol.DecisionReasonTerminalTurnSuppression
	case "legacy terminal grace window":
		return protocol.DecisionReasonTerminalGraceSuppression
	case "terminal trailing-event grace":
		return protocol.DecisionReasonTerminalGraceSuppression
	case "user acknowledged visible facts; ASL leases preserved":
		return protocol.DecisionReasonUserAcknowledged
	case "session end cleared its remaining subagent facts":
		return protocol.DecisionReasonSessionAgentsCleared
	default:
		return strings.TrimSpace(trace.Reason)
	}
}
