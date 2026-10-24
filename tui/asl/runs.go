package asl

// This file paints the Runs view: the header that states the availability
// verdict alongside the counts, the notice stack (stale banner, error, action
// notice, receipts), the scrolled run list, and the selected-run detail panel.

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/layout"
)

// RenderRuns draws the whole Runs body.
func RenderRuns(in RunsInput) string {
	var sb strings.Builder
	sb.WriteString(layout.ClampLine(styleHeader.Render(in.headerText()), in.Width))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	width := layout.WrapWidth(in.Width)
	if banner := in.BannerText(); banner != "" {
		for _, line := range layout.WrapHard(banner, width) {
			sb.WriteString(styleBanner.Render(line))
			sb.WriteByte('\n')
		}
	}
	if in.Err != nil {
		for _, line := range layout.WrapHard(in.Err.Error(), width) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}
	if in.Notice != "" {
		for _, line := range layout.WrapHard(in.Notice, width) {
			sb.WriteString(styleStateWarning.Render(line))
			sb.WriteByte('\n')
		}
	}
	for _, line := range in.operationLines() {
		sb.WriteString(layout.ClampLine(styleDim.Render(line), in.Width))
		sb.WriteByte('\n')
	}

	sb.WriteString(in.renderRunList(in.VisibleHeight()))
	for _, line := range in.detailPanel() {
		sb.WriteString(layout.ClampLine(line, in.Width))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// headerText states the availability verdict next to the counts, so the header
// never claims a count for data it does not trust.
func (in RunsInput) headerText() string {
	header := "Runs"
	switch in.View() {
	case ViewLoading:
		return header + "  " + styleDim.Render(layout.Placeholder(layout.LoadingText("")))
	case ViewUnavailable:
		return header + "  " + styleStateWarning.Render("[unavailable]")
	}
	availability := "[live]"
	if in.View() == ViewStale {
		availability = "[stale]"
	}
	counts := fmt.Sprintf("[%d need action / %d runs]", NeedsActionCount(in.Rows), len(in.Rows))
	header += "  " + styleDim.Render(counts) + "  " + styleDim.Render(availability)
	if in.Loading {
		header += "  " + styleDim.Render("(refreshing…)")
	}
	return header
}

// emptyRunsText is the empty list's state and its next step: a run starts from
// a saved plan, with R in the Plans view (reached from ":" then "Search Plans").
var emptyRunsText = layout.EmptyText("active or actionable runs", "R on a plan in Plans starts one")

func (in RunsInput) renderRunList(height int) string {
	switch in.View() {
	case ViewLoading:
		return styleDim.Render("  "+layout.Placeholder(layout.LoadingText("runs"))) + "\n"
	case ViewUnavailable:
		return in.renderUnavailable()
	}
	if len(in.Rows) == 0 {
		return styleDim.Render(layout.ClampLine("  "+layout.Placeholder(emptyRunsText), in.Width)) + "\n"
	}

	now := in.now()
	window := layout.ListWindowFor(len(in.Rows), in.Offset, height)
	var sb strings.Builder
	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := window.Start; i < window.End; i++ {
		line := formatRow(in.Rows[i], in.Width, in.wide(), now)
		if i == in.Cursor {
			line = renderSelectedRow(line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// renderUnavailable states that the integration is unavailable. It is
// deliberately distinct from the empty list, which claims ASL is reachable and
// simply has nothing to show.
func (in RunsInput) renderUnavailable() string {
	var sb strings.Builder
	sb.WriteString(styleDim.Render("  (agent-session-loop is unavailable)"))
	sb.WriteByte('\n')
	if reason := strings.TrimSpace(in.Status.Reason); reason != "" {
		sb.WriteString(layout.ClampLine(styleDim.Render("  "+reason), in.Width))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// detailPanel describes the selected run. The reason and binding lines show at
// every width: a wide row spends its last column on the workspace rather than
// on why the run is listed, and ownership is re-derived per snapshot. The
// remaining fields are narrow-only, since a wide row already carries them
// inline.
func (in RunsInput) detailPanel() []string {
	if in.View() == ViewUnavailable {
		return nil
	}
	row, ok := in.SelectedRow()
	if !ok {
		return nil
	}
	lines := []string{
		"",
		styleDim.Render("  reason: ") + reasonText(row),
		styleDim.Render("  binding: ") + in.bindingText(row),
	}
	if in.wide() {
		return lines
	}
	return append(lines,
		styleDim.Render("  session/phase: ")+sessionPhase(row),
		styleDim.Render("  harness: ")+fieldOr(row.Harness, "?"),
		styleDim.Render("  workspace: ")+displayPath(row.WorkspaceRoot),
	)
}

// bindingText names the live session a run is exactly bound to, or reports the
// run as background when no exact binding survives. A loose workspace or
// harness match is not ownership and is reported as unbound, so a resumed run
// never appears to still own a window it lost.
func (in RunsInput) bindingText(row aslbridge.Row) string {
	if bound, found := ExactBinding(row, in.Sessions); found {
		return fieldOr(bound.Project, fieldOr(bound.Harness, "live session"))
	}
	return "background (no bound session)"
}
