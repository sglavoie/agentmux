package tui

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
)

// aslResponseInnerWidth is the widest the response form draws. It is clamped to
// the terminal on narrow layouts so the box never overflows.
const aslResponseInnerWidth = 76

// aslResponseBodyLines bounds how much of a question is shown at once. The
// remainder is reachable by opening the Workbench; the form is for answering,
// not for reading an entire transcript.
const aslResponseBodyLines = 10

// aslResponseEditorLines bounds the visible height of the answer editor.
const aslResponseEditorLines = 6

func aslResponseWidth(m Model) int {
	width := aslResponseInnerWidth
	if avail := m.width - 4; m.width > 0 && avail < width {
		width = avail
	}
	if width < 1 {
		width = 1
	}
	return width
}

func renderASLResponseBox(m Model) string {
	width := aslResponseWidth(m)
	return styleOverlayBox.Width(width).Render(strings.TrimRight(renderASLResponse(m, width), "\n"))
}

func renderASLResponse(m Model, width int) string {
	state := m.aslResponse
	var sb strings.Builder

	sb.WriteString(styleHeader.Render(aslResponseTitle(state)))
	sb.WriteByte('\n')
	for _, line := range layout.WrapHard(aslResponseMetaLine(state), width) {
		sb.WriteString(styleDim.Render(line))
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	for _, line := range aslResponseBodyBlock(state, width) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	sb.WriteString(styleDim.Render("Answer"))
	sb.WriteByte('\n')
	for _, line := range aslResponseEditorBlock(state, width) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	sb.WriteString(aslResponseButtonRow(state))
	sb.WriteByte('\n')

	for _, line := range aslResponseStatusLines(state, width) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func aslResponseTitle(state aslResponseState) string {
	if state.kind == aslResponsePaused {
		return "Resume " + aslFieldOr(state.target.Label, "run")
	}
	return "Reply to " + aslFieldOr(state.target.Label, "run")
}

// aslResponseMetaLine describes the question without quoting it: which of the
// captured questions is selected, what kind of wait it is, and when it was
// asked.
func aslResponseMetaLine(state aslResponseState) string {
	parts := []string{}
	if total := len(state.questionIDs); total > 1 {
		parts = append(parts, fmt.Sprintf("question %d of %d", state.questionIdx+1, total))
	}
	if kind := strings.TrimSpace(state.contextKind); kind != "" {
		parts = append(parts, kind)
	}
	if asked := strings.TrimSpace(state.askedAt); asked != "" {
		parts = append(parts, "asked "+asked)
	}
	if len(parts) == 0 {
		return "waiting for a reply"
	}
	return strings.Join(parts, " · ")
}

// aslResponseBodyBlock renders the fetched question. The body is revealed only
// here, at the moment it is drawn, and never through a formatter that could
// reach a log, a toast, or an error string.
func aslResponseBodyBlock(state aslResponseState, width int) []string {
	if state.fetching {
		return []string{styleDim.Render("(loading the question…)")}
	}
	body := strings.TrimRight(state.body.Reveal(), "\n")
	if body == "" {
		return []string{styleDim.Render("(no question text)")}
	}
	var wrapped []string
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, layout.WrapHard(line, width)...)
	}
	if len(wrapped) > aslResponseBodyLines {
		wrapped = wrapped[:aslResponseBodyLines]
		wrapped = append(wrapped, styleDim.Render("… open the Workbench with w for the full question"))
	}
	return wrapped
}

// aslResponseEditorBlock renders the operator's draft with a visible caret. The
// draft is revealed only here for the same reason the body is.
func aslResponseEditorBlock(state aslResponseState, width int) []string {
	draft := state.drafts[state.target.QuestionID].Reveal()
	caret := ""
	if !state.focusButtons {
		caret = "▌"
	}
	var lines []string
	for _, line := range strings.Split(draft, "\n") {
		if line == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, layout.WrapHard(line, width)...)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	lines[len(lines)-1] += caret
	if len(lines) > aslResponseEditorLines {
		lines = lines[len(lines)-aslResponseEditorLines:]
	}
	if draft == "" && state.focusButtons {
		return []string{styleDim.Render("(empty)")}
	}
	return lines
}

func aslResponseButtonRow(state aslResponseState) string {
	cells := make([]string, 0, len(state.buttons))
	for i, button := range state.buttons {
		label := " " + button.label + " "
		if state.focusButtons && i == state.buttonIdx {
			label = styleSelected.Render(label)
		} else {
			label = styleDim.Render(label)
		}
		cells = append(cells, label)
	}
	return strings.Join(cells, " ")
}

// aslResponseStatusLines report the submission's identity-free state and any
// refusal. Both are fixed vocabularies, so neither can carry answer text.
func aslResponseStatusLines(state aslResponseState, width int) []string {
	var out []string
	if state.status != "" {
		out = append(out, styleDim.Render("status: "+state.status))
	}
	if state.notice != "" {
		for _, line := range layout.WrapHard(state.notice, width) {
			out = append(out, styleStateWarning.Render(line))
		}
	}
	return out
}
