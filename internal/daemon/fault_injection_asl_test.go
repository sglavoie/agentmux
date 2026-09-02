package daemon

// Fault injection against ASL lifecycle events, where the loop's on-disk state
// is the ground truth the lease is reconciled against. Events can be dropped in
// either direction — a finish that never arrives leaves a lease running
// forever, a start that never arrives leaves a live run invisible — and
// reconciliation has to repair both. The asymmetry that makes it safe: two
// consecutive successful discoveries are needed to release a lease, while a
// failed discovery is missing evidence rather than evidence of absence and
// changes nothing.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestFaultDroppedASLFinishRepairedByReconciliation drops a run's finish event
// after its turn started through the real hook path. Two consecutive
// successful discoveries that cannot see the run must converge the lease to
// the state the lost finish would have produced, and the repair must be
// recorded as a synthetic watchdog intervention.
func TestFaultDroppedASLFinishRepairedByReconciliation(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	h.publishSnapshot(windows, time.Now())
	stubDaemonLoopDiscover(t, nil, nil)

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeHookEvent,
		WindowID: 42,
		Event:    "AgentSessionLoopTurnStarted",
		PayloadJSON: fmt.Sprintf(
			`{"schema_version":%d,"contract_set_id":%q,"source":"agent_session_loop",`+
				`"cwd":%q,"harness":"claude","run_id":"run-lost"}`,
			events.ASLHookSchemaVersion, aslcontract.ContractSetID, projectDir),
	})
	if !resp.OK {
		t.Fatalf("turn started = %+v", resp)
	}
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("live run = looping=%v icon=%q; want ⚡🔁", looping, icon)
	}

	// The finish event is dropped. Reconciliation misses the run twice.
	now := time.Now()
	h.ReconcileASLLeases(now, windows)
	if looping, _ := windowLoopState(t, h, 42); !looping {
		t.Fatal("one miss released the lease; want two-miss protection")
	}
	h.ReconcileASLLeases(now.Add(aslReconcileInterval), windows)

	looping, icon := windowLoopState(t, h, 42)
	if looping || icon != "stop" {
		t.Fatalf("after two misses = looping=%v icon=%q; want the lost finish's 🛑 without 🔁", looping, icon)
	}

	export := h.Handle(protocol.Request{Type: protocol.TypeEventsExport})
	wd := export.WindowMetadata[42].Watchdog
	if wd.Reconciles == 0 || wd.LastEvent != eventASLReconciled {
		t.Fatalf("watchdog metadata = %+v; want the reconciliation repair recorded", wd)
	}
}

// TestFaultDroppedASLStartSeededByReconciliation is the opposite direction: no
// lifecycle event ever arrived (daemon restarted, hooks dropped), but the
// loop's on-disk state reports an executing turn. Reconciliation must seed the
// lease with the discovered phase.
func TestFaultDroppedASLStartSeededByReconciliation(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-seeded", agentloop.PhaseTurn)}, nil)

	h.ReconcileASLLeases(time.Now(), windows)

	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("seeded = looping=%v icon=%q; want ⚡🔁 from discovery", looping, icon)
	}
}

// TestFaultDiscoveryFailurePreservesLeases: a failing discovery is missing
// evidence, not evidence of absence — leases survive any number of failed
// scans.
func TestFaultDiscoveryFailurePreservesLeases(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseTurn)}, nil)
	h.ReconcileASLLeases(time.Now(), windows)

	stubDaemonLoopDiscover(t, nil, errors.New("state dir unreadable"))
	now := time.Now()
	for i := 1; i <= 3; i++ {
		h.ReconcileASLLeases(now.Add(time.Duration(i)*aslReconcileInterval), windows)
	}
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("after failed discoveries = looping=%v icon=%q; want lease preserved", looping, icon)
	}
}
