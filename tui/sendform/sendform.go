// Package sendform is the dashboard's form for sending input to a session
// nobody is attached to: its state, the key vocabulary it forwards, and how
// it is drawn.
//
// It decides nothing about whether input may be written. The root package
// hands a submission to commands.Send, which is `amx send`, and shows what
// came back. What the form owns is the operator's side of that: a draft, the
// tail of the agent's screen so a prompt is answered with the prompt in view,
// and which half has the keyboard.
//
// The form has two halves because the two things an operator sends are
// different acts. Text is composed and then sent on purpose (ctrl+s), so
// Enter is a newline. A key is the answer to a prompt, so in the keys half
// each press is sent at once, and the form stays open on the screen that
// results, since one prompt often leads to the next.
//
// The draft is the operator's prompt. The root retains unsent drafts in memory
// per session. Text is drawn here and nowhere else: the
// State formats itself without it, so a model dump, a log line, or an error
// can never carry it.
//
// The package is a leaf: it names no Model and imports no surface.
package sendform

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

// State is everything the form holds. It is replaced wholesale when the form
// closes; the root keeps unsent drafts separately in process-local storage.
type State struct {
	Active bool
	// Target is the row the form was opened on. It is never rewritten while
	// the form is open, so a refresh cannot retarget a send.
	Target session.RunningSession
	// Title is how the form names the session.
	Title string
	// Draft is the text being composed.
	Draft string
	// Cursor is a byte offset at a grapheme boundary.
	Cursor int
	// FocusKeys says the keys half has the keyboard.
	FocusKeys bool
	// Screen is the tail of the agent's screen, and ScreenErr why there is
	// none. ScreenLoaded is false until the first read answers.
	Screen       string
	ScreenErr    string
	ScreenLoaded bool
	// InFlight says a send has not answered yet. Seq numbers the sends and
	// screen reads this form made, so an answer to an earlier form is dropped.
	InFlight bool
	Seq      int
	// Confirmed is set once the operator was told the session runs an
	// agent-session-loop run; the next send says so.
	Confirmed bool
	// Notice is the last thing the form has to say, and Warn whether it is a
	// refusal or a failure and not a report of success.
	Notice string
	Warn   bool
}

// String renders the state without the draft or the screen.
func (s State) String() string {
	return fmt.Sprintf("sendform.State{active:%t session:%s draftBytes:%d keys:%t inFlight:%t confirmed:%t}",
		s.Active, s.Target.RuntimeSessionID, len(s.Draft), s.FocusKeys, s.InFlight, s.Confirmed)
}

// GoString is String, so %#v is as safe as %v.
func (s State) GoString() string { return s.String() }

// keys maps what a terminal reports for a key press to the name `amx send`
// takes for it. Tab and Escape are in that vocabulary and not in this map:
// here they move between the form's halves and close it, and an agent that
// needs one of them is attached to instead.
var keys = map[string]string{
	"enter": "enter", "up": "up", "down": "down", "left": "left", "right": "right",
	"space": "space", "y": "y", "n": "n",
	"0": "0", "1": "1", "2": "2", "3": "3", "4": "4", "5": "5", "6": "6", "7": "7", "8": "8", "9": "9",
}

// KeyFor is the name the form sends for a key press, or false for a key it
// does not forward.
func KeyFor(pressed string) (string, bool) {
	name, ok := keys[pressed]
	return name, ok
}

const (
	innerWidth  = 84
	editorLines = 6
)

var (
	styleHeader  = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleWarning = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleBox     = theme.Popup()
)

// Width is the inner width the form draws at on a terminal this wide.
func Width(terminalWidth int) int {
	width := innerWidth
	if avail := terminalWidth - 4; terminalWidth > 0 && avail < width {
		width = avail
	}
	return max(width, 1)
}

// Render draws the form inside the overlay border.
func Render(state State, terminalWidth, terminalHeight int) string {
	width := Width(terminalWidth)
	height := terminalHeight - styleBox.GetVerticalFrameSize()
	if terminalHeight <= 0 {
		height = 40
	}
	return styleBox.Width(width + styleBox.GetHorizontalFrameSize()).Render(strings.TrimRight(render(state, width, height), "\n"))
}

func render(state State, width, height int) string {
	title := state.Title
	if title == "" {
		title = "session"
	}
	heading := styleHeader.Render(layout.Truncate("Send to "+title, width, "…"))
	if height < 8 {
		return heading + "\n" + styleDim.Render(layout.Truncate("Enlarge terminal to edit", width, "…"))
	}
	notice := []string{}
	if state.InFlight {
		notice = []string{styleDim.Render("sending…")}
	} else if state.Notice != "" {
		style := styleDim
		if state.Warn {
			style = styleWarning
		}
		notice = layoutLines(style, state.Notice, width)
		if len(notice) > 3 {
			notice = append(notice[:2], style.Render(layout.Truncate("More in Action Feedback", width, "…")))
		}
	}
	// Allocate screen and draft together so a full 14-line capture cannot push
	// the editor below a short terminal. The caret remains inside the draft window.
	fixed := 5 + len(notice)
	if state.FocusKeys {
		fixed++
	}
	available := max(2, height-fixed)
	editorHeight := min(editorLines, max(1, available/2))
	screenHeight := max(1, available-editorHeight)
	screen := screenBlock(state, width)
	if len(screen) > screenHeight {
		screen = screen[len(screen)-screenHeight:]
	}
	lines := []string{heading, "", styleDim.Render("Its screen")}
	lines = append(lines, screen...)
	if state.FocusKeys {
		lines = append(lines, styleHeader.Render("Keys ▌"), styleDim.Render(layout.Truncate("0-9 y n ↑↓←→ space enter · sent immediately", width, "…")), styleDim.Render("Text"))
	} else {
		lines = append(lines, styleDim.Render("Keys"), styleHeader.Render(layout.Truncate("Text · Enter: newline · arrows: move", width, "…")))
	}
	lines = append(lines, editorBlockHeight(state, width, editorHeight)...)
	lines = append(lines, notice...)
	return strings.Join(lines, "\n")
}

// screenBlock is the tail of the agent's screen, clipped to the form's width.
// Rows are cut, not wrapped: a wrapped row would move every row below it and
// the screen would stop looking like the screen.
func screenBlock(state State, width int) []string {
	switch {
	case !state.ScreenLoaded:
		return []string{styleDim.Render("(reading…)")}
	case state.ScreenErr != "":
		return layoutLines(styleWarning, "could not be read: "+state.ScreenErr, width)
	case strings.TrimSpace(state.Screen) == "":
		return []string{styleDim.Render("(blank)")}
	}
	rows := strings.Split(state.Screen, "\n")
	for i, row := range rows {
		if clipped := layout.WrapHard(row, width); len(clipped) > 0 {
			rows[i] = clipped[0]
		}
	}
	return rows
}

func layoutLines(style lipgloss.Style, text string, width int) []string {
	var out []string
	for _, line := range layout.WrapHard(text, width) {
		out = append(out, style.Render(line))
	}
	return out
}

// editorBlock is the draft with a caret while the text half has the keyboard.
func editorBlock(state State, width int) []string {
	return editorBlockHeight(state, width, editorLines)
}

func editorBlockHeight(state State, width, height int) []string {
	if state.Draft == "" && state.FocusKeys {
		return []string{styleDim.Render("(empty)")}
	}
	rows := editorRows(state.Draft, width)
	cursor := min(max(state.Cursor, 0), len(state.Draft))
	active := cursorRow(rows, cursor)
	visible := max(1, height-2)
	start := max(0, active-visible+1)
	end := min(len(rows), start+visible)
	var lines []string
	if start > 0 && height >= 3 {
		lines = append(lines, styleDim.Render(layout.Truncate("↑ more text", width, "…")))
	}
	for i := start; i < end; i++ {
		row := rows[i]
		text := state.Draft[row.start:row.end]
		if !state.FocusKeys && i == active {
			at := min(cursor, row.end) - row.start
			text = text[:at] + "▌" + text[at:]
		}
		if text == "" {
			lines = append(lines, "")
		} else {
			lines = append(lines, layout.Truncate(text, width, ""))
		}
	}
	if end < len(rows) && height >= 3 {
		lines = append(lines, styleDim.Render(layout.Truncate("↓ more text", width, "…")))
	}
	return lines
}
