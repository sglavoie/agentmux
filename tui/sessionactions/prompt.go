// The confirmation a destructive session action raises, drawn without a host
// model.
//
// The prompt itself has lived in this package since the controller did: Confirm
// and ConfirmPromptFor decide WHETHER one is raised and what it says. What was
// missing was the other half — how it is PAINTED — which stayed in the
// dashboard's root package behind a renderer that reads a Bubble Tea model. A
// second surface offering the same actions (the live session overview, which
// F2 opens) would have had to re-spell that rendering or import the root, and
// the root is the one package it may not import.
//
// So the painting moves here, beside the sentences it paints. The input is a
// value: a verb, the thing the verb applies to, and the consequence of saying
// yes. The dashboard keeps its own renderer as a thin caller, because it raises
// confirmations for worktrees and ASL runs that have nothing to do with session
// actions and those stay where they are.

package sessionactions

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

// promptNaturalWidth is the confirmation's preferred outer width. It matches the
// value the dashboard's confirm box has always used; the clamp below is what
// makes a narrow terminal reflow rather than clip.
const promptNaturalWidth = 70

// The prompt's three styles, derived from the shared palette exactly as the
// dashboard's confirm overlay derives them, so moving the rendering here does
// not move its colors.
var (
	stylePromptVerb        = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	stylePromptConsequence = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	stylePromptBox         = theme.Popup()
)

// PromptInput is everything the confirmation is drawn from.
//
// Verb is the imperative being confirmed ("Close", "Restart"). Subject is what
// it applies to — a session title, already resolved and including any icon;
// this package does not know how a host names its rows. Consequence is the
// sentence describing what saying yes actually does, which is the reason the
// overlay exists at all: a prompt that only repeats the verb asks the operator
// to confirm the thing they just pressed.
//
// Width is the host's viewport width. Zero means unconstrained, which is what a
// natural-width probe wants.
type PromptInput struct {
	Verb        string
	Subject     string
	Consequence string
	Width       int
}

// ConfirmVerbText is the imperative one confirmation verb is PRINTED as, and
// false for a verb this package did not name.
//
// The three verbs are stable identifiers a host stores while the prompt is up;
// what they are spelled as on screen is presentation, and it lived in the
// dashboard's own confirm overlay beside the verbs for worktrees and ASL runs.
// The overview raises the same three prompts and would have had to spell them a
// second time, which is exactly the duplication the surface wordings in
// sessionactions.go exist to prevent. A host with verbs of its own keeps them:
// the false return says "not one of mine", not "unnameable".
func ConfirmVerbText(verb string) (string, bool) {
	switch verb {
	case ConfirmClose:
		return "End session", true
	case ConfirmRestart:
		return "Restart", true
	case ConfirmReplaceProfile:
		return "Restart under a different profile", true
	}
	return "", false
}

// PromptFor builds the input for one confirmation of a session action. It is the
// pairing Confirm already decided, said in the renderer's vocabulary, so a host
// that has a pending action and a target does not have to restate either.
func PromptFor(prompt ConfirmPrompt, subject string, width int) PromptInput {
	return PromptInput{
		Verb:        prompt.Verb,
		Subject:     subject,
		Consequence: prompt.Consequence,
		Width:       width,
	}
}

// BoxWidth is the outer width the prompt renders at: its preferred width, always
// leaving two terminal cells on either side.
func (in PromptInput) BoxWidth() int {
	width := promptNaturalWidth
	if in.Width > 0 {
		width = min(width, in.Width-4)
	}
	return max(width, 1)
}

// ContentWidth is the interior the two lines wrap against.
func (in PromptInput) ContentWidth() int {
	return max(in.BoxWidth()-stylePromptBox.GetHorizontalFrameSize(), 1)
}

// RenderPrompt renders the modal interior: the question, then the consequence.
//
// The consequence is WRAPPED rather than truncated. Half a sentence about what
// closing a session destroys is not a shorter reading of the warning, it is a
// different one, and the operator is about to answer y or n to whichever
// sentence they read.
func RenderPrompt(in PromptInput) string {
	width := in.ContentWidth()
	lines := []string{
		stylePromptVerb.Render(layout.Truncate(
			fmt.Sprintf("%s %q?", in.Verb, layout.RenderTitleIcon(in.Subject)), width, "…")),
	}
	for _, line := range layout.WrapWords(in.Consequence, width) {
		lines = append(lines, stylePromptConsequence.Render(line))
	}
	return strings.Join(lines, "\n")
}

// RenderPromptBox is the bounded-modal form a host composites.
func RenderPromptBox(in PromptInput) string {
	return stylePromptBox.Width(in.BoxWidth()).Render(RenderPrompt(in))
}
