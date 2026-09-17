// This file is close recovery: what a surface remembers about the sessions it
// has just closed, for exactly as long as reopening one is an undo rather than a
// fresh launch.
//
// It lives beside the controller because it is the same kind of thing — state
// about dispatched operations, keyed by the request identity the controller
// mints — and because two surfaces need it. The dashboard offers it today; the
// overview is the second, and a recovery stack held privately in the dashboard's
// own fields could not be handed to it.
//
// Three properties are the type rather than details of it.
//
// It is a VALUE with copy-on-write mutators. The hosts are Bubble Tea models
// copied by value on every update, so an entry list mutated in place — the
// `s = s[:0]` compaction this replaces — is written through by every copy of the
// model that outlived the mutation, including the copies a pending command still
// holds. Every mutator here returns a new Recovery and never writes through the
// receiver's backing array.
//
// An entry becomes recoverable only when the close it came from is CONFIRMED.
// A close is reserved at dispatch, when the snapshot is still reconstructible,
// and promoted when the operation reports that the tab was observed to go. A
// close that failed, or that nobody could confirm, leaves nothing behind: the
// recovery would relaunch a session that is still running.
//
// The five-second deadline starts at that confirmation, not at dispatch. The
// window is meant to be five seconds of the operator looking at the result, and
// a close that took two seconds to confirm would otherwise offer three.

package sessionactions

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// RecoveryWindow is how long a confirmed close stays recoverable.
const RecoveryWindow = 5 * time.Second

// maxRecoveryEntries bounds the stack. It is what keeps a close whose completion
// never arrives — a dropped message, a surface torn down mid-flight — from
// growing the list without limit, since a reserved entry has no deadline yet and
// so cannot be swept by time. Well past any plausible number of closes inside one
// five-second window.
const maxRecoveryEntries = 16

// Snapshot is everything a relaunch needs, captured before the close.
//
// It is captured first and stored second. Reconstructing it afterwards would
// mean reading a row that is by then deliberately gone.
type Snapshot struct {
	// Session is the launch template rebuilt from the closed row.
	Session config.Session
	// Account is the profile the session ran under.
	Account config.Account
	// Label is what to call it in a sentence, typically the row's title.
	Label string
}

// Reconstructable reports whether the snapshot names something launchable. A row
// whose profile is not in the loaded configuration produces one that does not,
// and such a close is never offered a recovery.
func (s Snapshot) Reconstructable() bool { return s.Account.Name != "" }

// recoveryStage is how far one remembered close has got.
type recoveryStage int

const (
	// stageReserved is a close that has been dispatched and not yet reported.
	// It has no deadline: the window has not started.
	stageReserved recoveryStage = iota
	// stageAvailable is a confirmed close, recoverable until its deadline.
	stageAvailable
	// stageClaimed is an entry whose relaunch is in flight. It keeps its
	// deadline, so a relaunch that fails becomes available again with the time
	// it had left rather than a fresh five seconds.
	stageClaimed
)

type recoveryEntry struct {
	stage    recoveryStage
	close    RequestID
	relaunch RequestID
	deadline time.Time
	snapshot Snapshot
}

// Recovery is one surface's stack of closes that may still be undone, oldest
// first. The zero value is an empty stack and is ready to use.
type Recovery struct {
	entries []recoveryEntry
	// last is the counter relaunch request identities are minted from. It is
	// separate from the controller's own dispatch counter because a relaunch is
	// not a dispatched session action: it correlates only with this stack.
	last RequestID
}

// Claimed is one entry handed to a relaunch: which snapshot to launch, and the
// request identity to report the result under.
type Claimed struct {
	Request  RequestID
	Snapshot Snapshot
}

// Reserve remembers a close that has been dispatched but not yet reported.
//
// Nothing is offered to the operator yet. The entry exists so the snapshot is
// captured while the row still exists, and so the completion that arrives later
// has something to promote. A close with no request identity, or with nothing
// launchable to remember, is not reserved and is therefore never recoverable.
func (r Recovery) Reserve(request RequestID, snapshot Snapshot) Recovery {
	if !request.Valid() || !snapshot.Reconstructable() {
		return r
	}
	next := r
	next.entries = appendRecoveryEntry(r.entries, recoveryEntry{
		stage:    stageReserved,
		close:    request,
		snapshot: snapshot,
	})
	return next
}

// Confirm promotes the reserved close request to a recoverable entry expiring at
// deadline, and reports whether there was one to promote. A caller passes
// now.Add(RecoveryWindow): the window starts here, at the confirmation, rather
// than at the dispatch that reserved the entry.
func (r Recovery) Confirm(request RequestID, deadline time.Time) (Recovery, bool) {
	index := r.indexOfClose(request)
	if index < 0 || r.entries[index].stage != stageReserved {
		return r, false
	}
	next := r.clone()
	next.entries[index].stage = stageAvailable
	next.entries[index].deadline = deadline
	return next, true
}

// Discard drops the reserved close request without ever offering it. This is the
// failed and the unconfirmed close: in both, relaunching would produce a second
// live session beside one that may still be running.
func (r Recovery) Discard(request RequestID) Recovery {
	index := r.indexOfClose(request)
	if index < 0 || r.entries[index].stage != stageReserved {
		return r
	}
	return r.remove(index)
}

// Claim takes the most recent still-recoverable entry, marks its relaunch as in
// flight, and mints the request identity that relaunch reports under.
//
// Most-recent-first is what makes repeated undo walk back through repeated
// closes in the order they were made. A claimed entry is reserved rather than
// removed, so a second undo pressed while the first is still launching takes the
// entry below it instead of launching the same session twice.
func (r Recovery) Claim(now time.Time) (Recovery, Claimed, bool) {
	next := r.Sweep(now)
	for i := len(next.entries) - 1; i >= 0; i-- {
		if next.entries[i].stage != stageAvailable {
			continue
		}
		next = next.clone()
		next.last++
		next.entries[i].stage = stageClaimed
		next.entries[i].relaunch = next.last
		return next, Claimed{Request: next.last, Snapshot: next.entries[i].snapshot}, true
	}
	return next, Claimed{}, false
}

// Restored consumes the claimed entry: its relaunch succeeded and there is
// nothing left to undo.
func (r Recovery) Restored(relaunch RequestID) Recovery {
	index := r.indexOfRelaunch(relaunch)
	if index < 0 {
		return r
	}
	return r.remove(index)
}

// Release puts a claimed entry back after its relaunch failed, with the deadline
// it already had. The operator may try again, but a failed relaunch does not buy
// more time: the window is about how long the close stays undoable, and retrying
// does not make the close more recent.
func (r Recovery) Release(relaunch RequestID) Recovery {
	index := r.indexOfRelaunch(relaunch)
	if index < 0 {
		return r
	}
	next := r.clone()
	next.entries[index].stage = stageAvailable
	next.entries[index].relaunch = 0
	return next
}

// Sweep drops every entry past its deadline.
//
// A reserved entry has no deadline — its close has not reported an outcome — and
// is never swept here. The bound on the stack is what keeps a completion that
// never arrives from accumulating.
func (r Recovery) Sweep(now time.Time) Recovery {
	kept := make([]recoveryEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry.stage != stageReserved && !now.Before(entry.deadline) {
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) == len(r.entries) {
		return r
	}
	next := r
	next.entries = kept
	return next
}

// Available reports the snapshots that may still be relaunched, oldest first. An
// entry whose relaunch is already in flight is not among them.
func (r Recovery) Available(now time.Time) []Snapshot {
	out := make([]Snapshot, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry.recoverable(now) {
			out = append(out, entry.snapshot)
		}
	}
	return out
}

// Latest reports the snapshot Claim would take, without taking it.
func (r Recovery) Latest(now time.Time) (Snapshot, bool) {
	for i := len(r.entries) - 1; i >= 0; i-- {
		if r.entries[i].recoverable(now) {
			return r.entries[i].snapshot, true
		}
	}
	return Snapshot{}, false
}

// Deadline reports when the last still-recoverable entry expires, which is how
// long an affordance naming the whole stack should stay on screen.
func (r Recovery) Deadline(now time.Time) (time.Time, bool) {
	var latest time.Time
	found := false
	for _, entry := range r.entries {
		if !entry.recoverable(now) {
			continue
		}
		if !found || entry.deadline.After(latest) {
			latest = entry.deadline
			found = true
		}
	}
	return latest, found
}

func (e recoveryEntry) recoverable(now time.Time) bool {
	return e.stage == stageAvailable && now.Before(e.deadline)
}

func (r Recovery) indexOfClose(request RequestID) int {
	if !request.Valid() {
		return -1
	}
	for i, entry := range r.entries {
		if entry.close == request {
			return i
		}
	}
	return -1
}

func (r Recovery) indexOfRelaunch(request RequestID) int {
	if !request.Valid() {
		return -1
	}
	for i, entry := range r.entries {
		if entry.stage == stageClaimed && entry.relaunch == request {
			return i
		}
	}
	return -1
}

// clone copies the backing array so a mutation cannot be written through a
// Recovery a copied model still holds.
func (r Recovery) clone() Recovery {
	next := r
	next.entries = append(make([]recoveryEntry, 0, len(r.entries)), r.entries...)
	return next
}

func (r Recovery) remove(index int) Recovery {
	kept := make([]recoveryEntry, 0, len(r.entries)-1)
	kept = append(kept, r.entries[:index]...)
	kept = append(kept, r.entries[index+1:]...)
	next := r
	next.entries = kept
	return next
}

// appendRecoveryEntry appends into a fresh array, dropping the oldest entries
// when the stack is at its bound.
func appendRecoveryEntry(existing []recoveryEntry, entry recoveryEntry) []recoveryEntry {
	start := 0
	if len(existing) >= maxRecoveryEntries {
		start = len(existing) - maxRecoveryEntries + 1
	}
	out := make([]recoveryEntry, 0, len(existing)-start+1)
	out = append(out, existing[start:]...)
	return append(out, entry)
}
