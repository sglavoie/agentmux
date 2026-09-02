package launch

import (
	"strings"

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
	Cfg            *config.Config
}

// Render renders the profile picker overlay for restart/launch overrides.
func (in ProfileInput) Render() string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render(in.Purpose.header()+": "+in.Title) + "\n\n")

	maxName, maxHarness := 0, 0
	for _, acc := range in.Accounts {
		if n := layout.Width(acc.Name); n > maxName {
			maxName = n
		}
		if h := layout.Width(harnessDisplayText(in.Cfg, acc.EffectiveHarness())); h > maxHarness {
			maxHarness = h
		}
	}
	harnessColor := dashboard.HarnessColorMap(in.Cfg)
	for i, acc := range in.Accounts {
		namePad := maxName - layout.Width(acc.Name)
		if namePad < 0 {
			namePad = 0
		}
		name := accountStyle(acc.Color).Render(acc.Name) + strings.Repeat(" ", namePad)
		harnessText := harnessDisplayText(in.Cfg, acc.EffectiveHarness())
		pad := maxHarness - layout.Width(harnessText)
		if pad < 0 {
			pad = 0
		}
		harness := accountStyle(harnessColor[acc.EffectiveHarness()]).Render(harnessText) + strings.Repeat(" ", pad)
		line := name + "  " + harness
		if home := acc.EffectiveHomeDir(); home != "" {
			line += "  " + styleDim.Render(home)
		}
		if strings.EqualFold(acc.Name, in.Current) {
			line += styleDim.Render(" (current)")
		}
		if i == in.Cursor {
			sb.WriteString(renderSelectedRow(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if in.Cursor >= 0 && in.Cursor < len(in.Accounts) {
		selectedHarness := in.Accounts[in.Cursor].EffectiveHarness()
		currentHarness := in.CurrentHarness
		if currentHarness != "" && selectedHarness != "" && !strings.EqualFold(currentHarness, selectedHarness) {
			warning := "⚠  " + in.Purpose.harnessWarning(currentHarness, selectedHarness)
			sb.WriteString("\n" + styleAttentionRow.Render(warning) + "\n")
		}
	}
	return sb.String()
}

// RenderBox wraps Render in styleOverlayBox so the profile picker can be
// composited as a centered modal on top of the dimmed base view. The box grows
// to fit rendered profile rows and clamps to Width-4 on narrow terminals.
func (in ProfileInput) RenderBox() string {
	content := strings.TrimRight(in.Render(), "\n")
	boxWidth := 60
	if contentWidth := layout.PopupWidth(content) + styleOverlayBox.GetHorizontalFrameSize(); contentWidth > boxWidth {
		boxWidth = contentWidth
	}
	if avail := in.Width - 4; avail < boxWidth {
		boxWidth = avail
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	return styleOverlayBox.Width(boxWidth).Render(content)
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
