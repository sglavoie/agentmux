package launch

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ProfileInput is what the profile-override overlay needs to render itself.
// Title is supplied by the caller because which name the overlay is titled
// after depends on the mode it was opened from — a live session for a restart,
// a managed worktree for a reopen — and resolving that is the root model's job.
type ProfileInput struct {
	Accounts []config.Account
	Cursor   int
	// Current is the profile name the target already uses; its row is
	// annotated "(current)".
	Current string
	// CurrentHarness is the harness the target already runs, used to decide
	// whether the harness-switch warning applies.
	CurrentHarness string
	Purpose        ConfigPickerPurpose
	Title          string
	Width          int
	Height         int
	Cfg            *config.Config
}

// Render renders the profile picker overlay for restart/launch overrides.
func (in ProfileInput) Render() string {
	width := max(1, in.boxWidth()-styleOverlayBox.GetHorizontalFrameSize())
	wrap := func(text string) string { return ansi.Hardwrap(text, width, true) }
	header := styleHeader.Render(wrap(in.Purpose.header() + ": " + in.Title))
	detail := ""
	if in.Cursor >= 0 && in.Cursor < len(in.Accounts) {
		selected := in.Accounts[in.Cursor]
		detail = styleDim.Render(wrap("Home: " + selected.EffectiveHomeDir()))
		if layout.Width(selected.Name) > width-25 {
			detail = styleDim.Render(wrap("Profile: "+selected.Name)) + "\n" + detail
		}
		if current, chosen := in.CurrentHarness, selected.EffectiveHarness(); current != "" && chosen != "" && !strings.EqualFold(current, chosen) {
			detail = styleAttentionRow.Render(wrap("⚠  "+in.Purpose.harnessWarning(current, chosen))) + "\n" + detail
		}
	}
	// Metadata consumes only the selected row's detail, never every row. A
	// bounded list follows its cursor when there are more profiles than fit.
	gaps := true
	headerRows := strings.Count(header, "\n") + 1
	detailRows := 0
	if detail != "" {
		detailRows = strings.Count(detail, "\n") + 1
	}
	available := headerRows + detailRows + len(in.Accounts) + 3
	if in.Height > 0 {
		available = max(1, in.Height-styleOverlayBox.GetVerticalFrameSize())
	}
	if available < headerRows+detailRows+4 {
		gaps = false
	}
	if available < headerRows+detailRows+2 {
		header = styleHeader.Render(layout.Truncate(in.Purpose.header()+": "+in.Title, width, "…"))
		headerRows = 1
		detailLimit := max(0, available-3)
		detailLines := strings.Split(detail, "\n")
		if len(detailLines) > detailLimit {
			detailLines = detailLines[:detailLimit]
		}
		detail = strings.Join(detailLines, "\n")
		detailRows = len(detailLines)
	}
	gapRows := 0
	if gaps {
		gapRows = 2
	}
	budget := max(1, available-headerRows-detailRows-gapRows)
	if budget < len(in.Accounts) {
		budget = max(1, budget-1)
	}
	start := max(0, min(in.Cursor-budget/2, len(in.Accounts)-budget))
	end := min(len(in.Accounts), start+budget)
	var lines []string
	lines = append(lines, header)
	if gaps {
		lines = append(lines, "")
	}
	harnessColor := dashboard.HarnessColorMap(in.Cfg)
	for i := start; i < end; i++ {
		acc := in.Accounts[i]
		suffix := "  " + harnessDisplayText(in.Cfg, acc.EffectiveHarness())
		if strings.EqualFold(acc.Name, in.Current) {
			suffix += " (current)"
		}
		nameWidth := max(1, width-layout.Width(suffix))
		name := layout.Truncate(acc.Name, nameWidth, "…")
		line := accountStyle(acc.Color).Render(name) + accountStyle(harnessColor[acc.EffectiveHarness()]).Render(suffix)
		line = layout.StyledTruncate(line, width, "…")
		if i == in.Cursor {
			line = renderSelectedRow(line)
		}
		lines = append(lines, line)
	}
	if end-start < len(in.Accounts) && available > headerRows+detailRows+gapRows+1 {
		lines = append(lines, styleDim.Render(fmt.Sprintf("%d–%d of %d profiles", start+1, end, len(in.Accounts))))
	}
	if detail != "" {
		if gaps {
			lines = append(lines, "")
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n") + "\n"
}

func (in ProfileInput) boxWidth() int {
	if in.Width <= 0 {
		return 100
	}
	return max(1, min(100, in.Width-4))
}

// RenderBox wraps the responsive list and its selected-profile details.
func (in ProfileInput) RenderBox() string {
	return styleOverlayBox.Width(in.boxWidth()).Render(strings.TrimRight(in.Render(), "\n"))
}

// AccountIndex returns the index of the named account in the slice, or 0 if
// not found.
func AccountIndex(accounts []config.Account, name string) int {
	for i, a := range accounts {
		if strings.EqualFold(a.Name, name) {
			return i
		}
	}
	return 0
}
