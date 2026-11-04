package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
)

func TestDispatchPickerLaunch_MissingAccountReportsError(t *testing.T) {
	t.Parallel()

	m := launchDispatchModel(t, nil)
	m.cfg.Sessions[0].Account = "missing"
	m.pickerSessions = launch.SortSessions(m.cfg.Sessions)
	m.pickerFilteredIdx = launch.ApplyFilter(m.pickerSessions, "")
	m.pickerSelected = make(map[int]bool)
	selectByTitle(&m, "api")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd for selected session with missing account")
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 {
		t.Fatalf("expected 1 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	if got[0].err == nil {
		t.Fatal("expected missing account to be reported as launch error")
	}
	if got[0].title != "api" {
		t.Errorf("expected queued title api, got %q", got[0].title)
	}
}

// TestDispatchPickerLaunch_RunningSnapshotIsStable verifies that selected
// sessions are finalized synchronously at dispatch time. Mutating
// m.pickerRunning after dispatch does NOT affect which sessions launch because
// the closures don't re-poll the snapshot.
func TestDispatchPickerLaunch_RunningSnapshotIsStable(t *testing.T) {
	t.Parallel()

	m := launchDispatchModel(t, nil)
	selectByTitle(&m, "api", "backend")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	// Simulate the "race" scenario described in the original session plan:
	// A's tab appears in m.sessions (and therefore in any rebuilt
	// pickerRunning snapshot) before B's closure fires. Mutate both maps and
	// verify the already-dispatched closures still emit both progress values.
	m.pickerRunning[session.SessionKey("work", "api")] = 1
	m.pickerRunning[session.SessionKey("work", "backend")] = 1
	m.sessions = append(m.sessions, session.RunningSession{
		TabID: 99, Base: "api", Account: "work",
	})

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg even after pickerRunning mutation, got %d", len(got))
	}
}
