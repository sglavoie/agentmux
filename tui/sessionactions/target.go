// This file is the target contract: which session an action was aimed at, as
// captured at the moment the operator chose it.

package sessionactions

import (
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// Target is the session an action acts on, captured rather than looked up. The
// row is copied when the target is made, so a background topology refresh or a
// cursor the operator has since moved cannot retarget an action between the
// moment it was chosen and the moment it runs.
//
// The zero Target is "nothing was captured", which is what every availability
// rule refuses against. That is the whole fail-closed story: a synthetic row —
// a family summary, an empty list — cannot become a target, because it never
// produces one.
type Target struct {
	// HasSession records that a real row was captured. It is separate from the
	// session value because a zero RunningSession is a legitimate reading of a
	// live window whose fields have not arrived yet, and the two must not be
	// confused.
	HasSession bool
	// Session is the captured row.
	Session session.RunningSession
}

// Capture snapshots a live row as an action target.
func Capture(s session.RunningSession) Target {
	return Target{HasSession: true, Session: s}
}

// FromContext adapts the palette's generic command context, which captures the
// same row for every command family rather than for session actions alone.
func FromContext(ctx commands.Context) Target {
	if !ctx.HasTarget {
		return Target{}
	}
	return Capture(ctx.Target)
}

// Context projects the target back onto the palette's generic context so the
// availability gates in tui/commands stay the single implementation of the
// rules both surfaces read.
func (t Target) Context() commands.Context {
	if !t.HasSession {
		return commands.Context{}
	}
	return commands.Context{Target: t.Session, HasTarget: true}
}

// TabID is the captured Kitty tab the action addresses, or 0 when nothing was
// captured. It is read from the captured row rather than stored beside it, so
// there is no second copy to fall out of agreement with the session.
func (t Target) TabID() int {
	if !t.HasSession {
		return 0
	}
	return t.Session.TabID
}

// WindowID is the captured Kitty window the action addresses, or 0 when
// nothing was captured.
func (t Target) WindowID() int {
	if !t.HasSession {
		return 0
	}
	return t.Session.WindowID
}
