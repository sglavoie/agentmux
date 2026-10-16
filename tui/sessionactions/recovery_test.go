package sessionactions_test

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// base is a fixed instant. Every deadline in this file is expressed against it,
// so nothing here sleeps and nothing depends on how long a test took.
var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func snapshot(label string) sessionactions.Snapshot {
	return sessionactions.Snapshot{
		Session: config.Session{Title: label},
		Account: config.Account{Name: "work"},
		Label:   label,
	}
}

// TestReservedCloseIsNotYetRecoverable is the premature-success guard: the
// snapshot is captured at dispatch, but nothing is offered until the close is
// confirmed.
func TestReservedCloseIsNotYetRecoverable(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(1, snapshot("api"))

	if got := r.Available(base); len(got) != 0 {
		t.Fatalf("available after reserve = %v, want none", got)
	}
	if _, _, ok := r.Claim(base); ok {
		t.Fatal("a reserved close was claimable before it was confirmed")
	}

	r, promoted := r.Confirm(1, base.Add(sessionactions.RecoveryWindow))
	if !promoted {
		t.Fatal("Confirm reported nothing to promote")
	}
	if got := r.Available(base); len(got) != 1 || got[0].Label != "api" {
		t.Fatalf("available after confirm = %v, want api", got)
	}
}

// TestTheWindowStartsAtConfirmationRatherThanDispatch is the whole reason the
// deadline is set by Confirm rather than Reserve.
func TestTheWindowStartsAtConfirmationRatherThanDispatch(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(1, snapshot("api"))

	// The close takes two seconds to confirm.
	confirmedAt := base.Add(2 * time.Second)
	r, _ = r.Confirm(1, confirmedAt.Add(sessionactions.RecoveryWindow))

	justBefore := confirmedAt.Add(sessionactions.RecoveryWindow - time.Millisecond)
	if len(r.Available(justBefore)) != 1 {
		t.Fatal("entry expired before thirty seconds had passed since the confirmation")
	}
	if len(r.Available(confirmedAt.Add(sessionactions.RecoveryWindow))) != 0 {
		t.Fatal("entry outlived its deadline")
	}
}

// TestUnconfirmedAndFailedClosesLeaveNothingBehind is the false-recovery guard.
func TestUnconfirmedAndFailedClosesLeaveNothingBehind(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(7, snapshot("api")).Discard(7)

	if got := r.Available(base); len(got) != 0 {
		t.Fatalf("available after discard = %v, want none", got)
	}
	// A completion arriving after the discard cannot resurrect it.
	if _, promoted := r.Confirm(7, base.Add(sessionactions.RecoveryWindow)); promoted {
		t.Fatal("a discarded close was promoted by a later confirmation")
	}
}

// TestRepeatedClosesKeepIndividualDeadlinesAndUndoNewestFirst covers the two
// properties multiple closes inside one window have to preserve.
func TestRepeatedClosesKeepIndividualDeadlinesAndUndoNewestFirst(t *testing.T) {
	r := sessionactions.Recovery{}
	for i, label := range []string{"first", "second", "third"} {
		request := sessionactions.RequestID(i + 1)
		at := base.Add(time.Duration(i) * time.Second)
		r = r.Reserve(request, snapshot(label))
		r, _ = r.Confirm(request, at.Add(sessionactions.RecoveryWindow))
	}

	// One second after the first entry expired, the two later ones are still
	// live: the deadlines are per entry, not one shared window.
	afterFirst := base.Add(sessionactions.RecoveryWindow + 500*time.Millisecond)
	available := r.Available(afterFirst)
	if len(available) != 2 || available[0].Label != "second" || available[1].Label != "third" {
		t.Fatalf("available after the first expired = %v, want second and third", available)
	}

	r, claimed, ok := r.Claim(afterFirst)
	if !ok || claimed.Snapshot.Label != "third" {
		t.Fatalf("claim = %+v ok=%v, want the newest entry", claimed, ok)
	}
	r = r.Restored(claimed.Request)

	_, next, ok := r.Claim(afterFirst)
	if !ok || next.Snapshot.Label != "second" {
		t.Fatalf("second claim = %+v ok=%v, want the next newest entry", next, ok)
	}
	if next.Request == claimed.Request {
		t.Fatal("two relaunches were minted the same request identity")
	}
}

// TestAClaimedEntryIsNotOfferedTwice is the repeated-undo guard: pressing undo
// twice while the first relaunch is still in flight must not launch it twice.
func TestAClaimedEntryIsNotOfferedTwice(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(1, snapshot("api"))
	r, _ = r.Confirm(1, base.Add(sessionactions.RecoveryWindow))

	r, first, ok := r.Claim(base)
	if !ok {
		t.Fatal("first claim refused")
	}
	if _, _, ok := r.Claim(base); ok {
		t.Fatal("an entry whose relaunch was in flight was claimed again")
	}
	if got := r.Available(base); len(got) != 0 {
		t.Fatalf("a claimed entry was still offered: %v", got)
	}

	// A failed relaunch puts it back, with the deadline it already had.
	r = r.Release(first.Request)
	if got := r.Available(base); len(got) != 1 {
		t.Fatalf("available after release = %v, want the entry back", got)
	}
	if len(r.Available(base.Add(sessionactions.RecoveryWindow))) != 0 {
		t.Fatal("a released entry outlived its original deadline")
	}
}

// TestRestoredEntryIsGoneForGood keeps a successful relaunch from being undone a
// second time.
func TestRestoredEntryIsGoneForGood(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(1, snapshot("api"))
	r, _ = r.Confirm(1, base.Add(sessionactions.RecoveryWindow))
	r, claimed, _ := r.Claim(base)

	r = r.Restored(claimed.Request)
	if got := r.Available(base); len(got) != 0 {
		t.Fatalf("available after restore = %v, want none", got)
	}
	// A duplicate reply changes nothing.
	if got := r.Restored(claimed.Request).Available(base); len(got) != 0 {
		t.Fatalf("a repeated restore reply changed the stack: %v", got)
	}
	if got := r.Release(claimed.Request).Available(base); len(got) != 0 {
		t.Fatalf("a late failure reply revived a consumed entry: %v", got)
	}
}

// TestExpiredEntriesAreNeitherOfferedNorClaimable is the expired-undo guard.
func TestExpiredEntriesAreNeitherOfferedNorClaimable(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(1, snapshot("api"))
	r, _ = r.Confirm(1, base.Add(sessionactions.RecoveryWindow))

	after := base.Add(sessionactions.RecoveryWindow + time.Second)
	if got := r.Available(after); len(got) != 0 {
		t.Fatalf("available after the window = %v, want none", got)
	}
	swept, claimed, ok := r.Claim(after)
	if ok {
		t.Fatalf("an expired entry was claimed: %+v", claimed)
	}
	if got := swept.Available(base); len(got) != 0 {
		t.Fatalf("a refused claim left the expired entry behind: %v", got)
	}
}

// TestMutationsDoNotWriteThroughACopiedRecovery is the shared-mutable-state
// guard. The hosts are Bubble Tea models copied by value on every update, so a
// mutator that compacted or wrote into the receiver's array would be visible in
// every copy that outlived it — including the copy a pending command still
// holds.
func TestMutationsDoNotWriteThroughACopiedRecovery(t *testing.T) {
	original := sessionactions.Recovery{}
	for i, label := range []string{"first", "second"} {
		request := sessionactions.RequestID(i + 1)
		original = original.Reserve(request, snapshot(label))
		original, _ = original.Confirm(request, base.Add(sessionactions.RecoveryWindow))
	}
	held := original

	// Every mutator, run against the same value.
	claimed, _, _ := original.Claim(base)
	claimed.Restored(1)
	original.Reserve(3, snapshot("third"))
	original.Discard(1)
	original.Sweep(base.Add(time.Hour))
	original.Confirm(1, base)

	got := held.Available(base)
	if len(got) != 2 || got[0].Label != "first" || got[1].Label != "second" {
		t.Fatalf("the held copy saw %v, want its own first and second", got)
	}

	// The slice Available hands out is the caller's own, too.
	got[0] = snapshot("clobbered")
	if again := held.Available(base); again[0].Label != "first" {
		t.Fatalf("writing to a returned slice changed the stack: %v", again)
	}
}

// TestAnUnreconstructableSnapshotIsNeverRemembered: a row whose profile is not
// in the loaded configuration cannot be relaunched, so it is not offered.
func TestAnUnreconstructableSnapshotIsNeverRemembered(t *testing.T) {
	r := sessionactions.Recovery{}.Reserve(1, sessionactions.Snapshot{Label: "api"})
	if r, promoted := r.Confirm(1, base.Add(sessionactions.RecoveryWindow)); promoted || len(r.Available(base)) != 0 {
		t.Fatalf("an unlaunchable snapshot became recoverable: promoted=%v available=%v", promoted, r.Available(base))
	}
	if r := (sessionactions.Recovery{}).Reserve(0, snapshot("api")); len(r.Available(base)) != 0 {
		t.Fatal("a close with no request identity was remembered")
	}
}

// TestTheStackIsBounded keeps a close whose completion never arrives from
// growing the list without limit: a reserved entry has no deadline, so time
// cannot sweep it.
func TestTheStackIsBounded(t *testing.T) {
	r := sessionactions.Recovery{}
	for i := range 100 {
		r = r.Reserve(sessionactions.RequestID(i+1), snapshot("api"))
	}
	// Only the most recent reservations survive, which is what the oldest
	// request no longer being promotable reports.
	if _, promoted := r.Confirm(1, base.Add(sessionactions.RecoveryWindow)); promoted {
		t.Fatal("the oldest reservation survived 100 closes")
	}
	if _, promoted := r.Confirm(100, base.Add(sessionactions.RecoveryWindow)); !promoted {
		t.Fatal("the newest reservation was dropped")
	}
}

// TestDeadlineReportsTheLastEntryToExpire: an affordance naming the whole stack
// stays on screen exactly as long as something in it is still recoverable.
func TestDeadlineReportsTheLastEntryToExpire(t *testing.T) {
	r := sessionactions.Recovery{}
	if _, ok := r.Deadline(base); ok {
		t.Fatal("an empty stack reported a deadline")
	}
	for i, label := range []string{"first", "second"} {
		request := sessionactions.RequestID(i + 1)
		r = r.Reserve(request, snapshot(label))
		r, _ = r.Confirm(request, base.Add(time.Duration(i)*time.Second).Add(sessionactions.RecoveryWindow))
	}
	deadline, ok := r.Deadline(base)
	if !ok || !deadline.Equal(base.Add(time.Second+sessionactions.RecoveryWindow)) {
		t.Fatalf("deadline = %v ok=%v, want the later entry's", deadline, ok)
	}
	if latest, ok := r.Latest(base); !ok || latest.Label != "second" {
		t.Fatalf("latest = %+v ok=%v, want second", latest, ok)
	}
}
