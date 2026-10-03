package asl

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
)

// A never-run plan is no-state, not unreadable. Counting it with unreadable
// states made doctor report hundreds of "unreadable state documents" per sweep
// on a state root that ASL's own pairing report calls healthy.
func TestNeverRunPlansAreNotCountedAsUnreadable(t *testing.T) {
	var m ReconcileMetrics
	m.RecordSweep(time.Unix(0, 0), agentloop.DiscoveryResult{Observations: []agentloop.PlanObservation{
		{StateDir: "/s/a", Presence: agentloop.PresenceIndeterminate, Reason: agentloop.ReasonNoState},
		{StateDir: "/s/b", Presence: agentloop.PresenceIndeterminate, Reason: agentloop.ReasonUnreadableState},
	}})
	if got := m.Snapshot("/s", time.Unix(1, 0)).UnreadableStates; got != 1 {
		t.Fatalf("UnreadableStates = %d, want 1 (the no-state plan must not count)", got)
	}
}
