package asl

// This file owns the Runs view's availability verdict and its geometry: how
// trustworthy the held projection is, how wide the terminal is willing to be,
// and how many rows are left once the notices and the detail panel have been
// charged. The header, the banner, and the body all read the same verdict, so
// they can never disagree about whether the data on screen is trustworthy.

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// WideThreshold is the terminal width at or above which the Runs list shows
// every column inline. Below it the row keeps only what an operator needs to
// triage — state, label, age, reason — and the rest moves to the selected-row
// panel instead of being truncated away.
const WideThreshold = 100

// ViewState is the single availability verdict the header, banner, and body all
// read, so they can never disagree about whether data is trustworthy.
type ViewState int

const (
	ViewLoading ViewState = iota
	ViewLive
	ViewStale
	ViewUnavailable
)

// RunsInput is everything the Runs view reads. The root projects its model onto
// one of these per render, which is what keeps this package free of the Bubble
// Tea model.
//
// BodyHeight is charged by the root rather than derived here: it subtracts the
// root's own header, footer, and status-bar chrome, none of which this leaf
// knows about.
type RunsInput struct {
	Rows   []aslbridge.Row
	Cursor int
	Offset int
	Width  int

	// Height is the terminal's own height and BodyHeight is what is left of it
	// once the root has charged its header, footer, and status bar. They are
	// separate because a terminal that is merely too short to hold both the
	// chrome and a row still shows one row, while a terminal that has reported
	// no height at all shows none.
	Height     int
	BodyHeight int

	// Loaded distinguishes "no snapshot yet" from "a snapshot with no rows";
	// Err and Status carry why a snapshot is missing or read-only.
	Loaded  bool
	Loading bool
	Err     error
	Meta    aslbridge.SnapshotMeta
	Status  aslbridge.Status

	// Notice explains the outcome of a deliberate action. It is kept apart from
	// Err because Err also decides whether the held snapshot is trustworthy.
	Notice     string
	Operations []OperationView

	// Sessions is the live-session list the binding line is resolved against.
	Sessions []session.RunningSession

	// Now is the reference instant for snapshot ageing and row ages. A zero
	// value falls back to the wall clock, so a caller that does not care need
	// not set it.
	Now time.Time
}

func (in RunsInput) now() time.Time {
	if in.Now.IsZero() {
		return time.Now()
	}
	return in.Now
}

// View reports how trustworthy the held projection is right now.
func (in RunsInput) View() ViewState {
	switch {
	case !in.Loaded && in.Err == nil:
		return ViewLoading
	case !in.Loaded:
		return ViewUnavailable
	case in.Err == nil && in.Status.Available && !in.Meta.Stale:
		return ViewLive
	case len(in.Rows) > 0 && in.snapshotAge() <= aslbridge.StaleWindow:
		return ViewStale
	default:
		return ViewUnavailable
	}
}

// snapshotAge measures how long the held projection has been in hand.
// ReceivedAt is preferred because it keeps ageing while the bridge is down;
// AgeSeconds is the fallback when a snapshot arrived without one.
func (in RunsInput) snapshotAge() time.Duration {
	if !in.Meta.ReceivedAt.IsZero() {
		if age := in.now().Sub(in.Meta.ReceivedAt); age > 0 {
			return age
		}
		return 0
	}
	if in.Meta.AgeSeconds > 0 {
		return time.Duration(in.Meta.AgeSeconds) * time.Second
	}
	return 0
}

// NeedsActionCount reports how many rows ASL projected into the needs-action
// urgency group.
func NeedsActionCount(rows []aslbridge.Row) int {
	count := 0
	for _, row := range rows {
		if row.UrgencyGroup == "needs-action" {
			count++
		}
	}
	return count
}

// Wide reports whether the list has room for every column inline.
func (in RunsInput) Wide() bool {
	return in.Width <= 0 || in.Width >= WideThreshold
}

// VisibleHeight is how many run rows fit once the notices and the selected-row
// detail panel have been charged against the body.
func (in RunsInput) VisibleHeight() int {
	if in.Height <= 0 {
		return 0
	}
	chrome := 2 + in.noticeLines() + len(in.detailPanel())
	available := in.BodyHeight - chrome
	if available < 1 {
		return 1
	}
	return available
}

func (in RunsInput) noticeLines() int {
	width := layout.WrapWidth(in.Width)
	lines := 0
	if banner := in.BannerText(); banner != "" {
		lines += len(layout.WrapHard(banner, width))
	}
	if in.Err != nil {
		lines += len(layout.WrapHard(in.Err.Error(), width))
	}
	if in.Notice != "" {
		lines += len(layout.WrapHard(in.Notice, width))
	}
	lines += len(in.operationLines())
	return lines
}

// operationLines reports the tracked submissions as identity and state only.
// A receipt outlives the form that produced it, which is how an accepted answer
// is seen to become consumed, so nothing here may carry the answer itself.
func (in RunsInput) operationLines() []string {
	if len(in.Operations) == 0 {
		return nil
	}
	out := make([]string, 0, len(in.Operations))
	for _, op := range in.Operations {
		out = append(out, fmt.Sprintf("  %s: %s", MethodLabel(op.Method), op.State))
	}
	return out
}

// BannerText is the read-only warning a stale snapshot carries, and the empty
// string at every other availability verdict.
func (in RunsInput) BannerText() string {
	if in.View() != ViewStale {
		return ""
	}
	return "read-only: showing the last agent-session-loop snapshot"
}

// SelectedRow returns the run under the cursor.
func (in RunsInput) SelectedRow() (aslbridge.Row, bool) {
	if in.Cursor < 0 || in.Cursor >= len(in.Rows) {
		return aslbridge.Row{}, false
	}
	return in.Rows[in.Cursor], true
}
