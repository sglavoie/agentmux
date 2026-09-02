package asl

// This file owns one run row's presentation: the columns a wide terminal shows
// inline, the triage-only shape a narrow one falls back to, and the small field
// projections both are built from. Nothing here reads a question body or an
// answer; a row carries identity, state, and ASL's own reason for listing it.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/layout"
	tuiworktrees "github.com/sglavoie/agentmux/tui/worktrees"
)

// FormatRow renders one run. A wide row carries session/phase, harness, and
// workspace inline; a narrow one keeps state, label, age, and the reason the
// row is listed, and leaves the rest to the detail panel.
func FormatRow(row aslbridge.Row, width int, wide bool, now time.Time) string {
	if width <= 0 {
		width = 120
	}
	state := stateText(row)
	label := RowLabel(row)
	age := tuiworktrees.CompactAge(ActivityTime(row), now)
	var line string
	if wide {
		line = fmt.Sprintf("  %s  %s  %s  %s  %5s  %s",
			cell(state, 18), cell(label, 24), cell(sessionPhase(row), 22),
			cell(fieldOr(row.Harness, "?"), 8), age, displayPath(row.WorkspaceRoot))
	} else {
		line = fmt.Sprintf("  %s  %s  %5s  %s", cell(state, 18), cell(label, 16), age, reasonText(row))
	}
	return layout.StyledTruncate(line, width, "…")
}

// cell keeps a column exactly one width wide. Padding alone is not enough: an
// over-long session ID would otherwise push every later column out of alignment
// for that row only.
func cell(value string, width int) string {
	return layout.PadRight(layout.Truncate(value, width, "…"), width)
}

// stateText marks needs-action rows so the group boundary stays legible after
// the list scrolls past the first screen.
func stateText(row aslbridge.Row) string {
	state := fieldOr(row.PrimaryState, "unknown")
	if row.UrgencyGroup == "needs-action" {
		return "! " + state
	}
	return "  " + state
}

// RowLabel names a run the way an operator asked for it, falling back to the
// plan directory and finally to the projected row ID.
func RowLabel(row aslbridge.Row) string {
	if label := strings.TrimSpace(row.Label); label != "" {
		return label
	}
	if row.PlanDir != "" {
		return filepath.Base(row.PlanDir)
	}
	return fieldOr(row.RowID, "(unnamed run)")
}

func sessionPhase(row aslbridge.Row) string {
	session := fieldOr(row.SessionID, "-")
	phase := fieldOr(row.Phase, "-")
	return session + "/" + phase
}

// reasonText is the shortest honest answer to "why is this row here".
func reasonText(row aslbridge.Row) string {
	for _, candidate := range []string{row.PauseReason, row.RecommendedAction, row.ActivityLabel} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "-"
}

func displayPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "(unknown workspace)"
	}
	return trimmed
}

func fieldOr(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

// StateMarker is the dashboard glyph a bound run paints next to its session.
func StateMarker(state string) string {
	switch state {
	case "paused-for-reply":
		return "❓"
	case "verifying":
		return "🔍"
	case "failed", "invalid":
		return "❌"
	case "stale":
		return "⏳"
	default:
		return "🔁"
	}
}
