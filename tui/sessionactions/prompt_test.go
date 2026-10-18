package sessionactions

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/layout"
)

// TestPromptForCarriesTheDecidedSentences checks that the renderer's input is
// the pairing Confirm already made rather than a second spelling of it: a host
// with a pending prompt and a subject restates neither.
func TestPromptForCarriesTheDecidedSentences(t *testing.T) {
	prompt := ConfirmPrompt{Verb: "End Session", Consequence: "Ends the harness process running in this tab."}
	in := PromptFor(prompt, "api", 100)
	if in.Verb != prompt.Verb {
		t.Errorf("verb = %q, want %q", in.Verb, prompt.Verb)
	}
	if in.Consequence != prompt.Consequence {
		t.Errorf("consequence = %q, want %q", in.Consequence, prompt.Consequence)
	}
	if in.Subject != "api" {
		t.Errorf("subject = %q, want %q", in.Subject, "api")
	}
	if in.Width != 100 {
		t.Errorf("width = %d, want 100", in.Width)
	}
}

// TestPromptBoxWidthLeavesTwoCellsOnEitherSide is the shared popup margin
// contract, which is the whole reason the width policy moved into this package
// rather than being restated by each host.
func TestPromptBoxWidthLeavesTwoCellsOnEitherSide(t *testing.T) {
	for _, tc := range []struct{ viewport, want int }{
		{0, promptNaturalWidth},
		{200, promptNaturalWidth},
		{80, 70},
		{60, 56},
		{20, 16},
		{4, 1},
		{1, 1},
	} {
		if got := (PromptInput{Width: tc.viewport}).BoxWidth(); got != tc.want {
			t.Errorf("BoxWidth at viewport %d = %d, want %d", tc.viewport, got, tc.want)
		}
	}
}

// TestRenderPromptWrapsTheConsequence is the stated policy: half a sentence
// about what saying yes destroys is a different warning, not a shorter one, and
// the operator answers y or n to whichever sentence they read.
func TestRenderPromptWrapsTheConsequence(t *testing.T) {
	consequence := "Ends the harness process running in this tab and closes the Kitty window; unsaved work in the pane is lost."
	in := PromptInput{Verb: "End Session", Subject: "api", Consequence: consequence, Width: 60}
	out := RenderPrompt(in)
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("prompt is %d lines, want the consequence wrapped onto more than one:\n%s", len(lines), out)
	}
	body := strings.Join(lines[1:], " ")
	for _, word := range strings.Fields(consequence) {
		if !strings.Contains(body, word) {
			t.Errorf("consequence lost the word %q:\n%s", word, out)
		}
	}
}

// TestRenderPromptNeverExceedsTheContentWidth covers the four target sizes and
// the degenerate ones underneath them, because a prompt wider than its box is a
// clipped warning.
func TestRenderPromptNeverExceedsTheContentWidth(t *testing.T) {
	inputs := []PromptInput{
		{Verb: "End Session", Subject: "api", Consequence: "Ends the harness process running in this tab."},
		{
			Verb:        "Restart under a different profile",
			Subject:     "monorepo-services-authentication-gateway",
			Consequence: strings.Repeat("a long consequence sentence ", 8),
		},
		{Verb: "End Session", Subject: "日本語のセッション", Consequence: "セッションを終了します。"},
		{Verb: "End Session", Subject: "", Consequence: ""},
	}
	for _, width := range []int{80, 100, 120, 160, 40, 10} {
		for _, in := range inputs {
			in.Width = width
			content := in.ContentWidth()
			for _, line := range strings.Split(RenderPrompt(in), "\n") {
				if got := layout.StyledWidth(line); got > content {
					t.Errorf("width %d: line spans %d cells, content width is %d: %q", width, got, content, line)
				}
			}
			for _, line := range strings.Split(RenderPromptBox(in), "\n") {
				if got := layout.StyledWidth(line); got > width {
					t.Errorf("width %d: box line spans %d cells: %q", width, got, line)
				}
			}
		}
	}
}

// TestRenderPromptStatesVerbAndSubject keeps the question itself readable: the
// first line has to say what is about to happen and to what.
func TestRenderPromptStatesVerbAndSubject(t *testing.T) {
	in := PromptInput{Verb: "End Session", Subject: "api", Consequence: "Ends the process.", Width: 100}
	first := strings.Split(RenderPrompt(in), "\n")[0]
	if !strings.Contains(first, "End Session") || !strings.Contains(first, "api") {
		t.Errorf("first line %q does not name both the verb and the subject", first)
	}
}
