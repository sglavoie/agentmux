// One read of one live terminal, clipped to the tile it is going to be drawn in.
//
// This is the overview's counterpart to the inspector's own preview fetch, and
// the difference between the two is the CLIPPING. The inspector reads one window
// and hands the whole scrollback to a viewport that can scroll back through it; a
// grid reads several windows at once, shows only the last few lines of each, and
// can never scroll. So the cut is made HERE, inside the command's goroutine,
// rather than in the update loop that receives it: splitting a multi-megabyte
// capture is proportional to the scrollback and would otherwise be paid on the
// thread that paints frames.
//
// The clip is also what makes the message safe to keep. strings.Split hands back
// substrings that share the capture's backing array, so a tail slice of a
// scrollback retains the entire scrollback — a per-window leak that would grow
// with the operator's terminal history rather than with what is on screen. Every
// retained line is therefore cloned, and the result is bounded by the tile's own
// width and height.
//
// A caller that has stopped caring about a read drops the answer.

package commands

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// CaptureTickMsg is the capture cadence, which is deliberately not TickMsg.
//
// The overview polls its topology on the dashboard's refresh interval and its
// terminal output on the preview interval, and those are different numbers —
// seconds against milliseconds. One message type for both would make every
// capture tick also ask for a window tree.
type CaptureTickMsg struct{}

// CaptureTick schedules a single CaptureTickMsg after d.
func CaptureTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return CaptureTickMsg{}
	})
}

// CaptureRequest is one scheduled read: what to read, how to stamp it, and how
// big the tile it is destined for is.
//
// Generation and RequestID answer different questions and are both carried back
// on the reply. Generation says what the tile was POINTED AT when the read was
// issued, and decides whether the text may be painted. RequestID says which read
// this is, and decides only whether the scheduler's in-flight slot may be
// released. A caller that settled on the generation alone would strand the slot
// of every read it had already stopped wanting.
type CaptureRequest struct {
	// Target is the pane to read, in whichever backend hosts it. It is the
	// identity the scheduler keys on.
	Target     session.PaneTarget
	Generation int
	RequestID  int
	// Width and Height are the tile's output region in cells and lines. The
	// reply is clipped to them, so they are also the bound on what the caller
	// ends up holding.
	Width, Height int
}

// ErrCaptureUnavailable reports a target whose backend has no reader wired
// here. It is a failed read like any other: the reply still releases its slot,
// and the tile shows the failure instead of content.
var ErrCaptureUnavailable = errors.New("capture unavailable for this backend")

// CaptureSources is what the surface can read panes through.
type CaptureSources struct {
	Panes map[runtimesession.BackendKind]backend.Capturer
}

// CaptureScreen reads one target's scrollback through the source for its
// backend and returns its clipped tail. A target with no source fails with
// ErrCaptureUnavailable. Every path replies.
func CaptureScreen(sources CaptureSources, req CaptureRequest) tea.Cmd {
	if req.Target.IsZero() || req.Target.Backend != runtimesession.BackendTmux {
		return func() tea.Msg { return CaptureOutputMsg{Request: req, Err: ErrCaptureUnavailable} }
	}
	return func() tea.Msg {
		capturer := sources.Panes[req.Target.Backend]
		if capturer == nil {
			return CaptureOutputMsg{Request: req, Err: fmt.Errorf("%w: %s", ErrCaptureUnavailable, req.Target.Backend)}
		}
		text, err := capturer.Capture(req.Target.Pane, backend.ExtentAll)
		if err != nil {
			return CaptureOutputMsg{Request: req, Err: err}
		}
		return CaptureOutputMsg{Request: req, Lines: ClipCapture(text, req.Width, req.Height)}
	}
}

// CaptureOutputMsg is one completed read. It always comes back — the request it
// answers is echoed whole — which is what lets the caller treat a reply as the
// release of the slot its request took.
type CaptureOutputMsg struct {
	Request CaptureRequest
	// Lines is the clipped visible tail, newest last, already split by
	// dashboard.SplitOutput. Nil under Err, and legitimately nil for a terminal
	// with nothing in it.
	Lines []string
	Err   error
}

// ClipCapture cuts a raw scrollback down to the last height lines, each at most
// width display cells, and returns lines that retain nothing of the original.
//
// The three steps are each load-bearing. The SPLIT is dashboard.SplitOutput, so
// the overview counts lines exactly as the inspector does and an empty buffer
// stays zero lines rather than one blank one. The TAIL is the zero
// dashboard.OutputViewport, which is what "follow the newest output" already
// means for a surface with no scroll position. And the CLONE is what turns a
// window onto the scrollback into a copy of the part that is visible: both
// strings.Split and layout.StyledTruncate hand back the input's own memory when
// nothing has to change, so without it a nine-line tile would keep its session's
// entire history alive.
func ClipCapture(text string, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	tail := dashboard.OutputViewport{}.Window(dashboard.SplitOutput(text), height)
	if len(tail) == 0 {
		return nil
	}
	out := make([]string, len(tail))
	for i, line := range tail {
		out[i] = strings.Clone(layout.StyledTruncate(line, width, "…"))
	}
	return out
}
