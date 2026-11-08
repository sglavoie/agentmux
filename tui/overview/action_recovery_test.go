package overview

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// closeSelected drives the whole close interaction — menu, mnemonic,
// confirmation, and the completion reporting that the tab was observed to go.
func closeSelected(t *testing.T, m model, r *recorder) model {
	t.Helper()
	m = openMenu(t, m)
	m = press(t, m, "X")
	if m.acts.surface != surfaceConfirm {
		t.Fatalf("`X` left surface %v, want the confirmation", m.acts.surface)
	}
	m = press(t, m, "y")
	d, ok := r.lastDispatch()
	if !ok || d.Action != commands.CloseSession {
		t.Fatalf("the confirmed close dispatched %v", r.actionIDs())
	}
	return only(t, m, commands.ActionDoneMsg{Request: uint64(d.Request)})
}

// TestAConfirmedCloseIsOfferedAnUndoAndRelaunches is the recovery lifecycle end
// to end: reserve at dispatch, promote on a confirmed close, claim on `u`.
func TestAConfirmedCloseIsOfferedAnUndoAndRelaunches(t *testing.T) {
	sessions := viewSessions(4)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	closed := m.sessions[m.selected]

	m = closeSelected(t, m, r)
	if !m.recoverable() {
		t.Fatal("a confirmed close left nothing to undo")
	}
	offer := m.feedback()
	if !strings.HasPrefix(offer, "ended session ") || !strings.Contains(offer, "u relaunch (30s)") {
		t.Fatalf("the feedback row says %q, want the undo offer", offer)
	}
	// The offer is on the FEEDBACK row and nowhere else. A close is undoable for
	// thirty seconds; a permanent footer entry would go on naming `u` for the rest
	// of the session, which is the advertised-but-dead key this surface is not
	// allowed to have.
	if !strings.Contains(frameOf(m), "u relaunch (30s)") {
		t.Fatalf("the frame does not carry the undo offer:\n%s", frameOf(m))
	}
	if hints := hintRowOf(m); strings.Contains(hints, "undo") {
		t.Fatalf("the footer advertises the undo: %q", hints)
	}

	// The grid reconciles onto the nearest survivor when the close lands.
	m = only(t, m, commands.TickMsg{})
	survivors := append(append([]session.RunningSession{}, sessions[:1]...), sessions[2:]...)
	m = only(t, m, hosted(snapshot(m.refreshActiveSeq, 0, survivors), hostTabElsewhere))
	if m.selectedTabID != sessions[2].TabID {
		t.Fatalf("after the close the cursor is on tab %d, want the survivor that took its place (%d)", m.selectedTabID, sessions[2].TabID)
	}
	if !m.recoverable() {
		t.Fatal("the refresh that applied the close discarded its recovery")
	}

	m = press(t, m, "u")
	if len(r.relaunches) != 1 {
		t.Fatalf("`u` asked for %d relaunches, want 1", len(r.relaunches))
	}
	if got := r.relaunches[0].Session.Title; got != closed.EffectiveProject() {
		t.Fatalf("the relaunch names project %q, want the closed row's %q", got, closed.EffectiveProject())
	}
	if got := r.relaunches[0].Account.Name; got != closed.EffectiveProfile() {
		t.Fatalf("the relaunch names profile %q, want %q", got, closed.EffectiveProfile())
	}
	if m.recoverable() {
		t.Fatal("a claimed close is still offered; a second `u` would launch it twice")
	}

	// A second press takes nothing, and the outcome is reported.
	m = press(t, m, "u")
	if len(r.relaunches) != 1 {
		t.Fatalf("a second `u` asked for %d relaunches in total, want 1", len(r.relaunches))
	}
	m = only(t, m, recoveryDoneMsg{request: r.relaunchRequests[0], label: r.relaunches[0].Label})
	if got := m.feedback(); !strings.HasPrefix(got, "restored ") {
		t.Fatalf("the feedback row says %q, want the restoration", got)
	}
}

// TestAFailedRelaunchIsOfferedAgainForWhatWasLeftOfItsWindow. The deadline is the
// close's, not the retry's: an operator may retry for exactly as long as the
// close was going to stay undoable and no longer.
func TestAFailedRelaunchIsOfferedAgainForWhatWasLeftOfItsWindow(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	m = closeSelected(t, m, r)
	m = press(t, m, "u")

	m = only(t, m, recoveryDoneMsg{
		request: r.relaunchRequests[0],
		label:   r.relaunches[0].Label,
		err:     errors.New("launch failed"),
	})
	if !m.recoverable() {
		t.Fatal("a failed relaunch consumed the recovery; there is nothing left to retry")
	}
	// The released entry is back on the offer, so the failure has to displace the
	// offer rather than sit under it: redrawing "closed X · u relaunch" here would be
	// exactly what a dropped keystroke looks like.
	got := m.feedback()
	if !strings.Contains(got, "launch failed") {
		t.Fatalf("the feedback row says %q, want the failure", got)
	}
	if !strings.Contains(got, "u retry") {
		t.Fatalf("the feedback row says %q, want it to name the retry it is still granting", got)
	}

	// It expires with the window the close started, not with a fresh one.
	r.now = r.now.Add(sessionactions.RecoveryWindow + time.Second)
	if m.recoverable() {
		t.Fatal("the retry was granted a fresh thirty seconds")
	}
}

// TestReviewRecoveryRetryFeedbackDeadline is the retry hint against the close's
// own deadline rather than the report's.
//
// A released entry keeps the deadline its CLOSE was confirmed with, so the retry
// beside a failure has to be withdrawn at that deadline and not when the
// four-second report expires. The clock is injected, so each arrangement RENDERS
// the row at a chosen instant rather than asking the stack and trusting the two
// to agree: a failure inside the window read on both sides of the deadline, a
// failure that arrives after it, and a lapsed failure beside a close that is
// still recoverable — where the row has to name the entry `u` would actually
// take.
func TestReviewRecoveryRetryFeedbackDeadline(t *testing.T) {
	t.Run("a failure inside the window loses its retry at the deadline", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = closeSelected(t, m, r)
		confirmedAt := r.now

		// `u` late in the window, and the launch fails with half a second of it
		// left.
		r.now = confirmedAt.Add(sessionactions.RecoveryWindow - time.Second)
		m = press(t, m, "u")
		r.now = confirmedAt.Add(sessionactions.RecoveryWindow - 500*time.Millisecond)
		m = only(t, m, recoveryDoneMsg{
			request: r.relaunchRequests[0],
			label:   r.relaunches[0].Label,
			err:     errors.New("launch failed"),
		})

		if !m.recoverable() {
			t.Fatal("the failed relaunch left nothing claimable half a second before the deadline")
		}
		got := m.feedback()
		if !strings.Contains(got, "launch failed") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
		if !strings.Contains(got, "u retry (1s)") {
			t.Fatalf("the feedback row says %q, want the retry named for what is LEFT of the window", got)
		}

		// Past the close's own deadline. The failure is still worth reading — its
		// report runs four seconds from the failure — but `u` refuses from here on,
		// so the row must stop naming it.
		r.now = confirmedAt.Add(sessionactions.RecoveryWindow + 100*time.Millisecond)
		if m.recoverable() {
			t.Fatal("the retry was granted time past the close's deadline")
		}
		got = m.feedback()
		if !strings.Contains(got, "launch failed") {
			t.Fatalf("the feedback row says %q, want the failure to stay visible", got)
		}
		if strings.Contains(got, "retry") {
			t.Fatalf("the feedback row says %q, want the retry withdrawn with the window", got)
		}
		frame := frameOf(m)
		if !strings.Contains(frame, "launch failed") || strings.Contains(frame, "retry") {
			t.Fatalf("the frame disagrees with the feedback row:\n%s", frame)
		}
	})

	t.Run("a failure after the window names no retry at all", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = closeSelected(t, m, r)
		confirmedAt := r.now

		r.now = confirmedAt.Add(sessionactions.RecoveryWindow - time.Second)
		m = press(t, m, "u")
		// The launch takes two seconds to fail, which is one second past the
		// deadline the claim was made under. The entry is released onto an offer
		// that has already expired.
		r.now = confirmedAt.Add(sessionactions.RecoveryWindow + time.Second)
		m = only(t, m, recoveryDoneMsg{
			request: r.relaunchRequests[0],
			label:   r.relaunches[0].Label,
			err:     errors.New("launch failed"),
		})

		if m.recoverable() {
			t.Fatal("releasing an expired entry made it claimable again")
		}
		got := m.feedback()
		if !strings.Contains(got, "launch failed") {
			t.Fatalf("the feedback row says %q, want the failure", got)
		}
		if strings.Contains(got, "retry") {
			t.Fatalf("the feedback row says %q, want no retry offered for an entry Claim refuses", got)
		}
		// And the key agrees with the row it is not advertised on: it takes
		// nothing, and refusing does not put a retry back.
		m = press(t, m, "u")
		if len(r.relaunches) != 1 {
			t.Fatalf("`u` past the window asked for %d relaunches in total, want the 1 already made", len(r.relaunches))
		}
		if got := m.feedback(); strings.Contains(got, "retry") {
			t.Fatalf("the refused keypress put a retry back on the row: %q", got)
		}
	})

	t.Run("a lapsed failure does not displace a close that is still recoverable", func(t *testing.T) {
		sessions := viewSessions(4)
		m, r := actionModel(t, sessions, 1, hostTabElsewhere)
		m = closeSelected(t, m, r)
		confirmedAt := r.now

		// Claim the first close a second in, then close a second row a second
		// after that, so the two entries expire two seconds apart.
		r.now = confirmedAt.Add(time.Second)
		m = press(t, m, "u")
		if len(r.relaunches) != 1 {
			t.Fatalf("`u` asked for %d relaunches, want 1", len(r.relaunches))
		}
		r.now = confirmedAt.Add(2 * time.Second)
		m = press(t, m, "tab")
		m = closeSelected(t, m, r)

		// The first relaunch fails a second past ITS deadline, while the second
		// close stays recoverable for another second.
		r.now = confirmedAt.Add(sessionactions.RecoveryWindow + time.Second)
		m = only(t, m, recoveryDoneMsg{
			request: r.relaunchRequests[0],
			label:   r.relaunches[0].Label,
			err:     errors.New("launch failed"),
		})

		restorable, ok := m.acts.recovery.Latest(r.now)
		if !ok {
			t.Fatal("the second close stopped being recoverable when the first relaunch failed")
		}
		if restorable.Label == r.relaunches[0].Label {
			t.Fatalf("the fixture claimed the wrong entry: %q is the one that failed", restorable.Label)
		}
		got := m.feedback()
		if strings.Contains(got, "retry") {
			t.Fatalf("the feedback row says %q, want no retry for the lapsed entry", got)
		}
		if !strings.Contains(got, "u relaunch") || !strings.Contains(got, restorable.Label) {
			t.Fatalf("the feedback row says %q, want the offer naming %q — the close `u` would actually take", got, restorable.Label)
		}
		// And it takes exactly that one.
		m = press(t, m, "u")
		if len(r.relaunches) != 2 {
			t.Fatalf("`u` asked for %d relaunches in total, want 2", len(r.relaunches))
		}
		if got := r.relaunches[1].Label; got != restorable.Label {
			t.Fatalf("`u` restored %q, want the %q the row named", got, restorable.Label)
		}
		if got := m.feedback(); !strings.Contains(got, "restoring "+restorable.Label) {
			t.Fatalf("the feedback row says %q, want the restoration the keystroke just started", got)
		}
	})
}

// TestOnlyAConfirmedCloseIsRecoverable. Relaunching a session that is still
// running produces a second copy of it rather than an undo, so a close that
// failed and one nobody could confirm both leave nothing behind.
func TestOnlyAConfirmedCloseIsRecoverable(t *testing.T) {
	sessions := viewSessions(3)
	for _, tc := range []struct {
		name       string
		completion commands.ActionDoneMsg
	}{
		{name: "a failed close", completion: commands.ActionDoneMsg{Err: errors.New("no matching tab")}},
		{name: "an unconfirmed close", completion: commands.ActionDoneMsg{Unconfirmed: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r := actionModel(t, sessions, 1, hostTabElsewhere)
			m = openMenu(t, m)
			m = press(t, m, "X")
			m = press(t, m, "y")
			d, _ := r.lastDispatch()

			tc.completion.Request = uint64(d.Request)
			m = only(t, m, tc.completion)
			if m.recoverable() {
				t.Fatalf("%s was offered an undo", tc.name)
			}
			m = press(t, m, "u")
			if len(r.relaunches) != 0 {
				t.Fatalf("`u` after %s relaunched %d sessions", tc.name, len(r.relaunches))
			}
			if got := m.feedback(); got == "" {
				t.Fatalf("%s reported nothing", tc.name)
			}
		})
	}
}

// TestClosingTheHostingTabIsNeverRecoverable. Closing it takes the grid, the
// feedback row, and the thirty seconds the offer would have lasted with it, so an
// offer made there is an affordance that dies before it can be used.
func TestClosingTheHostingTabIsNeverRecoverable(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, sessions[1].TabID)

	m = closeSelected(t, m, r)
	if m.recoverable() {
		t.Fatal("closing the hosting tab was offered an undo")
	}
	if strings.Contains(m.feedback(), "u relaunch") {
		t.Fatalf("the feedback row offers an undo: %q", m.feedback())
	}
}

// TestAnExpiredOfferIsWithdrawn. The offer names a window, and the window is the
// stack's rather than this file's; a surface that went on drawing it would be
// promising something the stack has stopped granting.
func TestAnExpiredOfferIsWithdrawn(t *testing.T) {
	sessions := viewSessions(3)
	m, r := actionModel(t, sessions, 1, hostTabElsewhere)
	m = closeSelected(t, m, r)

	r.now = r.now.Add(sessionactions.RecoveryWindow + time.Millisecond)
	if m.recoverable() {
		t.Fatal("the offer outlived its window")
	}
	if strings.Contains(m.feedback(), "u relaunch") {
		t.Fatalf("the feedback row still offers the expired undo: %q", m.feedback())
	}
	m = press(t, m, "u")
	if len(r.relaunches) != 0 {
		t.Fatalf("`u` after expiry relaunched %d sessions", len(r.relaunches))
	}
	if got := m.feedback(); strings.Contains(got, "restoring") {
		t.Fatalf("the expired `u` says %q, want it to have claimed nothing", got)
	}
}
