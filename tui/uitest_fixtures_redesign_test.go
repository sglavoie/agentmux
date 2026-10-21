// Every fixture in uitest_fixtures_redesign.go is asserted here. Nothing in the
// harness discovers a fixture: a constructor that no test names is built by
// zero tests, and a world that silently stopped containing the collision, the
// overlap, or the off-screen lines it was built for would keep its scenarios
// green by no longer posing the problem at all.
//
// So each test below pins the TRAP rather than the rendering. The scenarios in
// docs/tui-testing/scenarios pin what today's screen shows; these pin that the
// fixture still has something to show, which is what a later re-tuning of the
// data could quietly undo.

package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// sessionByTitle finds a loaded row by its display title. Titles collide in
// duplicate_names on purpose, so callers there must use sessionsByTitle.
func sessionByTitle(t *testing.T, d *uitestDriver, title string) session.RunningSession {
	t.Helper()
	rows := sessionsByTitle(d, title)
	if len(rows) != 1 {
		t.Fatalf("rows titled %q = %d, want exactly 1", title, len(rows))
	}
	return rows[0]
}

func sessionsByTitle(d *uitestDriver, title string) []session.RunningSession {
	var out []session.RunningSession
	for _, row := range d.Model().sessions {
		if dashboard.SessionDisplayTitle(row) == title {
			out = append(out, row)
		}
	}
	return out
}

// TestDuplicateNamesFixturePosesEachRungOfTheLadder is the guard that keeps the
// fixture a collision fixture. Each pair must be separated by exactly one thing
// and by nothing else, because a pair that also differed in branch OR profile
// would let a disambiguator pass by reaching for whichever it happened to try
// first.
func TestDuplicateNamesFixturePosesEachRungOfTheLadder(t *testing.T) {
	t.Parallel()

	d, _ := newUITestFixtureDriver(mustFixture(t, "duplicate_names"), UITestSize{})
	cleanupUITestDriver(t, d)

	api := sessionsByTitle(d, "api")
	if len(api) != 2 {
		t.Fatalf("api rows = %d, want 2", len(api))
	}
	if api[0].Account != api[1].Account {
		t.Fatalf("the api pair differs by profile (%q/%q), so branch is not the only separator", api[0].Account, api[1].Account)
	}
	if api[0].Branch == api[1].Branch {
		t.Fatalf("the api pair shares branch %q, so nothing separates it", api[0].Branch)
	}

	web := sessionsByTitle(d, "web")
	if len(web) != 2 {
		t.Fatalf("web rows = %d, want 2", len(web))
	}
	if web[0].Branch != web[1].Branch {
		t.Fatalf("the web pair differs by branch (%q/%q), so it never reaches the profile rung", web[0].Branch, web[1].Branch)
	}
	if web[0].Account == web[1].Account {
		t.Fatalf("the web pair shares profile %q, so nothing separates it", web[0].Account)
	}

	docs := sessionsByTitle(d, "docs")
	if len(docs) != 2 {
		t.Fatalf("docs rows = %d, want 2", len(docs))
	}
	if docs[0].Branch != docs[1].Branch || docs[0].Account != docs[1].Account || docs[0].Directory != docs[1].Directory {
		t.Fatalf("the docs pair is separable before the last rung: %+v vs %+v", docs[0], docs[1])
	}
	if docs[0].WindowID == docs[1].WindowID {
		t.Fatal("the docs pair shares a window ID, so even identity cannot separate it")
	}

	// The control. A collision is a property of the live set, so the fixture
	// has to contain a row that a disambiguator must leave alone.
	if got := len(sessionsByTitle(d, "notes")); got != 1 {
		t.Fatalf("notes rows = %d, want exactly 1 uncollided row", got)
	}
}

// TestPausedOutputFixtureOverflowsThePane is the guard that keeps following and
// holding distinguishable. If the scrollback ever fit entirely in the pane, the
// two would render the same picture and every scenario built on this fixture
// would pass without testing anything.
func TestPausedOutputFixtureOverflowsThePane(t *testing.T) {
	t.Parallel()

	fx := mustFixture(t, "paused_output")
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	cleanupUITestDriver(t, d)

	lines := strings.Split(strings.TrimRight(fx.Text, "\n"), "\n")
	if len(lines) <= fx.Size.Height {
		t.Fatalf("scrollback is %d lines at height %d: the pane can never be shorter than the buffer", len(lines), fx.Size.Height)
	}
	// Numbered from 01 so an assertion names a position rather than content.
	if lines[0] != "output line 01" || lines[len(lines)-1] != "output line 40" {
		t.Fatalf("scrollback endpoints = %q…%q", lines[0], lines[len(lines)-1])
	}

	// No key opens the pane any more: the inspector is what the fixture's size
	// was chosen to afford, so the text reaches a pane on the first frame. The
	// assertion stays because the fixture is worthless if it does not.
	if !d.Model().inspectorActive() {
		t.Fatalf("no inspector at %dx%d, so the fixture's text never reaches a pane",
			d.Model().width, d.Model().height)
	}
	screen := strings.Join(d.Observe().Screen.Lines, "\n")
	if !strings.Contains(screen, "output line 40") {
		t.Fatal("the pane is not following: the last line of the buffer is off screen")
	}
	if strings.Contains(screen, "output line 01") {
		t.Fatal("the whole buffer fits on screen, so holding a position would look identical to following")
	}
}

// TestHealthStatesFixtureSaysSomethingTheDefaultCannot is the guard that keeps
// this fixture worth having. Every fixture without a Health block reports
// nothing checked, which reads as a daemon that is down — so a health fixture
// that merely repeated "degraded" would be indistinguishable from having no
// fixture at all.
func TestHealthStatesFixtureSaysSomethingTheDefaultCannot(t *testing.T) {
	t.Parallel()

	fx := mustFixture(t, "health_states")
	if fx.Health == nil {
		t.Fatal("fixture carries no Health block, so it reproduces the hermetic default")
	}
	if !fx.Health.DaemonAlive {
		t.Fatal("a dead daemon is what every other fixture already reports; this one exists to separate the two causes")
	}
	hooks := fx.Health.HookSummary
	if len(hooks.InSync) == 0 || len(hooks.Drifted) == 0 || len(hooks.Missing) == 0 {
		t.Fatalf("all three hook categories must be populated at once: %+v", hooks)
	}
	if hooks.Healthy() {
		t.Fatal("hook summary reports healthy, so nothing degrades")
	}

	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	cleanupUITestDriver(t, d)

	// Health is refreshed from the tick handler alone, so an untouched model has
	// checked nothing — and a zero checked-at clock is what suppresses the
	// degraded reading. The two halves of that are asserted rather than assumed,
	// because a fixture whose state only appeared after an action nobody took
	// would be a fixture that never appeared at all.
	if daemonHealthDegraded(d.Model()) || hookHealthDegraded(d.Model()) {
		t.Fatal("health is degraded before any refresh ran")
	}
	d.Tick(1)
	if err := d.Err(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if daemonHealthDegraded(d.Model()) {
		t.Fatal("the daemon reads as down; this fixture's daemon is alive")
	}
	if !hookHealthDegraded(d.Model()) {
		t.Fatal("the hook files do not read as degraded")
	}
}

// TestAttentionOverlapFixtureCarriesMoreThanOneReasonPerRow is the guard on the
// premise: rows that want the operator for several reasons at once, and one row
// that wants them for none. Today the dashboard can only show one reason per
// row, which is exactly why the fixture has to assert the facts rather than the
// rendering.
func TestAttentionOverlapFixtureCarriesMoreThanOneReasonPerRow(t *testing.T) {
	t.Parallel()

	d, _ := newUITestFixtureDriver(mustFixture(t, "attention_overlap"), UITestSize{})
	cleanupUITestDriver(t, d)

	blocked := sessionByTitle(t, d, "blocked")
	if blocked.State != "key" || blocked.Reason == "" || !blocked.Unseen {
		t.Fatalf("blocked carries fewer than two reasons: %+v", blocked)
	}
	failed := sessionByTitle(t, d, "failed")
	if failed.State != "error" || !failed.Unseen {
		t.Fatalf("failed carries fewer than two reasons: %+v", failed)
	}
	looping := sessionByTitle(t, d, "looping")
	if looping.State != "question" || looping.Reason == "" || !looping.Unseen || !looping.Looping {
		t.Fatalf("looping carries fewer than three reasons: %+v", looping)
	}
	// The single-reason control: without it, "two reasons" is not
	// distinguishable from "any reason at all".
	unread := sessionByTitle(t, d, "unread")
	if unread.State != "stop" || !unread.Unseen || unread.Reason != "" || unread.Looping {
		t.Fatalf("unread is not a single-reason row: %+v", unread)
	}

	// The discriminator. A recoverable tool warning is not a claim on the
	// operator, so it must stay out of the attention band even though its icon
	// is the loudest thing on the row.
	warned := sessionByTitle(t, d, "warned")
	if warned.State != "warning" {
		t.Fatalf("warned state = %q", warned.State)
	}
	if warned.Unseen {
		t.Fatal("the warning row is unseen, so it is no longer a clean not-your-problem control")
	}
	m := d.Model()
	if got := dashboard.StateGroup(warned, m.lastActivity, m.idleThreshold); got != 1 {
		t.Fatalf("the tool warning landed in group %d, want Active", got)
	}
	for _, row := range []session.RunningSession{blocked, failed, looping, unread} {
		if got := dashboard.StateGroup(row, m.lastActivity, m.idleThreshold); got != 0 {
			t.Fatalf("%s landed in group %d, want Needs Attention", dashboard.SessionDisplayTitle(row), got)
		}
	}
}

// TestRedesignFixturesAreReachableByCommandAction ties the new worlds to the
// new action: the fixtures exist for scenarios, and a scenario reaches another
// screen by naming a capability. A fixture the command action cannot be used
// against is a fixture that only literal-key scenarios can drive.
func TestRedesignFixturesAreReachableByCommandAction(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"duplicate_names", "paused_output", "health_states", "attention_overlap"} {
		t.Run(name, func(t *testing.T) {
			d, _ := newUITestFixtureDriver(mustFixture(t, name), UITestSize{})
			cleanupUITestDriver(t, d)
			if err := d.Command(commands.SearchPlans); err != nil {
				t.Fatalf("reach Plans by name: %v", err)
			}
			if d.Model().mode != ModePlanSearch {
				t.Fatalf("mode = %q, want plan_search", d.Model().mode.String())
			}
		})
	}
}

func mustFixture(t *testing.T, name string) uitestFixture {
	t.Helper()
	fx, err := uitestFixtureByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return fx
}
