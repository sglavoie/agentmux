package tui

import (
	"strings"
)

// renderConfigPicker renders the profile picker overlay for restart/launch overrides.
func renderConfigPicker(m Model) string {
	var sb strings.Builder
	purpose := m.activeConfigPickerPurpose()
	header := purpose.header()
	sb.WriteString(styleHeader.Render(header+": "+runningSessionDisplayTitle(m.targetSession)) + "\n\n")

	maxName, maxHarness := 0, 0
	for _, acc := range m.configPickerAccounts {
		if n := displayWidth(acc.Name); n > maxName {
			maxName = n
		}
		if h := displayWidth(harnessDisplayText(m.cfg, acc.EffectiveHarness())); h > maxHarness {
			maxHarness = h
		}
	}
	harnessColor := buildHarnessColorMap(m)
	for i, acc := range m.configPickerAccounts {
		namePad := maxName - displayWidth(acc.Name)
		if namePad < 0 {
			namePad = 0
		}
		name := accountStyle(acc.Color).Render(acc.Name) + strings.Repeat(" ", namePad)
		harnessText := harnessDisplayText(m.cfg, acc.EffectiveHarness())
		pad := maxHarness - displayWidth(harnessText)
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
			sb.WriteString(styleSelected.Render(line))
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
// base view. The inner width is capped at 60 columns and clamped to
// m.width-4 on narrow terminals.
func renderConfigPickerBox(m Model) string {
	innerWidth := 60
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderConfigPicker(m), "\n"))
}
