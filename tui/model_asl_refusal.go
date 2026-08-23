package tui

// This file is the whole of what the Runs view is allowed to say about a
// refused action. Availability is read from the daemon capability block and
// ASL's own per-row projection, and every refusal is mapped from a stable code
// to a fixed sentence, so no bridge-authored text can reach the screen.

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// aslTargetChangedText is shown when the bridge refuses because the run moved
// under a modal. The action is not retried against a fresh token.
const aslTargetChangedText = "this run changed since it was selected: the form was closed and the list refreshed"

// aslRowPermits reports whether ASL's own projection lists a method as
// permitted on this row. Permission is never inferred from the state string.
func aslRowPermits(row aslbridge.Row, method string) bool {
	for _, permitted := range row.Actions {
		if permitted == method {
			return true
		}
	}
	return false
}

// aslActionAllowed decides whether one bridge method may be invoked against a
// row right now, and explains the refusal when it may not. Availability comes
// only from the daemon capability block and the row's projected metadata.
func aslActionAllowed(m Model, row aslbridge.Row, method string) (bool, string) {
	switch aslView(m) {
	case aslViewLive:
	case aslViewStale:
		return false, "actions are unavailable: this snapshot is read-only"
	default:
		return false, "actions are unavailable: agent-session-loop is not reachable"
	}
	if !m.aslStatus.MutationsEnabled {
		return false, "actions are unavailable: the daemon reports agent-session-loop mutations as disabled"
	}
	if strings.TrimSpace(row.ActionToken) == "" {
		return false, "actions are unavailable: this run carries no action token"
	}
	if aslRowPermits(row, method) {
		return true, ""
	}
	label := aslMethodLabel(method)
	if reason := strings.TrimSpace(row.DisabledReasons[method]); reason != "" {
		return false, label + " is unavailable: " + reason
	}
	return false, label + " is unavailable for this run"
}

func aslMethodLabel(method string) string {
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

// aslStaleCodes are the refusals that mean the run moved: the token, the
// question, or the run itself no longer matches what the operator reviewed.
var aslStaleCodes = map[string]bool{
	"stale-action-token":   true,
	"action-not-permitted": true,
	"no-active-run":        true,
	"no-open-question":     true,
	"question-mismatch":    true,
	"run-mismatch":         true,
	"not-paused":           true,
}

var aslErrorCodes = map[string]bool{
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
}

// aslErrorCode recovers the bridge refusal code from a daemon error. The daemon
// flattens a typed bridge error to "<code>: <message>", so the code is the only
// machine-readable part that survives the IPC boundary.
func aslErrorCode(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	code := text
	if idx := strings.Index(text, ": "); idx > 0 {
		code = text[:idx]
	}
	if aslStaleCodes[code] || aslErrorCodes[code] {
		return code
	}
	return ""
}

func aslStaleTarget(err error) bool {
	return aslStaleCodes[aslErrorCode(err)]
}

// aslRefusalText maps a refusal to a fixed operator-facing sentence. The
// bridge's own message is deliberately dropped: mapping by code is what makes
// it structurally impossible for question or answer text to reach an error
// string, a status line, or a toast.
func aslRefusalText(err error) string {
	switch aslErrorCode(err) {
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
	case "malformed-json", "frame-too-large", "internal-error":
		return "agent-session-loop reported an internal error"
	default:
		return "the action did not complete; its outcome is unknown and it was not retried"
	}
}
