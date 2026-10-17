package commands

import (
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/session"
)

// Sending input to a session from a surface: answering the prompt an agent
// is showing, or handing it its next prompt, without attaching.
//
// The surface decides nothing about whether a pane may be written to. It asks
// through Portable.Send, which is `amx send`, so the dashboard is held to the
// screen rules, the agent-session-loop guard, and the once-only record the
// command line is held to, and a refusal comes back as the same code.

// SendRequest is one input for one session: Text, or Keys by name.
type SendRequest struct {
	Text string
	Keys []string
	// Confirmed says the operator was told the session runs an
	// agent-session-loop run and sent anyway.
	Confirmed bool
}

// SendReport is what a send that was written reports.
type SendReport struct {
	// Delivery is accepted, consumed, or replayed.
	Delivery string
	// Screen is what the agent's screen said before it was written to.
	Screen string
}

// SendRefusal is a send that was decided against, by code. Nothing was
// written.
type SendRefusal struct {
	Code    string
	Message string
}

func (e *SendRefusal) Error() string { return e.Message }

// The refusal a surface answers differently from the rest: it is not a
// failure but a question to put to the operator.
const SendRefusedConfirmation = "confirmation_required"

// ErrNoSend reports a surface that was started with no way to send.
var ErrNoSend = errors.New("this surface cannot send to sessions")

// SendDoneMsg reports one send. Seq identifies the operation even after its
// form closes; the root retains its outcome without updating a newer form.
type SendDoneMsg struct {
	Seq    int
	Keys   bool
	Report SendReport
	Err    error
}

// SendScreenMsg carries the tail of a session's screen for the send form.
type SendScreenMsg struct {
	Seq  int
	Text string
	Err  error
}

// SendAvailability refuses sending to a row that is not a live tmux session,
// and to one whose agent-session-loop question is open: that answer belongs
// to the run, and the Runs view is where it is given.
func SendAvailability(ctx Context) Availability {
	if availability := SessionAvailability(ctx); !availability.Available {
		return availability
	}
	switch {
	case !IsPortable(ctx.Target) || ctx.Target.RuntimeSessionID == "":
		return Unavailable(ReasonSendNeedsTmux)
	case PortableLost(ctx.Target):
		return Unavailable(ReasonSendLost)
	case len(ctx.Target.ASLPhases) > 0 && ctx.Target.State == "question":
		return Unavailable(ReasonSendASLQuestion)
	}
	return Available()
}

// Send writes req to rs's agent and reports under seq.
func Send(rs session.RunningSession, req SendRequest, seq int) tea.Cmd {
	send := Portable.Send
	return func() tea.Msg {
		msg := SendDoneMsg{Seq: seq, Keys: len(req.Keys) > 0}
		if send == nil {
			msg.Err = ErrNoSend
			return msg
		}
		msg.Report, msg.Err = send(rs, req)
		return msg
	}
}

// SendScreenLines is how much of the screen's tail the send form shows.
const SendScreenLines = 14

// SendScreen reads the tail of rs's screen after delay, which gives an agent
// that was just written to time to redraw.
func SendScreen(rs session.RunningSession, seq int, delay time.Duration) tea.Cmd {
	capturer := Portable.Tmux
	return func() tea.Msg {
		if capturer == nil {
			return SendScreenMsg{Seq: seq, Err: ErrNoTerminal}
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		text, err := capturer.Capture(rs.PaneRef(), backend.ExtentScreen)
		return SendScreenMsg{Seq: seq, Text: screenTail(text, SendScreenLines), Err: err}
	}
}

// screenTail is the last n lines of a screen that hold anything, with the
// blank rows below the program's last line dropped.
func screenTail(screen string, n int) string {
	lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
