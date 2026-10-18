package screenstate

import (
	"os"
	"strings"
	"testing"
)

// Captured from codex-cli 0.163.0 on a private tmux server after /status and
// /new, at 64x24 and 140x45 respectively. Only the temporary directory was
// replaced with /tmp/amx-reset-check; no answer or reset marker was invented.

func TestCodexNewWithTruncatedContextFooter(t *testing.T) {
	data, err := os.ReadFile("testdata/codex_new_narrow.txt")
	if err != nil {
		t.Fatal(err)
	}
	fresh := string(data)
	if strings.Contains(fresh, "Context") {
		t.Fatal("fixture must exercise the missing context counter")
	}
	for _, tc := range []struct {
		name, screen, history string
		want                  bool
	}{
		{"fresh narrow session", fresh, fresh, true},
		{"unreadable history", fresh, "", false},
		{"previous answer in history", fresh, codexCompletedTurnScreen + fresh, false},
		{"plain answer in history", fresh, "An earlier answer.\n" + fresh, false},
		{"typed command is not a reset", strings.ReplaceAll(fresh, "Ask Codex to do anything", "/new"), fresh, false},
		{"answer on screen", strings.Replace(fresh, "›", "An answer.\n›", 1), fresh, false},
		{"nonzero context", strings.Replace(fresh, "granular ·…", "granular · Context 12% used", 1), fresh, false},
		{"nonzero context with exhausted quota", strings.Replace(fresh, "granular ·…", "Context 12% used · weekly 0% left", 1), fresh, false},
		{"working footer", strings.ReplaceAll(fresh, "Ready", "Working"), fresh, false},
		{"missing banner", strings.ReplaceAll(fresh, ">_ OpenAI Codex (v0.163.0)", ""), fresh, false},
		{"background task", fresh + "1 background terminal running\n", fresh, false},
		{"prompt in history", fresh, fresh + codexEditApprovalScreen, false},
		{"new turn in history", fresh, codexWorkingTurnScreen, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexClearScreen(tc.screen, tc.history); got != tc.want {
				t.Fatalf("ClassifyCodexClearScreen = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCodexClearContextCounterDoesNotReadQuotaAsContext(t *testing.T) {
	screen := strings.ReplaceAll(codexClearedComposerOnlyScreen, "context 0%", "context 12% · weekly 0% left")
	if screen == codexClearedComposerOnlyScreen {
		t.Fatal("fixture did not replace the context count")
	}
	if ClassifyCodexClearScreen(screen, screen) {
		t.Fatal("quota exhaustion must not be mistaken for an empty context")
	}
}

func TestCodexFreshLayoutWithLogoAndNoContextCounter(t *testing.T) {
	data, err := os.ReadFile("testdata/codex_new_wide.txt")
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.ReplaceAll(string(data), " · Context 0% used", "")
	if !ClassifyCodexClearScreen(fresh, fresh) {
		t.Fatal("fresh layout with the Codex logo and a hidden context counter was rejected")
	}
}
