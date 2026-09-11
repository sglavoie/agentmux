// Where the inspector's output pane is looking, and the single question that
// position has to answer: is it following the newest snapshot, or holding an
// older one still?
//
// The distinction exists because the pane's content is REPLACED rather than
// appended to. A scrollback read returns the whole buffer every refresh
// interval, so "scroll up three lines" against a live target means scrolling
// within a string that is about to be thrown away — the line the operator was
// reading would be somewhere else, or gone, before they finished reading it.
// Holding a copy of the snapshot that was on screen when the scroll happened is
// what makes an upward scroll mean anything at all.
//
// Everything here is a pure function of the value plus the arguments it is
// given. The viewport reads no clock, holds no reference to a model, and never
// asks for a snapshot — the caller supplies the newest one on every call, which
// is what lets the whole hold/resume contract be asserted without a terminal, a
// Kitty client, or a running dashboard.

package dashboard

import "strings"

// OutputViewport is the inspector's scroll position over one session's terminal
// output.
//
// Two states. FOLLOWING is the resting one: the pane shows the tail of whatever
// snapshot arrived last, so the newest line is always the bottom line and a
// refresh moves the text under the reader. HOLDING is what an upward scroll
// buys: the snapshot on screen at that moment is copied and kept, later
// snapshots are ignored, and the pane reports itself as Paused until the
// operator resumes.
//
// The zero value follows. That is deliberate and load-bearing in two places: a
// fresh selection resets to the tail by assigning the zero value, and a caller
// that has not yet grown a scroll binding — every caller today — gets the
// historical tail-following pane without saying anything.
type OutputViewport struct {
	// holding reports the held state. It is a separate bit rather than a nil
	// check on held, because a session whose scrollback is legitimately empty can
	// be held on an empty snapshot, and "held nothing" and "holding nothing" are
	// different answers to whether the pane is live.
	holding bool
	// held is the snapshot copy the pane is showing while holding. It is never
	// read while following.
	held []string
	// top is the first visible line index within held. Meaningless while
	// following, where the top is always derived from the tail.
	top int
}

// Paused reports whether the pane is holding a snapshot rather than following
// the newest one. It is the renderer's input for the Paused indication and the
// one piece of viewport state a surface outside this package needs to read.
func (v OutputViewport) Paused() bool { return v.holding }

// Window is the slice of output the pane should draw: the last height lines of
// the newest snapshot while following, and the held snapshot's current window
// while holding.
//
// latest is ignored entirely while holding. That is the hold's whole promise —
// a refresh that lands under a paused pane changes nothing on screen — and it
// is why the snapshot is copied into the value rather than re-sliced out of
// whatever the caller passes next.
func (v OutputViewport) Window(latest []string, height int) []string {
	if height <= 0 {
		return nil
	}
	source := latest
	top := 0
	if v.holding {
		source = v.held
		top = clampTop(v.top, len(source), height)
	} else {
		top = maxTop(len(source), height)
	}
	end := top + height
	if end > len(source) {
		end = len(source)
	}
	if top >= end {
		return nil
	}
	return source[top:end]
}

// ScrollUp moves the window up by n lines, entering the held state first when
// the pane was following.
//
// Scrolling up on content that already fits the pane whole is a no-op that does
// NOT pause. There is nothing above the first line to reach, so pausing would
// stop the pane following live output in exchange for a scroll position it
// never left — a silent trade the operator did not ask for.
func (v OutputViewport) ScrollUp(latest []string, height, n int) OutputViewport {
	if height <= 0 || n <= 0 {
		return v
	}
	if !v.holding {
		if maxTop(len(latest), height) == 0 {
			return v
		}
		v = v.hold(latest, maxTop(len(latest), height))
	}
	v.top = clampTop(v.top-n, len(v.held), height)
	return v
}

// ScrollDown moves the window down by n lines within the held snapshot.
//
// It is a no-op while following: following already shows the tail, and there is
// nothing below it. Reaching the bottom of the held snapshot does not resume
// either — the bottom of a snapshot taken a minute ago is not the newest line,
// and silently swapping one for the other would move the text under a reader
// who asked for nothing but the next line down. Resume is the operator's word,
// and it has its own method.
func (v OutputViewport) ScrollDown(height, n int) OutputViewport {
	if !v.holding || height <= 0 || n <= 0 {
		return v
	}
	v.top = clampTop(v.top+n, len(v.held), height)
	return v
}

// Top holds the snapshot and moves to its first line — the pane's Home.
//
// Like ScrollUp it declines to pause content that already fits: the first line
// of a snapshot shorter than the pane is already on screen.
func (v OutputViewport) Top(latest []string, height int) OutputViewport {
	if height <= 0 {
		return v
	}
	if !v.holding {
		if maxTop(len(latest), height) == 0 {
			return v
		}
		v = v.hold(latest, 0)
		return v
	}
	v.top = 0
	return v
}

// Resume drops the held snapshot and follows the newest one again — the pane's
// End, and what a changed selection does by assigning the zero value.
func (v OutputViewport) Resume() OutputViewport { return OutputViewport{} }

// hold captures a snapshot copy at a starting position. The copy is defensive:
// the caller's slice is built from a string it is free to replace, and a held
// window aliasing it would quietly stop being held.
func (v OutputViewport) hold(latest []string, top int) OutputViewport {
	held := make([]string, len(latest))
	copy(held, latest)
	return OutputViewport{holding: true, held: held, top: top}
}

// maxTop is the largest first-visible index that still fills the pane, and zero
// when the content does not overflow it.
func maxTop(total, height int) int {
	if total <= height {
		return 0
	}
	return total - height
}

// clampTop bounds a first-visible index to the positions that exist.
func clampTop(top, total, height int) int {
	if top < 0 {
		return 0
	}
	if limit := maxTop(total, height); top > limit {
		return limit
	}
	return top
}

// SplitOutput turns a raw scrollback capture into the lines the viewport
// addresses.
//
// Three things happen here rather than in the renderer, because all three
// decide how many LINES there are and the viewport's arithmetic is counted in
// lines. A capture's trailing newline is chrome and not a final empty line. An
// empty capture is zero lines rather than the one empty line strings.Split
// reports, which is what lets a legitimately empty buffer be told apart from a
// buffer with a blank line in it. And a CRLF capture's carriage returns are
// dropped, because a stray \r inside a rendered row is a cursor movement the
// terminal will obey — it would redraw the line on top of itself, and no amount
// of width arithmetic downstream would notice.
func SplitOutput(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.ReplaceAll(line, "\r", "")
	}
	return lines
}
