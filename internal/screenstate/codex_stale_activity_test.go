package screenstate

import "testing"

func TestCodexCompletionSupersedesHistoricalActivityText(t *testing.T) {
	for _, terminal := range []struct {
		name, screen string
	}{
		{"worked for", "• Worked for 12s\n\n› Ask Codex to do anything\n"},
		{"completed", codexFramedCompletedScreen},
		{"interrupted", "■ Conversation interrupted\n\n› Ask Codex to do anything\n"},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			for _, history := range []string{
				"• The old status said: 1 background terminal running · /ps to view · /stop to close\n",
				"• Press Esc to interrupt the command.\n",
				"• Working (5s • esc to interrupt) · 1 background terminal running\n",
			} {
				got := codexObservation(t, history+terminal.screen)
				if !got.SettleableTerminal() {
					t.Errorf("history %q: state %s, vetoes %s; want terminal", history, got.State, got.Vetoes)
				}
			}
		})
	}
}

func TestCodexCurrentActivityStillVetoesCompletion(t *testing.T) {
	for _, current := range []struct {
		text string
		veto Veto
	}{
		{"  1 background terminal running · /ps to view · /stop to close", VetoBackgroundTask},
		{"• Cogitating · press ctrl+c to cancel", VetoInterruptHint},
		{"• Working (5s • esc to interrupt)", VetoLiveStatusLine | VetoInterruptHint},
	} {
		got := codexObservation(t, "• Worked for 12s\n\n"+current.text+"\n\n› Ask Codex to do anything\n")
		if got.SettleableTerminal() || got.Vetoes&current.veto != current.veto {
			t.Errorf("current %q: state %s, vetoes %s; want veto %s", current.text, got.State, got.Vetoes, current.veto)
		}
	}
}
