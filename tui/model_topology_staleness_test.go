package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// A sustained Kitty outage is a degradation, not a failure: the rows the
// dashboard already has keep working, and the only thing that changes is that
// the operator is eventually told how old they are. These tests pin the two
// halves of that — when the warning appears, and that the red cold-failure
// error is not what appears instead.
//
// The READING is asserted on the health issue and the header indicator that
// draws it, not on the status bar. On the dashboard the status bar is the
// feedback slot for what the operator just did; a window tree's age is a
// standing condition rather than a response to a keypress, so it belongs to the
// health surface, and asserting it in both places is how the two would drift
// into two accounts of one outage. Each case below checks the dashboard status
// bar stays out of it, which is the de-duplication itself under test.

const staleWarningPrefix = "showing cached sessions"

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

// topologyIssue is the window tree's current reading, read out of the same issue
// set the header indicator and the details overlay are built from.
func topologyIssue(t *testing.T, m Model) dashboard.HealthIssue {
	t.Helper()
	issue, ok := dashboard.FindHealthIssue(healthIssues(m), dashboard.HealthIssueTopology)
	if !ok {
		t.Fatal("health reading carries no sessions issue")
	}
	return issue
}

func assertTopologyReading(t *testing.T, m Model, state dashboard.HealthState, summary string) {
	t.Helper()
	issue := topologyIssue(t, m)
	if issue.State != state {
		t.Fatalf("sessions health = %s, want %s (summary %q)", issue.State.Label(), state.Label(), issue.Summary)
	}
	if summary != "" && !strings.Contains(issue.Summary, summary) {
		t.Fatalf("sessions summary = %q, want to contain %q", issue.Summary, summary)
	}
	// The dashboard status bar is the feedback slot; a standing condition never
	// appears there.
	assertStatusBarLacks(t, m, staleWarningPrefix)
	assertStatusBarLacks(t, m, "Kitty error")
}

// TestStaleWarningOnsetIsExactlyTheSharedThreshold pins both edges of the one
// decision this feature makes. The threshold is protocol.KittySnapshotStaleAfter
// rather than a second copy of six seconds, so the dashboard and the daemon
// cannot drift apart about when topology has gone old.
func TestStaleWarningOnsetIsExactlyTheSharedThreshold(t *testing.T) {
	t.Parallel()

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
				assertTopologyReading(t, m, dashboard.HealthOK, "refreshed")
				return
			}
			assertTopologyReading(t, m, dashboard.HealthWarning, tc.want)
		})
	}
}

// TestStaleWarningAgeAdvancesWithoutAnyRefreshResult is the reason the age is
// derived at render time. During a real outage no message arrives at all, so an
// age computed when the last message landed would freeze at the moment the
// dashboard stopped being able to see Kitty.
func TestStaleWarningAgeAdvancesWithoutAnyRefreshResult(t *testing.T) {
	t.Parallel()

	clock := staleTestEpoch
	m := warmDashboard(&clock)

	for _, elapsed := range []time.Duration{6 * time.Second, 20 * time.Second, 5 * time.Minute} {
		clock = staleTestEpoch.Add(elapsed)
		want := fmt.Sprintf("%s (%ds old)", staleWarningPrefix, int(elapsed/time.Second))
		assertTopologyReading(t, m, dashboard.HealthWarning, want)
	}

	// Failing refreshes must not reset the age either: a failure carries no
	// capture, so the tree on screen is exactly as old as it was.
	clock = staleTestEpoch.Add(42 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))
	assertTopologyReading(t, m, dashboard.HealthWarning, staleWarningPrefix+" (42s old)")
}

// TestStaleWarningClearsOnTheNextSuccessfulTopology closes the loop: recovery is
// not a separate code path, it is the same stamping that made the warning
// possible, applied to a newer tree.
func TestStaleWarningClearsOnTheNextSuccessfulTopology(t *testing.T) {
	t.Parallel()

	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(30 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))
	assertTopologyReading(t, m, dashboard.HealthWarning, staleWarningPrefix)

	m = applySessions(m, commands.SessionsMsg{
		Sessions: makeSessions(),
		Topology: commands.TopologyFreshness{Source: commands.TopologyFromDirect, CapturedAt: clock},
	})
	assertTopologyReading(t, m, dashboard.HealthOK, "refreshed")
	if m.err != nil {
		t.Fatalf("recovery left err = %v, want it cleared", m.err)
	}
}

// TestRecoveryToAnAlreadyStaleSnapshotReportsTheNewAge covers the case the
// daemon's own verdict makes possible: a refresh can succeed and still hand back
// topology that is older than the threshold. The warning must then re-derive
// from the new capture rather than either clearing or continuing to grow.
func TestRecoveryToAnAlreadyStaleSnapshotReportsTheNewAge(t *testing.T) {
	t.Parallel()

	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(90 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))
	assertTopologyReading(t, m, dashboard.HealthWarning, staleWarningPrefix+" (90s old)")

	m = applySessions(m, commands.SessionsMsg{
		Sessions: makeSessions(),
		Topology: commands.TopologyFreshness{
			Source: commands.TopologyFromDaemon,
			Stale:  true,
			Age:    8 * time.Second,
		},
	})
	assertTopologyReading(t, m, dashboard.HealthWarning, staleWarningPrefix+" (8s old)")
}

// TestColdRefreshFailureStaysRed is the boundary the warning is carved out of.
// With no tree ever rendered there is nothing to degrade to, so the failure is
// the whole state of the dashboard and keeps its red error.
func TestColdRefreshFailureStaysRed(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width = 100
	m.now = func() time.Time { return staleTestEpoch }
	m = applyRefreshErr(m, errors.New("kitten @ ls: connection refused"))

	assertTopologyReading(t, m, dashboard.HealthFailed, "no session list: kitten @ ls: connection refused")
	// A cold failure is the whole state of the dashboard, so it is what the one
	// header indicator reads out — not a phrase buried under an overall count.
	if got := stripAnsi(headerHealthSegment(m).text); !strings.Contains(got, "sessions unavailable") {
		t.Fatalf("header indicator = %q, want the cold-failure reading", got)
	}
	if got := dashboard.HealthOverall(healthIssues(m)); got != dashboard.HealthFailed {
		t.Fatalf("overall health = %s on a cold failure, want failed", got.Label())
	}
}

// TestWarmRefreshFailureIsQuietThenNonfatal is the control for the test above:
// the same error is not red once there is a list to keep showing, and it never
// becomes red — it becomes a warning.
func TestWarmRefreshFailureIsQuietThenNonfatal(t *testing.T) {
	t.Parallel()

	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(2 * time.Second)
	m = applyRefreshErr(m, errors.New("kitten @ ls: signal: killed"))

	assertTopologyReading(t, m, dashboard.HealthOK, "refreshed")

	clock = staleTestEpoch.Add(11 * time.Second)
	assertTopologyReading(t, m, dashboard.HealthWarning, staleWarningPrefix)
	if got := topologyIssue(t, m).State; got == dashboard.HealthFailed {
		t.Fatal("a warm outage hardened into a failure")
	}
}

// TestStaleWarningNeverDisplacesAnActionError keeps the two surfaces apart. An
// action error reports something the operator just asked for and has no
// relationship to how old the window tree is, so it owns the feedback slot; the
// standing condition stays in the header indicator, where it is still readable
// at the same moment rather than queued behind the error.
func TestStaleWarningNeverDisplacesAnActionError(t *testing.T) {
	t.Parallel()

	clock := staleTestEpoch
	m := warmDashboard(&clock)
	clock = staleTestEpoch.Add(30 * time.Second)
	m.actionErr = errors.New("close tab: no such window")

	assertStatusBarContains(t, m, "Action error: close tab: no such window")
	assertStatusBarLacks(t, m, staleWarningPrefix)
	if got := stripAnsi(headerHealthSegment(m).text); !strings.Contains(got, "sessions 30s stale") {
		t.Fatalf("header indicator = %q, want the stale reading beside the action error", got)
	}
}

// TestTopologyAgeClampsABackwardsClock covers a clock that moved backwards
// between the capture and the render. The answer is zero, not a negative age
// formatted into the warning.
func TestTopologyAgeClampsABackwardsClock(t *testing.T) {
	t.Parallel()

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
	assertTopologyReading(t, m, dashboard.HealthOK, "refreshed")
}

// TestStaleTopologyStillRebuildsTheListAndKeepsTheCursor pins the rendering half
// of the stale rule. A stale tree is not quarantined: it replaces the list like
// any other, and the row the operator had selected follows it across the
// rebuild even though the tree it came from is old enough to be unwritable.
func TestStaleTopologyStillRebuildsTheListAndKeepsTheCursor(t *testing.T) {
	t.Parallel()

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
	assertTopologyReading(t, m, dashboard.HealthWarning, staleWarningPrefix+" (7s old)")
}

// TestStaleTopologyConsumesAnUnseenSelectionTokenExactlyOnce is the model-side
// consequence of deferring the user-var clear: because the request is still on
// the window, the dashboard sees the same token on every stale refresh and must
// honour it once rather than re-stealing the cursor on each one.
func TestStaleTopologyConsumesAnUnseenSelectionTokenExactlyOnce(t *testing.T) {
	t.Parallel()

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
