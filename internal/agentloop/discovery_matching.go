package agentloop

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// heartbeatTimestamp picks the freshest liveness timestamp the run persists:
// the active turn's per-heartbeat stamp, else the verification's per-heartbeat
// stamp (persisted by newer loops), else the verification's current check
// start, else the verification start.
func heartbeatTimestamp(state stateFile) string {
	if state.ActiveTurn != nil && strings.TrimSpace(state.ActiveTurn.LastHeartbeatAt) != "" {
		return state.ActiveTurn.LastHeartbeatAt
	}
	if state.ActiveVerification != nil {
		if raw := strings.TrimSpace(state.ActiveVerification.LastHeartbeatAt); raw != "" {
			return raw
		}
		if raw := strings.TrimSpace(state.ActiveVerification.CurrentCheckStartedAt); raw != "" {
			return raw
		}
		return strings.TrimSpace(state.ActiveVerification.StartedAt)
	}
	return ""
}

func matchingWindow(candidates []Candidate, windowID int) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.WindowID <= 0 {
			continue
		}
		if candidate.WindowID == windowID {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func candidatesEligibleForWindowFallback(candidates []Candidate, managed map[int]struct{}) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.WindowID <= 0 {
			matches = append(matches, candidate)
			continue
		}
		if _, isManaged := managed[candidate.WindowID]; !isManaged {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func managedWindowIDs(openTargets []WorkspaceTarget, targetWindowID int) map[int]struct{} {
	managed := make(map[int]struct{}, len(openTargets)+1)
	for _, target := range openTargets {
		if target.WindowID > 0 {
			managed[target.WindowID] = struct{}{}
		}
	}
	if targetWindowID > 0 {
		managed[targetWindowID] = struct{}{}
	}
	return managed
}

func matchingCanonicalCWD(candidates []Candidate, selectedCanonical string) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.CWD == "" {
			continue
		}
		cwd, err := CanonicalizePath(candidate.CWD)
		if err != nil {
			continue
		}
		if cwd == selectedCanonical {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func filterHarness(candidates []Candidate, targetHarness string) []Candidate {
	matches := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Harness == targetHarness {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func workspaceHasOpenCandidateHarness(candidates []Candidate, selectedCanonical string, openTargets []WorkspaceTarget) bool {
	openHarnesses := make(map[string]struct{})
	for _, target := range openTargets {
		if target.CWD == "" {
			continue
		}
		cwd, err := CanonicalizePath(target.CWD)
		if err != nil || cwd != selectedCanonical {
			continue
		}
		targetHarness := normalizeKnownHarness(target.Harness)
		if targetHarness != "" {
			openHarnesses[targetHarness] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if candidate.Harness == "" {
			continue
		}
		if _, ok := openHarnesses[candidate.Harness]; ok {
			return true
		}
	}
	return false
}

func effectiveHarness(state stateFile) string {
	if state.ActiveTurn != nil {
		if h := normalizeKnownHarness(state.ActiveTurn.Harness); h != "" {
			return h
		}
	}
	return normalizeKnownHarness(state.Harness)
}

func normalizeKnownHarness(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case harness.Codex:
		return harness.Codex
	case harness.Copilot:
		return harness.Copilot
	case harness.Claude:
		return harness.Claude
	default:
		return ""
	}
}
