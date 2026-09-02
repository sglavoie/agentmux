package events

// The agent-session-loop hook event vocabulary.
//
// These are wire names: ASL passes the event name beside the payload on every
// lifecycle delivery, so the strings below are the ingestion vocabulary the
// daemon matches against before anything is parsed. They live here rather than
// beside the loop-lifecycle orchestration because normalization, the
// compatibility gate, and the routing paths all switch on them, and a second
// copy of any of these names would be a silent ingestion gap rather than a
// compile error.

const (
	AgentSessionLoopStarted               = "AgentSessionLoopStarted"
	AgentSessionLoopTurnStarted           = "AgentSessionLoopTurnStarted"
	AgentSessionLoopTurnHeartbeat         = "AgentSessionLoopTurnHeartbeat"
	AgentSessionLoopTurnHangSuspected     = "AgentSessionLoopTurnHangSuspected"
	AgentSessionLoopTurnHangCleared       = "AgentSessionLoopTurnHangCleared"
	AgentSessionLoopTurnFinished          = "AgentSessionLoopTurnFinished"
	AgentSessionLoopVerificationStarted   = "AgentSessionLoopVerificationStarted"
	AgentSessionLoopVerificationHeartbeat = "AgentSessionLoopVerificationHeartbeat"
	AgentSessionLoopVerificationFinished  = "AgentSessionLoopVerificationFinished"
	AgentSessionLoopWaitStarted           = "AgentSessionLoopWaitStarted"
	AgentSessionLoopWaitEnded             = "AgentSessionLoopWaitEnded"
	AgentSessionLoopPaused                = "AgentSessionLoopPaused"
	AgentSessionLoopFinished              = "AgentSessionLoopFinished"
)

// The mailbox boundaries. They are lifecycle payloads under the same schema —
// so the compatibility gate covers them — but they are emitted by the ASL CLI
// rather than by a running loop, so they are deliberately not loop-marker
// events.
const (
	AgentSessionLoopMailboxQuestion   = "AgentSessionLoopMailboxQuestion"
	AgentSessionLoopMailboxReply      = "AgentSessionLoopMailboxReply"
	AgentSessionLoopMailboxResolution = "AgentSessionLoopMailboxResolution"
)

// IsAgentSessionLoopEvent reports whether the event is one of the thirteen
// boundaries a running loop emits, which are the ones that carry the loop
// marker. It is deliberately narrower than IsAgentSessionLoopHookEvent.
func IsAgentSessionLoopEvent(eventOrAction string) bool {
	switch eventOrAction {
	case AgentSessionLoopStarted, AgentSessionLoopTurnStarted,
		AgentSessionLoopTurnHeartbeat, AgentSessionLoopTurnHangSuspected,
		AgentSessionLoopTurnHangCleared, AgentSessionLoopTurnFinished,
		AgentSessionLoopVerificationStarted, AgentSessionLoopVerificationHeartbeat,
		AgentSessionLoopVerificationFinished, AgentSessionLoopWaitStarted,
		AgentSessionLoopWaitEnded, AgentSessionLoopPaused,
		AgentSessionLoopFinished:
		return true
	default:
		return false
	}
}
