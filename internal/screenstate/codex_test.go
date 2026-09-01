package screenstate

import (
	"testing"
)

// The three tests below moved here verbatim from
// internal/daemon/codex_prompt_completion_test.go when the classifiers moved.
// They are unchanged on purpose: Codex's classifiers are behaviour-neutral
// across the move except for the two word-independent vetoes, so anything these
// cases stopped asserting would be a regression the move introduced. The new
// coverage — the vetoes, the adapter's State/Tier reading, and the composer
// placeholder's accepted brittleness — is appended after them.

func TestClassifyCodexClearScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		all    string
		want   bool
	}{
		{
			name:   "reported cleared startup UI",
			screen: codexReportedClearedScreen,
			all:    codexReportedClearedScreen,
			want:   true,
		},
		{
			name:   "cleared startup UI with generic summary wording",
			screen: codexClearedScreen,
			all:    codexClearedScreen,
			want:   true,
		},
		{
			name:   "cleared composer without startup box",
			screen: codexClearedComposerOnlyScreen,
			all:    codexClearedComposerOnlyScreen,
			want:   true,
		},
		{
			name:   "visible prompt is not a clear screen",
			screen: codexEditApprovalScreen,
			all:    codexEditApprovalScreen,
			want:   false,
		},
		{
			name:   "nonzero context is not a clear screen",
			screen: codexNonzeroContextScreen,
			all:    codexNonzeroContextScreen,
			want:   false,
		},
		{
			name:   "transcript in scrollback blocks clear",
			screen: codexClearedScreen,
			all:    codexClearedScreenWithTranscript,
			want:   false,
		},
		{
			name:   "prompt marker in transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "› Earlier output: type /clear when done\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "assistant marker in transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "• I will inspect the hook flow.\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "tool tree marker in transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "└ Read codex_prompt.go\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "labeled transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "User: investigate /clear\nCodex: checking\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "empty scrollback does not clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "\n\n",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexClearScreen(tc.screen, tc.all); got != tc.want {
				t.Fatalf("ClassifyCodexClearScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyCodexCompletedTurnScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   bool
	}{
		{
			name:   "completed turn with composer",
			screen: codexCompletedTurnScreen,
			want:   true,
		},
		{
			name:   "active working turn",
			screen: codexWorkingTurnScreen,
			want:   false,
		},
		{
			name:   "prompt screen takes precedence",
			screen: codexCompletedThenPromptScreen,
			want:   false,
		},
		{
			name:   "worked for resumed to working",
			screen: codexWorkedThenWorkingScreen,
			want:   false,
		},
		{
			name:   "worked for without trailing composer",
			screen: codexWorkedForNoComposerScreen,
			want:   false,
		},
		{
			name:   "transcript prose mentioning worked for",
			screen: codexWorkedForProseScreen,
			want:   false,
		},
		{
			name:   "no status line at all",
			screen: codexClearedComposerOnlyScreen,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexCompletedTurnScreen(tc.screen); got != tc.want {
				t.Fatalf("ClassifyCodexCompletedTurnScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyCodexInterruptedTurnScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   bool
	}{
		{
			name:   "interrupted turn with composer",
			screen: codexInterruptedTurnScreen,
			want:   true,
		},
		{
			name:   "conversation interrupted with composer",
			screen: codexConversationInterruptedScreen,
			want:   true,
		},
		{
			name:   "installed 0.144.3 interruption with dim composer placeholder",
			screen: codexInstalledInterruptedTurnScreen,
			want:   true,
		},
		{
			name:   "interrupted then resumed to working",
			screen: codexInterruptedThenWorkingScreen,
			want:   false,
		},
		{
			name:   "interrupted without trailing composer",
			screen: codexInterruptedNoComposerScreen,
			want:   false,
		},
		{
			name:   "prompt screen takes precedence",
			screen: codexInterruptedThenPromptScreen,
			want:   false,
		},
		{
			name:   "transcript prose mentioning interrupted",
			screen: codexInterruptedProseScreen,
			want:   false,
		},
		{
			name:   "working spinner esc-to-interrupt is not interrupted",
			screen: codexWorkingTurnScreen,
			want:   false,
		},
		{
			name:   "no status line at all",
			screen: codexClearedComposerOnlyScreen,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexInterruptedTurnScreen(tc.screen); got != tc.want {
				t.Fatalf("ClassifyCodexInterruptedTurnScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}
