package debug

import (
	"fmt"
	"io"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// This file is the operator-facing rendering of the daemon's cached Kitty
// topology. `amx debug status` and `amx doctor` both report it and must not
// word it differently: one of them is what an operator reads first, and which
// one depends only on habit.
//
// Four states are distinguishable and each has a different remedy, so none of
// them may collapse into another:
//
//   - unreported — the daemon predates the block. Restart it; nothing is known.
//   - cold — a new daemon that has never completed a `kitten @ ls`. Wait, or
//     look at why the very first list has not landed.
//   - fresh — the cache is inside the shared threshold. Nothing to do.
//   - stale — a successful list landed once but not recently. This is the
//     recurrence signature the whole coordination path exists for.

// kittySnapshotState is the one-word verdict, kept separate from the detail so
// text and findings cannot drift apart.
func kittySnapshotState(snapshot *protocol.KittySnapshot) string {
	switch {
	case snapshot == nil:
		return "unreported"
	case !snapshot.Ready:
		return "cold"
	case snapshot.Stale:
		return "stale"
	default:
		return "fresh"
	}
}

// kittySnapshotAge is the snapshot's age as a duration. Zero for a cold or
// absent block, which is why callers print it only alongside a generation.
func kittySnapshotAge(snapshot *protocol.KittySnapshot) time.Duration {
	if snapshot == nil {
		return 0
	}
	return time.Duration(snapshot.AgeMS) * time.Millisecond
}

// kittySnapshotSummary is the whole line after "kitty snapshot: ".
//
// Staleness is read from the daemon's own Stale boolean rather than recomputed
// from the age beside it: the threshold has one home, and a CLI that re-derived
// the verdict could disagree with the daemon it is reporting on.
func kittySnapshotSummary(snapshot *protocol.KittySnapshot) string {
	state := kittySnapshotState(snapshot)
	switch state {
	case "unreported":
		return "unreported (older daemon; restart required)"
	case "cold":
		return "cold (no successful kitten @ ls yet)"
	case "stale":
		return fmt.Sprintf("stale (generation %d, age %s, threshold %s)",
			snapshot.Generation, kittySnapshotAge(snapshot).Round(time.Millisecond), protocol.KittySnapshotStaleAfter)
	default:
		return fmt.Sprintf("fresh (generation %d, age %s)",
			snapshot.Generation, kittySnapshotAge(snapshot).Round(time.Millisecond))
	}
}

// kittyCoordinatorSummary is the whole line after "kitty list coordinator: ".
//
// A nil coordinator is three conditions the daemon deliberately collapsed into
// one — no accounting surface, never read, idle key evicted — so it is reported
// as an absence of information and never as a healthy socket.
//
// consecutive_failures is the only field that describes the socket right now;
// last_error is retained after a recovery and describes the most recent failure
// ever seen, which is exactly the recurrence evidence a cleared stall leaves
// behind. The line says so rather than leaving an operator to infer it from a
// nonzero class beside a zero streak.
func kittyCoordinatorSummary(status *protocol.KittyListCoordinatorStatus) string {
	if status == nil {
		return "no information (never read, evicted, or unreported)"
	}
	lastError := status.LastErrorClass
	if lastError == "" {
		lastError = "none"
	}
	return fmt.Sprintf(
		"socket=%s attempts=%d failures=%d timeouts=%d coalesced=%d in_flight=%t consecutive_failures=%d last_latency=%s max_latency=%s last_error=%s",
		emptyDash(status.Socket), status.Attempts, status.Failures, status.Timeouts, status.CoalescedWaiters,
		status.InFlight, status.ConsecutiveFailures,
		time.Duration(status.LastLatencyMS)*time.Millisecond, time.Duration(status.MaxLatencyMS)*time.Millisecond,
		lastError,
	)
}

// checkKittySnapshotStale is the stable identifier for a daemon whose cached
// Kitty topology has aged past the shared threshold. It is WARNING severity on
// purpose: the dashboard still renders that topology and only suppresses the
// writes derived from it, so this is degraded-but-working and must not trip
// `--strict` on its own.
//
// Unlike checkScreenPatternOverrideInvalid, this identifier DID bump
// doctorReportSchemaVersion. The policy on that constant bumps for new failure
// identifiers; this is the case it did not anticipate, where a warning
// identifier is itself the diagnosis a consumer came for. Absent it, a stale
// snapshot and a daemon too old to report one are the same silence.
const checkKittySnapshotStale = "kitty_snapshot_stale"

// writeDoctorKittySnapshot reports the cached topology and raises the stale
// warning.
//
// The finding fires only for a ready-but-stale block. A cold block has never
// held a topology, so it has no age to be past — protocol.KittySnapshotStale
// already refuses to call it stale, and the conjunction here says so at the
// only place that turns the verdict into a finding. A nil block is a daemon
// that predates the field: unknown, reported as such, and never a finding,
// because a version gap is not a health fault.
func writeDoctorKittySnapshot(out io.Writer, snapshot *protocol.KittySnapshot, addFinding func(string, string, string, ...any)) error {
	if err := writeKittySnapshot(out, snapshot); err != nil {
		return err
	}
	if snapshot == nil || !snapshot.Ready || !snapshot.Stale {
		return nil
	}
	addFinding(severityWarning, checkKittySnapshotStale,
		"daemon Kitty topology is stale: the last successful kitten @ ls completed %s ago (threshold %s, generation %d)",
		kittySnapshotAge(snapshot).Round(time.Millisecond), protocol.KittySnapshotStaleAfter, snapshot.Generation)
	return nil
}

// writeKittySnapshot prints both lines. A daemon that predates the block gets
// the "unreported" line and no coordinator line at all: it has no accounting to
// report, and printing "no information" under an unreported snapshot would
// state the same absence twice.
func writeKittySnapshot(out io.Writer, snapshot *protocol.KittySnapshot) error {
	if _, err := fmt.Fprintf(out, "kitty snapshot: %s\n", kittySnapshotSummary(snapshot)); err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	_, err := fmt.Fprintf(out, "kitty list coordinator: %s\n", kittyCoordinatorSummary(snapshot.Coordinator))
	return err
}
