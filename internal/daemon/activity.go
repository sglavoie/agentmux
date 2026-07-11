package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/harness"
)

// newActivityBootID returns an opaque process-lifetime identifier used only
// when a legacy delivery lacks the hook request ID. Random generation failure
// cannot make the hook path fail, so the timestamp fallback still namespaces
// receipt sequences across daemon processes.
func newActivityBootID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err == nil {
		return hex.EncodeToString(data[:])
	}
	return fmt.Sprintf("boot-%d", time.Now().UnixNano())
}

// meaningfulActivity returns a compact, prompt-free journal item only for the
// reducer outcomes the workspace model can use. Normal active/tool churn,
// subagent bookkeeping, clears, suppressions, duplicates, and raw malformed
// hooks deliberately produce no work-journal record.
func meaningfulActivity(
	n NormalizedEvent,
	req Request,
	decision ReducerDecision,
	currentHarness, currentSessionID string,
	bootID string,
	receiptSequence uint64,
	now time.Time,
) (activity.Item, bool) {
	if decision != DecisionApplied {
		return activity.Item{}, false
	}

	kind, summary, ok := classifyMeaningfulActivity(n, req)
	if !ok {
		return activity.Item{}, false
	}
	harnessName := strings.TrimSpace(currentHarness)
	sessionID := strings.TrimSpace(currentSessionID)
	if harnessName == "" {
		harnessName = strings.TrimSpace(req.Harness)
	}
	if harnessName != "" {
		harnessName = harness.Normalize(harnessName)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(n.SessionID)
	}

	metadata := map[string]string{
		"action":      n.Action,
		"event":       n.Event,
		"source_kind": string(n.Source.Kind),
	}
	if n.Phase != "" {
		metadata["phase"] = n.Phase
	}
	if profile := strings.TrimSpace(req.Account); profile != "" {
		metadata["profile"] = profile
	}
	if source := strings.TrimSpace(req.Source); source != "" {
		metadata["session_source"] = source
	}
	metadataJSON, _ := json.Marshal(metadata)
	occurredAt := now
	if n.At > 0 {
		occurredAt = time.Unix(n.At, 0)
	}
	dedupeKey := "request:" + strings.TrimSpace(req.RequestID)
	if strings.TrimSpace(req.RequestID) == "" {
		dedupeKey = "receipt:" + bootID + ":" + strconv.FormatUint(receiptSequence, 10)
	}
	return activity.Item{
		DedupeKey:    dedupeKey,
		Harness:      harnessName,
		SessionID:    sessionID,
		WindowID:     req.WindowID,
		Kind:         kind,
		Summary:      summary,
		MetadataJSON: string(metadataJSON),
		OccurredAt:   occurredAt,
	}, true
}

func classifyMeaningfulActivity(n NormalizedEvent, req Request) (kind, summary string, ok bool) {
	// A session-start source differentiates fresh, continued, forked, and
	// handoff-created conversations without storing source prompt text.
	if n.Event == "SessionStart" || n.Event == "sessionStart" {
		switch strings.ToLower(strings.TrimSpace(req.Source)) {
		case "resume", "resumed", "continue", "continued":
			return "resume", "Session resumed", true
		case "fork", "forked", "branch":
			return "fork", "Session forked", true
		case "handoff", "handed_off":
			return "handoff", "Session started from handoff", true
		default:
			return "session_start", "Session started", true
		}
	}
	if n.Prompt {
		return "prompt", "Prompt submitted", true
	}
	if n.Phase != "" && meaningfulLoopPhase(n.Event) {
		return "loop_" + n.Phase, loopPhaseSummary(n.Phase), true
	}
	switch n.Action {
	case "permission":
		return "attention_permission", "Permission requested", true
	case "elicitation", "question":
		return "attention_input", "Input requested", true
	case "failure":
		return "failure", "Recoverable failure", true
	case "stop_failure":
		return "terminal_failure", "Session failed", true
	case "stop":
		return "completion", "Session completed", true
	}
	return "", "", false
}

func meaningfulLoopPhase(event string) bool {
	switch event {
	case agentSessionLoopTurnHeartbeat, agentSessionLoopVerificationHeartbeat:
		return false
	default:
		return true
	}
}

func loopPhaseSummary(phase string) string {
	switch phase {
	case ASLPhaseTurn:
		return "Loop turn started"
	case ASLPhaseVerification:
		return "Loop verification started"
	case ASLPhasePaused:
		return "Loop paused for attention"
	case ASLPhaseFinished:
		return "Loop finished"
	default:
		return "Loop phase changed"
	}
}
