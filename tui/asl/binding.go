package asl

// This file answers "which live session is this run"; the supervision display
// and the focus action both rest on it. Exactly one relation counts as
// ownership: a binding ASL itself projected. Everything looser is a guess and
// is offered as a candidate for the operator to choose between, never resolved
// silently.

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/session"
)

// ExactBinding reports the live session ASL has positively identified for a
// row: either the projected Kitty window, or a session carrying the same run ID
// in its managed user variables. Anything looser is a guess and is left to the
// fallback path.
func ExactBinding(row aslbridge.Row, sessions []session.RunningSession) (session.RunningSession, bool) {
	for _, candidate := range sessions {
		if row.Window != nil && row.Window.KittyWindowID != 0 && candidate.WindowID == row.Window.KittyWindowID {
			return candidate, true
		}
		if row.RunID != "" && candidate.ASLRunID == row.RunID {
			return candidate, true
		}
	}
	return session.RunningSession{}, false
}

// FallbackCandidates returns live sessions in the run's canonical workspace
// running the run's harness. Both must match: a workspace alone can host
// several harnesses, and a harness alone says nothing about which run it serves.
func FallbackCandidates(row aslbridge.Row, sessions []session.RunningSession) []actions.WorktreeLiveSession {
	root := CanonicalDir(row.WorkspaceRoot)
	harness := strings.TrimSpace(strings.ToLower(row.Harness))
	if root == "" || harness == "" {
		return nil
	}
	var out []actions.WorktreeLiveSession
	for _, candidate := range sessions {
		if CanonicalDir(candidate.Directory) != root {
			continue
		}
		if strings.ToLower(strings.TrimSpace(candidate.Harness)) != harness {
			continue
		}
		out = append(out, actions.WorktreeLiveSession{
			ID:               candidate.HarnessSessionID,
			TabID:            candidate.TabID,
			WindowID:         candidate.WindowID,
			Project:          candidate.Project,
			Profile:          candidate.Profile,
			Harness:          candidate.Harness,
			HarnessSessionID: candidate.HarnessSessionID,
			Directory:        candidate.Directory,
		})
	}
	return out
}

// MarkerFor returns the dashboard marker for a session an ASL run is exactly
// bound to. It reports false for every session ASL has not positively
// identified, and for any snapshot the view already considers untrustworthy.
func (in RunsInput) MarkerFor(s session.RunningSession) (string, bool) {
	if len(in.Rows) == 0 {
		return "", false
	}
	switch in.View() {
	case ViewLive, ViewStale:
	default:
		return "", false
	}
	for _, row := range in.Rows {
		if row.Window != nil && row.Window.KittyWindowID != 0 && row.Window.KittyWindowID == s.WindowID {
			return StateMarker(row.PrimaryState), true
		}
		if row.RunID != "" && row.RunID == s.ASLRunID {
			return StateMarker(row.PrimaryState), true
		}
	}
	return "", false
}

// CanonicalDir resolves a workspace path to the form two sides can be compared
// in, so a symlinked checkout and its target are recognized as one workspace.
func CanonicalDir(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// WorkbenchFocus maps a run state to the Workbench section an operator needs
// next for that state.
func WorkbenchFocus(state string) string {
	switch state {
	case "paused-for-reply":
		return aslbridge.FocusReply
	case "verifying":
		return aslbridge.FocusVerification
	case "failed", "stale", "invalid":
		return aslbridge.FocusEvents
	default:
		return aslbridge.FocusActive
	}
}
