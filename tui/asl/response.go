package asl

// This file paints the response modal. It is the only place a fetched question
// body or an operator draft is revealed: both are read at the moment they are
// drawn and never handed to a formatter that could reach a log, a toast, or an
// error string.

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
)

// ResponseInnerWidth is the widest the response form draws. It is clamped to
// the terminal on narrow layouts so the box never overflows.
const ResponseInnerWidth = 76

// responseBodyLines bounds how much of a question is shown at once. The
// remainder is reachable by opening the Workbench; the form is for answering,
// not for reading an entire transcript.
const responseBodyLines = 10

// responseEditorLines bounds the visible height of the answer editor.
const responseEditorLines = 6

// ResponseWidth is the inner width the form draws at on a terminal this wide.
func ResponseWidth(terminalWidth int) int {
	width := ResponseInnerWidth
	if avail := terminalWidth - 4; terminalWidth > 0 && avail < width {
		width = avail
	}
	if width < 1 {
		width = 1
	}
	return width
}

// RenderResponseBox draws the form inside the overlay border.
func RenderResponseBox(state ResponseState, terminalWidth int) string {
	width := ResponseWidth(terminalWidth)
	return styleOverlayBox.Width(width).Render(strings.TrimRight(RenderResponse(state, width), "\n"))
}

// RenderResponse draws the form body at an already-clamped inner width.
func RenderResponse(state ResponseState, width int) string {
	var sb strings.Builder

	sb.WriteString(styleHeader.Render(responseTitle(state)))
	sb.WriteByte('\n')
	for _, line := range layout.WrapHard(responseMetaLine(state), width) {
		sb.WriteString(styleDim.Render(line))
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	for _, line := range responseBodyBlock(state, width) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	sb.WriteString(styleDim.Render("Answer"))
	sb.WriteByte('\n')
	for _, line := range responseEditorBlock(state, width) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	sb.WriteString(responseButtonRow(state))
	sb.WriteByte('\n')

	for _, line := range responseStatusLines(state, width) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func responseTitle(state ResponseState) string {
	if state.Kind == ResponsePaused {
		return "Resume " + fieldOr(state.Target.Label, "run")
	}
	return "Reply to " + fieldOr(state.Target.Label, "run")
}

// responseMetaLine describes the question without quoting it: which of the
// captured questions is selected, what kind of wait it is, and when it was
// asked.
func responseMetaLine(state ResponseState) string {
	parts := []string{}
	if total := len(state.QuestionIDs); total > 1 {
		parts = append(parts, fmt.Sprintf("question %d of %d", state.QuestionIdx+1, total))
	}
	if kind := strings.TrimSpace(state.ContextKind); kind != "" {
		parts = append(parts, kind)
	}
	if asked := strings.TrimSpace(state.AskedAt); asked != "" {
		parts = append(parts, "asked "+asked)
	}
	if len(parts) == 0 {
		return "waiting for a reply"
	}
	return strings.Join(parts, " · ")
}

// responseBodyBlock renders the fetched question. The body is revealed only
// here, at the moment it is drawn, and never through a formatter that could
// reach a log, a toast, or an error string.
func responseBodyBlock(state ResponseState, width int) []string {
	if state.Fetching {
		return []string{styleDim.Render("(loading the question…)")}
	}
	body := strings.TrimRight(state.Body.Reveal(), "\n")
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
	if len(wrapped) > responseBodyLines {
		wrapped = wrapped[:responseBodyLines]
		wrapped = append(wrapped, styleDim.Render("… open the Workbench with w for the full question"))
	}
	return wrapped
}

// responseEditorBlock renders the operator's draft with a visible caret. The
// draft is revealed only here for the same reason the body is.
func responseEditorBlock(state ResponseState, width int) []string {
	draft := state.Drafts[state.Target.QuestionID].Reveal()
	caret := ""
	if !state.FocusButtons {
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
	if len(lines) > responseEditorLines {
		lines = lines[len(lines)-responseEditorLines:]
	}
	if draft == "" && state.FocusButtons {
		return []string{styleDim.Render("(empty)")}
	}
	return lines
}

func responseButtonRow(state ResponseState) string {
	cells := make([]string, 0, len(state.Buttons))
	for i, button := range state.Buttons {
		label := " " + button.Label + " "
		if state.FocusButtons && i == state.ButtonIdx {
			label = styleSelected.Render(label)
		} else {
			label = styleDim.Render(label)
		}
		cells = append(cells, label)
	}
	return strings.Join(cells, " ")
}

// responseStatusLines report the submission's identity-free state and any
// refusal. Both are fixed vocabularies, so neither can carry answer text.
func responseStatusLines(state ResponseState, width int) []string {
	var out []string
	if state.Status != "" {
		out = append(out, styleDim.Render("status: "+state.Status))
	}
	if state.Notice != "" {
		for _, line := range layout.WrapHard(state.Notice, width) {
			out = append(out, styleStateWarning.Render(line))
		}
	}
	return out
}
