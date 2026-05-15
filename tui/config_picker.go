package tui

import "strings"

// renderConfigPicker renders the profile picker overlay for restart/launch overrides.
func renderConfigPicker(m Model) string {
	var sb strings.Builder
	header := "Choose profile"
	if m.configPickerReturnMode == ModePicker {
		header = "Launch profile"
	}
	sb.WriteString(styleHeader.Render(header + ": " + m.targetSession.Base) + "\n\n")

	if m.configPickerCursor >= 0 && m.configPickerCursor < len(m.configPickerAccounts) {
		selectedHarness := m.configPickerAccounts[m.configPickerCursor].EffectiveHarness()
		currentHarness := m.targetSession.Harness
		if currentHarness != "" && selectedHarness != "" && !strings.EqualFold(currentHarness, selectedHarness) {
			warning := "⚠  Switching harness: " + currentHarness + " → " + selectedHarness + ". Session will restart."
			sb.WriteString(styleAttentionRow.Render(warning) + "\n\n")
		}
	}
	for i, acc := range m.configPickerAccounts {
		line := "  " + acc.Name + "  " + styleDim.Render(acc.EffectiveHarness())
		if home := acc.EffectiveHomeDir(); home != "" {
			line += "  " + styleDim.Render(home)
		}
		if acc.Name == m.configPickerCurrent {
			line += styleDim.Render(" (current)")
		}
		if i == m.configPickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
