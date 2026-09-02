package asl

// This file is the whole of what the Runs view is allowed to say about a
// refused action. Availability is read from the daemon capability block and
// ASL's own per-row projection, and every refusal is mapped from a stable code
// to a fixed sentence, so no bridge-authored text can reach the screen.

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// TargetChangedText is shown when the bridge refuses because the run moved
// under a modal. The action is not retried against a fresh token.
const TargetChangedText = "this run changed since it was selected: the form was closed and the list refreshed"

// RowPermits reports whether ASL's own projection lists a method as permitted
// on this row. Permission is never inferred from the state string.
func RowPermits(row aslbridge.Row, method string) bool {
	for _, permitted := range row.Actions {
		if permitted == method {
			return true
		}
	}
	return false
}

// ActionAllowed decides whether one bridge method may be invoked against a row
// right now, and explains the refusal when it may not. Availability comes only
// from the daemon capability block and the row's projected metadata.
func (in RunsInput) ActionAllowed(row aslbridge.Row, method string) (bool, string) {
	switch in.View() {
	case ViewLive:
	case ViewStale:
		return false, "actions are unavailable: this snapshot is read-only"
	default:
		return false, "actions are unavailable: agent-session-loop is not reachable"
	}
	if !in.Status.MutationsEnabled {
		return false, "actions are unavailable: the daemon reports agent-session-loop mutations as disabled"
	}
	if strings.TrimSpace(row.ActionToken) == "" {
		return false, "actions are unavailable: this run carries no action token"
	}
	if RowPermits(row, method) {
		return true, ""
	}
	label := MethodLabel(method)
	if reason := strings.TrimSpace(row.DisabledReasons[method]); reason != "" {
		return false, label + " is unavailable: " + reason
	}
	return false, label + " is unavailable for this run"
}

// MethodLabel names a bridge method the way an operator would.
func MethodLabel(method string) string {
	switch method {
	case aslbridge.MethodQuestionReply:
		return "reply"
	case aslbridge.MethodQuestionResolve:
		return "closing the question"
	case aslbridge.MethodRunResume:
		return "resume"
	case aslbridge.MethodRunStopAfterCurrent:
		return "stop after current"
	case aslbridge.MethodRunAbort:
		return "abort"
	default:
		return method
	}
}

// StaleCodes are the refusals that mean the run moved: the token, the question,
// or the run itself no longer matches what the operator reviewed.
var StaleCodes = map[string]bool{
	"stale-action-token":   true,
	"action-not-permitted": true,
	"no-active-run":        true,
	"no-open-question":     true,
	"question-mismatch":    true,
	"run-mismatch":         true,
	"not-paused":           true,
	// The projection the operator acted on has been superseded. That is the same
	// situation as a stale token seen one level up — the target moved, nothing was
	// applied — so it closes the form and refreshes rather than reporting a fault.
	"stale-snapshot": true,
}

// ErrorCodes are the refusals that report a fault rather than a moved target.
var ErrorCodes = map[string]bool{
	"invalid-plan":            true,
	"invalid-action":          true,
	"empty-text":              true,
	"question-body-too-large": true,
	"missing-question-body":   true,
	"malformed-json":          true,
	"invalid-request":         true,
	"unsupported-protocol":    true,
	"unknown-method":          true,
	"frame-too-large":         true,
	"internal-error":          true,
	"mutation-failed":         true,
	"workbench-unavailable":   true,
	// Not stale: a refresh cannot fix it. The two sides were built from different
	// contract sets, so the same pairing reproduces this forever and the operator
	// needs the sentence to point at the install rather than at the run.
	"contract-set-mismatch": true,
}

// ErrorCode recovers the bridge refusal code from a daemon error. The daemon
// flattens a typed bridge error to "<code>: <message>", so the code is the only
// machine-readable part that survives the IPC boundary.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	code := text
	if idx := strings.Index(text, ": "); idx > 0 {
		code = text[:idx]
	}
	if StaleCodes[code] || ErrorCodes[code] {
		return code
	}
	return ""
}

// StaleTarget reports whether a refusal means the run moved under the operator.
func StaleTarget(err error) bool {
	return StaleCodes[ErrorCode(err)]
}

// RefusalText maps a refusal to a fixed operator-facing sentence. The bridge's
// own message is deliberately dropped: mapping by code is what makes it
// structurally impossible for question or answer text to reach an error string,
// a status line, or a toast.
func RefusalText(err error) string {
	switch ErrorCode(err) {
	case "empty-text":
		return "the answer is empty: type a reply before submitting"
	case "question-body-too-large":
		return "this question is too large to show here: open the Workbench with w"
	case "missing-question-body":
		return "this question carries no readable text: open the Workbench with w"
	case "invalid-plan":
		return "agent-session-loop no longer recognizes this plan directory"
	case "mutation-failed":
		return "agent-session-loop could not record the action; nothing was applied"
	case "workbench-unavailable":
		return "the Workbench is unavailable in this bridge session"
	case "invalid-request", "invalid-action":
		return "the daemon refused the request as malformed"
	case "unknown-method", "unsupported-protocol":
		return "this agent-session-loop build does not support this action"
	case "contract-set-mismatch":
		// No identifier is quoted. Both are build constants an operator cannot act
		// on, and one of them is the peer's, which makes it text this side did not
		// author. The daemon log carries both.
		return "agent mux and agent-session-loop were built from different contract sets: update the older one"
	case "malformed-json", "frame-too-large", "internal-error":
		return "agent-session-loop reported an internal error"
	default:
		return "the action did not complete; its outcome is unknown and it was not retried"
	}
}
