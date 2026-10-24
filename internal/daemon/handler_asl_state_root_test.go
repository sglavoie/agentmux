package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/harness"
)

// stubLoopDiscover records the state root every discovery was asked to scan.
func stubLoopDiscover(t *testing.T) *[]string {
	t.Helper()
	var roots []string
	old := daemonLoopDiscover
	daemonLoopDiscover = func(opts agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		roots = append(roots, opts.StateRoot)
		return nil, nil
	}
	t.Cleanup(func() { daemonLoopDiscover = old })
	return &roots
}

// Sibling routing discovers active runs under the configured ASL state root,
// not ASL's default, and a root change drops candidates cached from the old
// one.
func TestActiveLoopCandidatesScanTheConfiguredStateRoot(t *testing.T) {
	roots := stubLoopDiscover(t)
	h := newTestHandler()
	h.SetASLStateRoot("/srv/asl-state")
	if _, err := h.activeLoopCandidates(); err != nil {
		t.Fatal(err)
	}
	h.SetASLStateRoot("/srv/other-state")
	if _, err := h.activeLoopCandidates(); err != nil {
		t.Fatal(err)
	}
	if len(*roots) != 2 || (*roots)[0] != "/srv/asl-state" || (*roots)[1] != "/srv/other-state" {
		t.Fatalf("discovered roots = %q, want each configured root once", *roots)
	}
}

// A loop finish on a workspace-matched target checks for other runs under the
// configured ASL state root.
func TestLoopFinishMarkerScansTheConfiguredStateRoot(t *testing.T) {
	roots := stubLoopDiscover(t)
	target := loopLifecycleTarget{windowID: 42, workspace: agentloop.WorkspaceTarget{CWD: t.TempDir(), Harness: harness.Claude, WindowID: 42}}
	marker := loopMarkerForLifecycleTarget(events.AgentSessionLoopFinished, target, nil, "/srv/asl-state")
	if marker == nil || *marker {
		t.Fatalf("marker = %v, want false with no matching run", marker)
	}
	if len(*roots) != 1 || (*roots)[0] != "/srv/asl-state" {
		t.Fatalf("discovered roots = %q, want the configured root", *roots)
	}
}
