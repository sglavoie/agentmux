package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/commands"
)

// A sustained Kitty outage is a degradation, not a failure: the rows the
// dashboard already has keep working, and the only thing that changes is that
// the operator is eventually told how old they are. These tests pin the two
// halves of that — when the warning appears, and that the red cold-failure
// error is not what appears instead.

const staleWarningPrefix = "Kitty refresh delayed; showing cached sessions"

// staleTestEpoch is an arbitrary fixed instant. Every age below is expressed as
// an offset from it, so nothing here depends on the wall clock.
var staleTestEpoch = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// warmDashboard returns a dashboard that has already rendered a session list,
// reading its clock from the caller's variable. Advancing that variable is how
// a test ages topology without sleeping.
func warmDashboard(clock *time.Time) Model {
	m := testModel()
	m.width = 100
	m.now = func() time.Time { return *clock }
	return loadSessions(m, makeSessions())
}

func statusBarText(m Model) string {
	return strings.TrimSpace(stripAnsi(renderStatusBar(m)))
}

func assertStatusBarLacks(t *testing.T, m Model, unwanted string) {
	t.Helper()
	if got := statusBarText(m); strings.Contains(got, unwanted) {
		t.Fatalf("status bar = %q, want it to omit %q", got, unwanted)
	}
}

// TestStaleWarningOnsetIsExactlyTheSharedThreshold pins both edges of the one
// decision this feature makes. The threshold is protocol.KittySnapshotStaleAfter
// rather than a second copy of six seconds, so the dashboard and the daemon
// cannot drift apart about when topology has gone old.
func TestStaleWarningOnsetIsExactlyTheSharedThreshold(t *testing.T) {
	cases := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{name: "well inside the threshold", elapsed: time.Second, want: ""},
		{name: "one millisecond short", elapsed: protocol.KittySnapshotStaleAfter - time.Millisecond, want: ""},
		{name: "exactly at the threshold", elapsed: protocol.KittySnapshotStaleAfter, want: staleWarningPrefix + " (6s old)"},
		{name: "past the threshold", elapsed: protocol.KittySnapshotStaleAfter + 3*time.Second, want: staleWarningPrefix + " (9s old)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := staleTestEpoch
			m := warmDashboard(&clock)
			clock = staleTestEpoch.Add(tc.elapsed)

			if tc.want == "" {
				assertStatusBarLacks(t, m, staleWarningPrefix)
				// Silence below the threshold means the ordinary list status,
				// not a blank bar.
				assertStatusBarContains(t, m, "Sessions: 3 running")
				return
			}
			assertStatusBarContains(t, m, tc.want)
		})
	}
}

// TestStaleWarningAgeAdvancesWithoutAnyRefreshResult is the reason the age is
// derived at render time. During a real outage no message arrives at all, so an
// age computed when the last message landed would freeze at the moment the
// dashboard stopped being able to see Kitty.
func TestStaleWarningAgeAdvancesWithoutAnyRefreshResult(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)

	for _, elapsed := range []time.Duration{6 * time.Second, 20 * time.Second, 5 * time.Minute} {
		clock = staleTestEpoch.Add(elapsed)
		want := fmt.Sprintf("%s (%ds old)", staleWarningPrefix, int(elapsed/time.Second))
		assertStatusBarContains(t, m, want)
	}

	// Failing refreshes must not reset the age either: a failure carries no
	// capture, so the tree on screen is exactly as old as it was.
	clock = staleTestEpoch.Add(42 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))
	assertStatusBarContains(t, m, staleWarningPrefix+" (42s old)")
}

// TestStaleWarningClearsOnTheNextSuccessfulTopology closes the loop: recovery is
// not a separate code path, it is the same stamping that made the warning
// possible, applied to a newer tree.
func TestStaleWarningClearsOnTheNextSuccessfulTopology(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(30 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))
	assertStatusBarContains(t, m, staleWarningPrefix)

	m = applySessions(m, commands.SessionsMsg{
		Sessions: makeSessions(),
		Topology: commands.TopologyFreshness{Source: commands.TopologyFromDirect, CapturedAt: clock},
	})
	assertStatusBarLacks(t, m, staleWarningPrefix)
	assertStatusBarContains(t, m, "Sessions: 3 running")
	if m.err != nil {
		t.Fatalf("recovery left err = %v, want it cleared", m.err)
	}
}

// TestRecoveryToAnAlreadyStaleSnapshotReportsTheNewAge covers the case the
// daemon's own verdict makes possible: a refresh can succeed and still hand back
// topology that is older than the threshold. The warning must then re-derive
// from the new capture rather than either clearing or continuing to grow.
func TestRecoveryToAnAlreadyStaleSnapshotReportsTheNewAge(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(90 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))
	assertStatusBarContains(t, m, staleWarningPrefix+" (90s old)")

	m = applySessions(m, commands.SessionsMsg{
		Sessions: makeSessions(),
		Topology: commands.TopologyFreshness{
			Source: commands.TopologyFromDaemon,
			Stale:  true,
			Age:    8 * time.Second,
		},
	})
	assertStatusBarContains(t, m, staleWarningPrefix+" (8s old)")
}

// TestColdRefreshFailureStaysRed is the boundary the warning is carved out of.
// With no tree ever rendered there is nothing to degrade to, so the failure is
// the whole state of the dashboard and keeps its red error.
func TestColdRefreshFailureStaysRed(t *testing.T) {
	m := testModel()
	m.width = 100
	m.now = func() time.Time { return staleTestEpoch }
	m = applyRefreshErr(m, errors.New("kitten @ ls: connection refused"))

	assertStatusBarContains(t, m, "Kitty error: kitten @ ls: connection refused")
	if msg := currentStatusMessage(m, staleTestEpoch); msg.Severity != statusError {
		t.Fatalf("cold failure severity = %v, want %v", msg.Severity, statusError)
	}
}

// TestWarmRefreshFailureIsQuietThenNonfatal is the control for the test above:
// the same error is not red once there is a list to keep showing, and it never
// becomes red — it becomes a warning.
func TestWarmRefreshFailureIsQuietThenNonfatal(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(2 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))

	assertStatusBarLacks(t, m, "Kitty error")
	assertStatusBarLacks(t, m, staleWarningPrefix)
	assertStatusBarContains(t, m, "Sessions: 3 running")

	clock = staleTestEpoch.Add(11 * time.Second)
	assertStatusBarLacks(t, m, "Kitty error")
	msg := currentStatusMessage(m, clock)
	if msg.Severity != statusWarning {
		t.Fatalf("sustained-outage severity = %v, want %v", msg.Severity, statusWarning)
	}
}

// TestStaleWarningNeverDisplacesAnActionError keeps the two apart. An action
// error reports something the operator just asked for and has no relationship to
// how old the window tree is, so it outranks the warning.
func TestStaleWarningNeverDisplacesAnActionError(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(30 * time.Second)
	m.actionErr = errors.New("close tab: no such window")

	assertStatusBarContains(t, m, "Action error: close tab: no such window")
	assertStatusBarLacks(t, m, staleWarningPrefix)
}

// TestTopologyAgeClampsABackwardsClock covers a clock that moved backwards
// between the capture and the render. The answer is zero, not a negative age
// formatted into the warning.
func TestTopologyAgeClampsABackwardsClock(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)
	m.topology.Age = time.Second

	age, warm := topologyAge(m, staleTestEpoch.Add(-time.Hour))
	if !warm {
		t.Fatal("a rendered dashboard reported itself cold")
	}
	if age != 0 {
		t.Fatalf("age = %v under a backwards clock, want 0", age)
	}
	clock = staleTestEpoch.Add(-time.Hour)
	assertStatusBarLacks(t, m, staleWarningPrefix)
}

// TestStaleTopologyStillRebuildsTheListAndKeepsTheCursor pins the rendering half
// of the stale rule. A stale tree is not quarantined: it replaces the list like
// any other, and the row the operator had selected follows it across the
// rebuild even though the tree it came from is old enough to be unwritable.
func TestStaleTopologyStillRebuildsTheListAndKeepsTheCursor(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)
	m = pressKey(m, "j")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d after one j, want 1", m.cursor)
	}
	selected := m.filtered[m.cursor].Base

	reordered := makeSessions()
	reordered[0], reordered[2] = reordered[2], reordered[0]
	m = applySessions(m, commands.SessionsMsg{
		Sessions: reordered,
		Topology: commands.TopologyFreshness{Source: commands.TopologyFromDaemon, Stale: true, Age: 7 * time.Second},
	})

	if len(m.filtered) != 3 {
		t.Fatalf("stale refresh rendered %d rows, want 3", len(m.filtered))
	}
	if got := m.filtered[m.cursor].Base; got != selected {
		t.Fatalf("cursor landed on %q after a stale rebuild, want %q", got, selected)
	}
	assertStatusBarContains(t, m, staleWarningPrefix+" (7s old)")
}

// TestStaleTopologyConsumesAnUnseenSelectionTokenExactlyOnce is the model-side
// consequence of deferring the user-var clear: because the request is still on
// the window, the dashboard sees the same token on every stale refresh and must
// honour it once rather than re-stealing the cursor on each one.
func TestStaleTopologyConsumesAnUnseenSelectionTokenExactlyOnce(t *testing.T) {
	clock := staleTestEpoch
	m := warmDashboard(&clock)

	staleSelection := func() commands.SessionsMsg {
		return commands.SessionsMsg{
			Sessions:       makeSessions(),
			SelectionTabID: 3,
			SelectionToken: "request-1",
			Topology:       commands.TopologyFreshness{Source: commands.TopologyFromDaemon, Stale: true, Age: 7 * time.Second},
		}
	}

	m = applySessions(m, staleSelection())
	if got := m.filtered[m.cursor].TabID; got != 3 {
		t.Fatalf("cursor tab = %d, want the requested tab 3", got)
	}

	m.cursor = 0
	m = applySessions(m, staleSelection())
	if m.cursor != 0 {
		t.Fatalf("an already-consumed token moved the cursor to %d, want it left at 0", m.cursor)
	}
}
