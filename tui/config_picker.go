package tui

import (
	"fmt"
	"strings"
)

// renderConfigPicker renders the profile picker overlay for restart/launch overrides.
func renderConfigPicker(m Model) string {
	var sb strings.Builder
	header := "Choose profile"
	if m.configPickerReturnMode == ModePicker {
		header = "Launch profile"
	}
	sb.WriteString(styleHeader.Render(header+": "+runningSessionDisplayTitle(m.targetSession)) + "\n\n")

	maxName, maxHarness := 0, 0
	for _, acc := range m.configPickerAccounts {
		if n := len(acc.Name); n > maxName {
			maxName = n
		}
		if h := len(acc.EffectiveHarness()); h > maxHarness {
			maxHarness = h
		}
	}
	for i, acc := range m.configPickerAccounts {
		name := fmt.Sprintf("%-*s", maxName, acc.Name)
		harness := fmt.Sprintf("%-*s", maxHarness, acc.EffectiveHarness())
		line := name + "  " + styleDim.Render(harness)
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
			warning := "⚠  Switching harness: " + currentHarness + " → " + selectedHarness + ". Session will restart."
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
