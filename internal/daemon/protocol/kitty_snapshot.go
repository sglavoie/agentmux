package protocol

import (
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file holds the optional Kitty-snapshot block carried by the daemon wire
// Response: what the daemon's cached window tree is, how old it is, and how the
// per-socket list coordinator underneath it has been behaving. It is additive,
// so a daemon that predates it simply omits the field and a client that
// predates it ignores one it does not know.
//
// The block is a serialization shape plus pure projections. The daemon reads
// the clock and decides what to publish; nothing here does either.

// KittySnapshotStaleAfter is the single shared threshold that separates a
// usable cached topology from one too old to act on. It lives here, in the
// vocabulary both the daemon and every client already import, so the daemon,
// doctor, and the dashboard cannot disagree about when the same snapshot went
// stale. probes.SnapshotView deliberately exposes only Age so this constant has
// exactly one home.
const KittySnapshotStaleAfter = 6 * time.Second

// KittySnapshotStale reports whether a snapshot of the given age is too old to
// act on. Only a published snapshot can be stale: a cold cache has never seen
// the window tree at all, which is a different condition with a different
// remedy, and reporting it as stale would hide the distinction behind one flag.
func KittySnapshotStale(ready bool, age time.Duration) bool {
	return ready && age >= KittySnapshotStaleAfter
}

// KittySnapshot is the daemon's cached view of the Kitty window tree as seen
// from the wire. Ready is false for a cold daemon that has not completed a
// successful list yet; every other field is then zero. Stale is computed by the
// daemon at response time against KittySnapshotStaleAfter rather than left to
// each client, so one deployment cannot hold two opinions about the same
// snapshot.
//
// Windows is populated for TypeStateQuery and omitted for TypeHealth: health is
// a status probe that must stay small enough for its acknowledgement budget,
// while the state query is what a dashboard refresh actually renders from. A
// nil Windows on a Ready block therefore means "not asked for", not "no
// windows"; the request type is what tells them apart.
type KittySnapshot struct {
	// Ready reports whether any snapshot has been published. False is a cold
	// cache.
	Ready bool `json:"ready"`
	// Stale is Ready && Age >= KittySnapshotStaleAfter, evaluated when the
	// response was built. A cold block is never stale.
	Stale bool `json:"stale,omitempty"`
	// Generation advances once per successful publication and never on a failed
	// read, which makes it — not CapturedAt — the ordering key for "newest
	// evidence": concurrent listers stamp their own completion times, so a read
	// that finished first can publish second.
	Generation uint64 `json:"generation,omitempty"`
	// CapturedAt is the Unix second at which the successful ListWindows behind
	// this snapshot COMPLETED. Zero for a cold block.
	CapturedAt int64 `json:"captured_at,omitempty"`
	// AgeMS is how long before the response the capture completed, in whole
	// milliseconds, clamped at zero so a backwards wall-clock adjustment reads
	// as fresh rather than negative.
	AgeMS int64 `json:"age_ms"`
	// Coordinator is the list-coordination accounting for the daemon's
	// effective socket. Unlike the fields above — which describe one specific
	// publication — these are operational counters sampled for the same socket
	// and are not bound to the generation beside them. Nil when the socket has
	// never been read, when its idle key has been evicted, or when the client
	// exposes no coordination surface at all; a consumer must read nil as "no
	// information", never as "healthy".
	Coordinator *KittyListCoordinatorStatus `json:"coordinator,omitempty"`
	// Windows is the cached window tree. Read-only for the daemon that owns it
	// and freshly decoded for every client that receives it.
	Windows []kitty.OSWindow `json:"windows,omitempty"`
}

// KittyListCoordinatorStatus is kitty.ListStatus in wire form: durations become
// whole milliseconds and the failure instant becomes a Unix second, so the
// block carries no Go-specific encodings.
//
// LastFailureAt and LastErrorClass are retained after a recovery and describe
// the most recent failure ever seen on this socket. ConsecutiveFailures is the
// only field that says whether the socket is failing right now.
type KittyListCoordinatorStatus struct {
	Socket string `json:"socket,omitempty"`
	// Attempts, Failures, Timeouts, and the latency fields count completed
	// underlying executions only. A read still in flight has not been counted
	// yet, so InFlight and CoalescedWaiters are the only fields that describe
	// an outstanding read.
	Attempts            uint64 `json:"attempts,omitempty"`
	Failures            uint64 `json:"failures,omitempty"`
	Timeouts            uint64 `json:"timeouts,omitempty"`
	CoalescedWaiters    uint64 `json:"coalesced_waiters,omitempty"`
	InFlight            bool   `json:"in_flight,omitempty"`
	ConsecutiveFailures uint64 `json:"consecutive_failures,omitempty"`
	LastLatencyMS       int64  `json:"last_latency_ms,omitempty"`
	MaxLatencyMS        int64  `json:"max_latency_ms,omitempty"`
	LastFailureAt       int64  `json:"last_failure_at,omitempty"`
	// LastErrorClass is the stable "timeout"/"command"/"decode" vocabulary, or
	// empty when the socket has never failed.
	LastErrorClass string `json:"last_error_class,omitempty"`
}

// NewKittyListCoordinatorStatus projects one retained coordinator entry onto
// the wire. It is a pure conversion: the caller decides whether a status was
// available at all.
func NewKittyListCoordinatorStatus(status kitty.ListStatus) KittyListCoordinatorStatus {
	out := KittyListCoordinatorStatus{
		Socket:              status.Socket,
		Attempts:            status.Attempts,
		Failures:            status.Failures,
		Timeouts:            status.Timeouts,
		CoalescedWaiters:    status.CoalescedWaiters,
		InFlight:            status.InFlight,
		ConsecutiveFailures: status.ConsecutiveFailures,
		LastLatencyMS:       DurationMillis(status.LastLatency),
		MaxLatencyMS:        DurationMillis(status.MaxLatency),
		LastErrorClass:      string(status.LastErrorClass),
	}
	if !status.LastFailure.IsZero() {
		out.LastFailureAt = status.LastFailure.Unix()
	}
	return out
}

// DurationMillis converts a duration to whole milliseconds, clamping a negative
// value to zero. Every duration on this block is an elapsed measurement, so a
// negative one can only come from a clock that moved backwards and is more
// honestly reported as "no time at all" than as a negative age.
func DurationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Millisecond)
}
