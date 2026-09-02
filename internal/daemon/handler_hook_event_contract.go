package daemon

// The lifecycle-hook contract gate.
//
// Agent Mux and ASL are versioned together by one contract-set ID, and a
// lifecycle hook is the one place the two meet without a connection to
// negotiate over: the payload arrives on stdin from a process the daemon never
// spawned and cannot interrogate. So it carries the compatibility block itself,
// and this is where that block is believed or the event is quarantined.
//
// There is no schema-1 path left. Tolerating one would mean guessing which
// fields an older producer meant, and the fields in question — the phase, the
// run identity, the mailbox generation — are exactly the ones the reducer turns
// into durable state and the dashboard renders. A payload this build cannot
// place is quarantined rather than half-read.
//
// The gate is scoped to the sixteen lifecycle boundaries. Harness hooks
// (Claude, Copilot, Codex) carry no contract block, are not asked to, and are
// unaffected: refusing an ASL event never costs Agent Mux its core function,
// which is the same rule the bridge follows.

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// aslHookSchemaVersion is the only lifecycle-hook schema this build ingests.
const aslHookSchemaVersion = 2

// aslHookSource is the producer name a lifecycle payload declares. The schema
// pins it as a const, so it is the field that says "this payload is from ASL"
// independently of which boundary it names.
const aslHookSource = "agent_session_loop"

// Compatibility refusals. They are daemon-authored constants, so they are
// stable across producers and carry nothing the payload chose: the version and
// identity a hook offered are private diagnostics, because a hook is free to
// put a plan path in either of them.
const (
	DecisionReasonHookSchemaUnsupported = "hook_schema_unsupported"
	DecisionReasonHookContractMismatch  = "hook_contract_set_mismatch"
)

// aslLifecycleHookEvents is the closed lifecycle vocabulary, and the only set of
// events the gate applies to. It is asserted against the vendored schema's own
// EventName enum in the tests rather than restated from memory, so it cannot
// drift from what ASL publishes.
var aslLifecycleHookEvents = map[string]bool{
	agentSessionLoopStarted:               true,
	agentSessionLoopTurnStarted:           true,
	agentSessionLoopTurnHeartbeat:         true,
	agentSessionLoopTurnFinished:          true,
	agentSessionLoopTurnHangSuspected:     true,
	agentSessionLoopTurnHangCleared:       true,
	agentSessionLoopVerificationStarted:   true,
	agentSessionLoopVerificationHeartbeat: true,
	agentSessionLoopVerificationFinished:  true,
	agentSessionLoopWaitStarted:           true,
	agentSessionLoopWaitEnded:             true,
	agentSessionLoopMailboxQuestion:       true,
	agentSessionLoopMailboxReply:          true,
	agentSessionLoopMailboxResolution:     true,
	agentSessionLoopPaused:                true,
	agentSessionLoopFinished:              true,
}

// isAgentSessionLoopHookEvent reports whether event is a lifecycle boundary and
// so subject to the contract gate. It is deliberately wider than
// isAgentSessionLoopEvent, which names only the thirteen boundaries that carry
// the loop marker: the three mailbox boundaries are lifecycle payloads under the
// same schema even though they are emitted by the CLI rather than the loop.
func isAgentSessionLoopHookEvent(event string) bool {
	return aslLifecycleHookEvents[strings.TrimSpace(event)]
}

// aslHookContractRefusal returns the stable reason a lifecycle payload cannot be
// ingested, or "" when it may be. The refusal is non-retryable by construction:
// nothing about resending the same payload changes either answer, so the caller
// quarantines rather than deferring.
func aslHookContractRefusal(event string, payload hookevent.HookPayload) string {
	if !isAgentSessionLoopHookEvent(event) {
		return ""
	}
	switch {
	case payload.SchemaVersion != aslHookSchemaVersion:
		return DecisionReasonHookSchemaUnsupported
	case payload.ContractSetID != aslcontract.ContractSetID:
		return DecisionReasonHookContractMismatch
	default:
		return ""
	}
}

// isQuarantineReason reports whether a suppression reason means the payload was
// refused as unusable rather than merely not acted on. A quarantined event is
// recorded with an invalid identity, because the daemon is explicitly declining
// to believe the identity fields it carried.
func isQuarantineReason(reason string) bool {
	switch reason {
	case protocol.DecisionReasonInvalidPayload,
		DecisionReasonHookSchemaUnsupported,
		DecisionReasonHookContractMismatch:
		return true
	default:
		return false
	}
}
