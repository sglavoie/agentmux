package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// renderConfigPicker renders the profile picker overlay for restart/launch overrides.
func renderConfigPicker(m Model) string {
	var sb strings.Builder
	purpose := m.activeConfigPickerPurpose()
	header := purpose.header()
	title := dashboard.SessionDisplayTitle(m.targetSession)
	if purpose == ConfigPickerWorktreeReopen && m.worktreeActionTarget != nil {
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	}
	sb.WriteString(styleHeader.Render(header+": "+title) + "\n\n")

	maxName, maxHarness := 0, 0
	for _, acc := range m.configPickerAccounts {
		if n := layout.Width(acc.Name); n > maxName {
			maxName = n
		}
		if h := layout.Width(harnessDisplayText(m.cfg, acc.EffectiveHarness())); h > maxHarness {
			maxHarness = h
		}
	}
	harnessColor := dashboard.HarnessColorMap(m.cfg)
	for i, acc := range m.configPickerAccounts {
		namePad := maxName - layout.Width(acc.Name)
		if namePad < 0 {
			namePad = 0
		}
		name := accountStyle(acc.Color).Render(acc.Name) + strings.Repeat(" ", namePad)
		harnessText := harnessDisplayText(m.cfg, acc.EffectiveHarness())
		pad := maxHarness - layout.Width(harnessText)
		if pad < 0 {
			pad = 0
		}
		harness := accountStyle(harnessColor[acc.EffectiveHarness()]).Render(harnessText) + strings.Repeat(" ", pad)
		line := name + "  " + harness
		if home := acc.EffectiveHomeDir(); home != "" {
			line += "  " + styleDim.Render(home)
		}
		if strings.EqualFold(acc.Name, m.configPickerCurrent) {
			line += styleDim.Render(" (current)")
		}
		if i == m.configPickerCursor {
			sb.WriteString(renderSelectedRow(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if m.configPickerCursor >= 0 && m.configPickerCursor < len(m.configPickerAccounts) {
		selectedHarness := m.configPickerAccounts[m.configPickerCursor].EffectiveHarness()
		currentHarness := m.targetSession.Harness
		if currentHarness != "" && selectedHarness != "" && !strings.EqualFold(currentHarness, selectedHarness) {
			warning := "⚠  " + purpose.harnessWarning(currentHarness, selectedHarness)
			sb.WriteString("\n" + styleAttentionRow.Render(warning) + "\n")
		}
	}
	return sb.String()
}

// renderConfigPickerBox wraps renderConfigPicker in styleOverlayBox so the
// profile picker can be composited as a centered modal on top of the dimmed
// base view. The box grows to fit rendered profile rows and clamps to
// m.width-4 on narrow terminals.
func renderConfigPickerBox(m Model) string {
	content := strings.TrimRight(renderConfigPicker(m), "\n")
	boxWidth := 60
	if contentWidth := layout.PopupWidth(content) + styleOverlayBox.GetHorizontalFrameSize(); contentWidth > boxWidth {
		boxWidth = contentWidth
	}
	if avail := m.width - 4; avail < boxWidth {
		boxWidth = avail
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	return styleOverlayBox.Width(boxWidth).Render(content)
}
