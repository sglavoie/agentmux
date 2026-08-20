package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// aslWideThreshold is the terminal width at or above which the Runs list shows
// every column inline. Below it the row keeps only what an operator needs to
// triage — state, label, age, reason — and the rest moves to the selected-row
// panel instead of being truncated away.
const aslWideThreshold = 100

// aslViewState is the single availability verdict the header, banner, and body
// all read, so they can never disagree about whether data is trustworthy.
type aslViewState int

const (
	aslViewLoading aslViewState = iota
	aslViewLive
	aslViewStale
	aslViewUnavailable
)

func aslView(m Model) aslViewState {
	return aslViewAt(m, time.Now())
}

func aslViewAt(m Model, now time.Time) aslViewState {
	switch {
	case !m.aslLoaded && m.aslErr == nil:
		return aslViewLoading
	case !m.aslLoaded:
		return aslViewUnavailable
	case m.aslErr == nil && m.aslStatus.Available && !m.aslMeta.Stale:
		return aslViewLive
	case len(m.aslRows) > 0 && aslSnapshotAge(m, now) <= aslbridge.StaleWindow:
		return aslViewStale
	default:
		return aslViewUnavailable
	}
}

// aslSnapshotAge measures how long the held projection has been in hand.
// ReceivedAt is preferred because it keeps ageing while the bridge is down;
// AgeSeconds is the fallback when a snapshot arrived without one.
func aslSnapshotAge(m Model, now time.Time) time.Duration {
	if !m.aslMeta.ReceivedAt.IsZero() {
		if age := now.Sub(m.aslMeta.ReceivedAt); age > 0 {
			return age
		}
		return 0
	}
	if m.aslMeta.AgeSeconds > 0 {
		return time.Duration(m.aslMeta.AgeSeconds) * time.Second
	}
	return 0
}

func aslNeedsActionCount(rows []aslbridge.Row) int {
	count := 0
	for _, row := range rows {
		if row.UrgencyGroup == "needs-action" {
			count++
		}
	}
	return count
}

func aslWide(m Model) bool {
	return m.width <= 0 || m.width >= aslWideThreshold
}

func aslVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	chrome := 2 + aslNoticeLines(m) + len(aslDetailPanel(m))
	available := rootBodyHeight(m) - chrome
	if available < 1 {
		return 1
	}
	return available
}

func aslNoticeLines(m Model) int {
	width := planViewerWrapWidth(m.width)
	lines := 0
	if banner := aslBannerText(m); banner != "" {
		lines += len(displayWrapHard(banner, width))
	}
	if m.aslErr != nil {
		lines += len(displayWrapHard(m.aslErr.Error(), width))
	}
	if m.aslNotice != "" {
		lines += len(displayWrapHard(m.aslNotice, width))
	}
	lines += len(aslOperationLines(m))
	return lines
}

// aslOperationLines reports the tracked submissions as identity and state only.
// A receipt outlives the form that produced it, which is how an accepted answer
// is seen to become consumed, so nothing here may carry the answer itself.
func aslOperationLines(m Model) []string {
	if len(m.aslOperations) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.aslOperations))
	for _, op := range m.aslOperations {
		out = append(out, fmt.Sprintf("  %s: %s", aslMethodLabel(op.Method), op.State))
	}
	return out
}

func aslBannerText(m Model) string {
	if aslView(m) != aslViewStale {
		return ""
	}
	return "read-only: showing the last agent-session-loop snapshot"
}

func renderASLRuns(m Model) string {
	var sb strings.Builder
	sb.WriteString(clampSearchLine(styleHeader.Render(aslHeaderText(m)), m.width))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	width := planViewerWrapWidth(m.width)
	if banner := aslBannerText(m); banner != "" {
		for _, line := range displayWrapHard(banner, width) {
			sb.WriteString(styleBanner.Render(line))
			sb.WriteByte('\n')
		}
	}
	if m.aslErr != nil {
		for _, line := range displayWrapHard(m.aslErr.Error(), width) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}
	if m.aslNotice != "" {
		for _, line := range displayWrapHard(m.aslNotice, width) {
			sb.WriteString(styleStateWarning.Render(line))
			sb.WriteByte('\n')
		}
	}
	for _, line := range aslOperationLines(m) {
		sb.WriteString(clampSearchLine(styleDim.Render(line), m.width))
		sb.WriteByte('\n')
	}

	sb.WriteString(renderASLRunList(m, aslVisibleHeight(m)))
	for _, line := range aslDetailPanel(m) {
		sb.WriteString(clampSearchLine(line, m.width))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func aslHeaderText(m Model) string {
	header := "Runs"
	switch aslView(m) {
	case aslViewLoading:
		return header + "  " + styleDim.Render("(loading…)")
	case aslViewUnavailable:
		return header + "  " + styleStateWarning.Render("[unavailable]")
	}
	availability := "[live]"
	if aslView(m) == aslViewStale {
		availability = "[stale]"
	}
	counts := fmt.Sprintf("[%d need action / %d runs]", aslNeedsActionCount(m.aslRows), len(m.aslRows))
	header += "  " + styleDim.Render(counts) + "  " + styleDim.Render(availability)
	if m.aslLoading {
		header += "  " + styleDim.Render("(refreshing…)")
	}
	return header
}

func renderASLRunList(m Model, height int) string {
	switch aslView(m) {
	case aslViewLoading:
		return styleDim.Render("  (loading runs…)") + "\n"
	case aslViewUnavailable:
		return renderASLUnavailable(m)
	}
	if len(m.aslRows) == 0 {
		return styleDim.Render("  (no active or actionable runs)") + "\n"
	}

	window := flatListWindowFor(len(m.aslRows), m.aslOffset, height)
	var sb strings.Builder
	if window.showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := window.start; i < window.end; i++ {
		line := formatASLRow(m.aslRows[i], m.width, aslWide(m))
		if i == m.aslCursor {
			line = styleSelected.Render(line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if window.showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// renderASLUnavailable states that the integration is unavailable. It is
// deliberately distinct from the empty list, which claims ASL is reachable and
// simply has nothing to show.
func renderASLUnavailable(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleDim.Render("  (agent-session-loop is unavailable)"))
	sb.WriteByte('\n')
	if reason := strings.TrimSpace(m.aslStatus.Reason); reason != "" {
		sb.WriteString(clampSearchLine(styleDim.Render("  "+reason), m.width))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func formatASLRow(row aslbridge.Row, width int, wide bool) string {
	if width <= 0 {
		width = 120
	}
	state := aslStateText(row)
	label := aslRowLabel(row)
	age := compactWorktreeAge(aslActivityTime(row), time.Now())
	var line string
	if wide {
		line = fmt.Sprintf("  %s  %s  %s  %s  %5s  %s",
			aslCell(state, 18), aslCell(label, 24), aslCell(aslSessionPhase(row), 22),
			aslCell(aslFieldOr(row.Harness, "?"), 8), age, aslDisplayPath(row.WorkspaceRoot))
	} else {
		line = fmt.Sprintf("  %s  %s  %5s  %s", aslCell(state, 18), aslCell(label, 16), age, aslReasonText(row))
	}
	return displayStyledTruncate(line, width, "…")
}

// aslCell keeps a column exactly one width wide. Padding alone is not enough:
// an over-long session ID would otherwise push every later column out of
// alignment for that row only.
func aslCell(value string, width int) string {
	return displayPadRight(displayTruncate(value, width, "…"), width)
}

// aslStateText marks needs-action rows so the group boundary stays legible
// after the list scrolls past the first screen.
func aslStateText(row aslbridge.Row) string {
	state := aslFieldOr(row.PrimaryState, "unknown")
	if row.UrgencyGroup == "needs-action" {
		return "! " + state
	}
	return "  " + state
}

func aslRowLabel(row aslbridge.Row) string {
	if label := strings.TrimSpace(row.Label); label != "" {
		return label
	}
	if row.PlanDir != "" {
		return filepath.Base(row.PlanDir)
	}
	return aslFieldOr(row.RowID, "(unnamed run)")
}

func aslSessionPhase(row aslbridge.Row) string {
	session := aslFieldOr(row.SessionID, "-")
	phase := aslFieldOr(row.Phase, "-")
	return session + "/" + phase
}

// aslReasonText is the shortest honest answer to "why is this row here".
func aslReasonText(row aslbridge.Row) string {
	for _, candidate := range []string{row.PauseReason, row.RecommendedAction, row.ActivityLabel} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "-"
}

// aslDetailPanel describes the selected run. The binding line shows at every
// width because ownership is re-derived per snapshot; the remaining fields are
// narrow-only, since a wide row already carries them inline.
func aslDetailPanel(m Model) []string {
	if aslView(m) == aslViewUnavailable {
		return nil
	}
	row, ok := m.selectedASLRow()
	if !ok {
		return nil
	}
	lines := []string{"", styleDim.Render("  binding: ") + aslBindingText(m, row)}
	if aslWide(m) {
		return lines
	}
	return append(lines,
		styleDim.Render("  session/phase: ")+aslSessionPhase(row),
		styleDim.Render("  harness: ")+aslFieldOr(row.Harness, "?"),
		styleDim.Render("  workspace: ")+aslDisplayPath(row.WorkspaceRoot),
	)
}

// aslBindingText names the live session a run is exactly bound to, or reports
// the run as background when no exact binding survives. A loose workspace or
// harness match is not ownership and is reported as unbound, so a resumed run
// never appears to still own a window it lost.
func aslBindingText(m Model, row aslbridge.Row) string {
	if bound, found := aslExactBinding(row, m.sessions); found {
		return aslFieldOr(bound.Project, aslFieldOr(bound.Harness, "live session"))
	}
	return "background (no bound session)"
}

func aslDisplayPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "(unknown workspace)"
	}
	return trimmed
}

func aslFieldOr(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
