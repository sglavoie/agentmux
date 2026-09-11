package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

var healthOverlayNow = time.Unix(1_700_000_000, 0)

// healthModel is a dashboard whose health facts are all observed and all clean,
// on a terminal wide and tall enough for the overlay to render unclipped. Tests
// degrade one source from it so a failure names its cause.
func healthModel(t *testing.T) Model {
	t.Helper()
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 40
	m.now = func() time.Time { return healthOverlayNow }
	m.daemonAlive = true
	m.daemonHealthCheckedAt = healthOverlayNow.Add(-3 * time.Second)
	m.hookHealth = commands.HookHealth{InSync: []string{"work", "personal"}}
	m.hookHealthCheckedAt = healthOverlayNow.Add(-4 * time.Second)
	m.topologyObservedAt = healthOverlayNow.Add(-time.Second)
	m.topology = commands.TopologyFreshness{Source: commands.TopologyFromDaemon, Generation: 7}
	return m
}

func healthBody(m Model) string {
	return stripAnsi(strings.Join(dashboard.HealthLines(healthInput(m)), "\n"))
}

func TestOpenHealthRemembersTheViewItWasOpenedFrom(t *testing.T) {
	for _, from := range []Mode{ModeList, ModeSessionSearch, ModePlanSearch, ModeWorktrees} {
		m := healthModel(t)
		m.mode = from
		m.healthOffset = 9

		m.openHealth(from)
		if m.mode != ModeHealth {
			t.Fatalf("mode = %v, want ModeHealth", m.mode)
		}
		if m.healthOffset != 0 {
			t.Fatalf("offset = %d on open, want a fresh viewport", m.healthOffset)
		}
		if got := m.activeRootMode(); got != from {
			t.Fatalf("activeRootMode = %v under the overlay, want %v", got, from)
		}

		m.healthOffset = 4
		m.closeHealth()
		if m.mode != from {
			t.Fatalf("esc landed on %v, want the view the overlay was opened from (%v)", m.mode, from)
		}
		if m.healthOffset != 0 {
			t.Fatalf("offset = %d after close, want 0", m.healthOffset)
		}
	}
}

func TestHealthKeysCloseAndQuit(t *testing.T) {
	for _, key := range []string{keyEsc, keyQ} {
		m := healthModel(t)
		m.openHealth(ModeList)
		next, cmd := m.handleHealthKey(tea.KeyPressMsg{Code: rune(key[0])})
		if key == keyEsc {
			next, cmd = m.handleHealthKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		}
		if cmd != nil {
			t.Fatalf("%s produced a command; the overlay observes nothing", key)
		}
		if got := next.(Model).mode; got != ModeList {
			t.Fatalf("%s left mode %v, want ModeList", key, got)
		}
	}
}

// G must land on the LAST body line, not one short of it. The scroll window
// spends a row on its ▲ marker once scrolled, so total-minus-visible stops
// exactly one line early — and on a short terminal that line is the whole of the
// configuration reading.
func TestHealthScrollReachesTheFinalLine(t *testing.T) {
	m := healthModel(t)
	m.width, m.height = 62, 20
	m.hookHealthErr = errors.New("read /home/user/.config/agent-mux/profiles/personal/settings.json: permission denied")
	m.openHealth(ModeList)

	lines := dashboard.HealthLines(healthInput(m))
	maxOffset, page := healthScrollBounds(m)
	if page <= 0 || len(lines) <= page {
		t.Fatalf("body of %d lines at page %d does not overflow; the test proves nothing", len(lines), page)
	}

	next, _ := m.handleHealthKey(tea.KeyPressMsg{Code: 'G'})
	m = next.(Model)
	if m.healthOffset != maxOffset {
		t.Fatalf("G landed on offset %d, want %d", m.healthOffset, maxOffset)
	}

	out := stripAnsi(renderHealthBox(m))
	last := stripAnsi(lines[len(lines)-1])
	if !strings.Contains(out, strings.TrimSpace(last)) {
		t.Fatalf("final body line %q is not on screen at max offset:\n%s", strings.TrimSpace(last), out)
	}
	if strings.Contains(out, "▼") {
		t.Fatalf("bottom marker still drawn at max offset:\n%s", out)
	}

	next, _ = m.handleHealthKey(tea.KeyPressMsg{Code: 'g'})
	m = next.(Model)
	if m.healthOffset != 0 {
		t.Fatalf("g landed on offset %d, want 0", m.healthOffset)
	}
}

func TestHealthScrollKeysStayInBounds(t *testing.T) {
	base := func() Model {
		m := healthModel(t)
		m.width, m.height = 62, 20
		m.hookHealthErr = errors.New("a long enough failure to force the body past one page of a short terminal")
		m.openHealth(ModeList)
		return m
	}
	maxOffset, page := healthScrollBounds(base())
	if maxOffset <= 1 {
		t.Fatalf("max offset %d leaves nothing to scroll", maxOffset)
	}

	cases := []struct {
		key   tea.KeyPressMsg
		start int
		want  int
	}{
		{tea.KeyPressMsg{Code: 'j'}, 0, 1},
		{tea.KeyPressMsg{Code: 'k'}, 1, 0},
		{tea.KeyPressMsg{Code: 'k'}, 0, 0},
		{tea.KeyPressMsg{Code: 'j'}, maxOffset, maxOffset},
		{tea.KeyPressMsg{Code: tea.KeyDown}, 0, 1},
		{tea.KeyPressMsg{Code: tea.KeyUp}, 1, 0},
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, 0, min(page, maxOffset)},
		{tea.KeyPressMsg{Code: tea.KeyPgUp}, maxOffset, max(maxOffset-page, 0)},
		{tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, 0, min(page/2, maxOffset)},
		{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, 0, 0},
	}
	for _, tc := range cases {
		m := base()
		m.healthOffset = tc.start
		next, cmd := m.handleHealthKey(tc.key)
		if cmd != nil {
			t.Fatalf("%s produced a command; scrolling must observe nothing", tc.key.String())
		}
		if got := next.(Model).healthOffset; got != tc.want {
			t.Fatalf("%s from %d = %d, want %d", tc.key.String(), tc.start, got, tc.want)
		}
	}
}

// The overlay is a reader: no key in it may return a command that observes
// anything. commands.Health spawns a detached daemon when its ping fails, so a
// render or key path that could reach it would make opening an overlay a side
// effect on the operator's machine.
func TestHealthOverlayStartsNoObservation(t *testing.T) {
	m := healthModel(t)
	m.daemonAlive = false
	m.deprecations = []config.Deprecation{{Key: "accounts", Replacement: "profiles"}}
	m.openHealth(ModeList)

	for _, key := range []tea.KeyPressMsg{
		{Code: 'j'}, {Code: 'k'}, {Code: 'g'}, {Code: 'G'},
		{Code: tea.KeyPgDown}, {Code: tea.KeyPgUp},
		{Code: 'd', Mod: tea.ModCtrl}, {Code: 'u', Mod: tea.ModCtrl},
		{Code: tea.KeyEscape},
	} {
		next, cmd := m.handleHealthKey(key)
		if cmd != nil {
			t.Fatalf("%s returned a command", key.String())
		}
		after := next.(Model)
		if after.daemonHealthCheckedAt != m.daemonHealthCheckedAt || after.hookHealthCheckedAt != m.hookHealthCheckedAt {
			t.Fatalf("%s moved an observation timestamp", key.String())
		}
	}

	// Rendering is likewise pure: two renders of the same model agree, and
	// neither moves a fact.
	before := m
	first := renderHealthBox(m)
	if second := renderHealthBox(m); first != second {
		t.Fatal("renderHealthBox is not a pure function of the model")
	}
	if m.daemonHealthCheckedAt != before.daemonHealthCheckedAt || m.deprecationDismissed != before.deprecationDismissed {
		t.Fatal("rendering the overlay mutated a health fact")
	}
}

// `d` is scoped to the one notice it names. It may not silence a daemon that is
// down or hooks that have drifted, and the reading recomputed after it still
// carries them.
func TestDismissHealthNoticeIsScopedToTheConfigurationNotice(t *testing.T) {
	m := healthModel(t)
	m.daemonAlive = false
	m.hookHealth = commands.HookHealth{InSync: []string{"work"}, Drifted: []string{"personal"}}
	m.deprecations = []config.Deprecation{{Key: "accounts", Replacement: "profiles"}}
	m.openHealth(ModeList)

	if got := dashboard.HealthOverall(healthIssues(m)); got != dashboard.HealthFailed {
		t.Fatalf("overall before dismissal = %s, want failed", got.Label())
	}
	if healthDismissFooterBinding(m) == "" {
		t.Fatal("footer does not advertise d while a dismissible notice wants attention")
	}

	next, cmd := m.handleHealthKey(tea.KeyPressMsg{Code: 'd'})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("dismissal produced no toast")
	}
	if !m.deprecationDismissed {
		t.Fatal("dismissal did not record")
	}
	if m.mode != ModeHealth {
		t.Fatalf("dismissal left mode %v, want the overlay to stay open", m.mode)
	}

	issues := healthIssues(m)
	if got := dashboard.HealthOverall(issues); got != dashboard.HealthFailed {
		t.Fatalf("overall after dismissal = %s: an unresolved failure was suppressed", got.Label())
	}
	for _, id := range []string{dashboard.HealthIssueDaemon, dashboard.HealthIssueHooks} {
		issue, ok := dashboard.FindHealthIssue(issues, id)
		if !ok {
			t.Fatalf("%s issue vanished", id)
		}
		if !issue.WantsAttention() {
			t.Fatalf("%s stopped wanting attention when the config notice was dismissed", id)
		}
	}
	if !daemonHealthDegraded(m) || !hookHealthDegraded(m) {
		t.Fatal("status bar stopped reading the two failures as degraded")
	}
	if got := healthDismissFooterBinding(m); got != "" {
		t.Fatalf("footer still advertises d with nothing left to dismiss: %q", got)
	}

	// Pressing it again is a no-op rather than a second silencing.
	next, cmd = m.handleHealthKey(tea.KeyPressMsg{Code: 'd'})
	if cmd != nil {
		t.Fatal("a second d produced a toast with nothing to dismiss")
	}
	if !next.(Model).deprecationDismissed {
		t.Fatal("a second d un-dismissed the notice")
	}
}

func TestDismissHealthNoticeDoesNothingWithNoNotice(t *testing.T) {
	m := healthModel(t)
	m.daemonAlive = false
	m.openHealth(ModeList)

	if got := healthDismissFooterBinding(m); got != "" {
		t.Fatalf("footer advertises d with no dismissible notice: %q", got)
	}
	next, cmd := m.handleHealthKey(tea.KeyPressMsg{Code: 'd'})
	if cmd != nil {
		t.Fatal("d produced a toast with no notice to dismiss")
	}
	if next.(Model).deprecationDismissed {
		t.Fatal("d set the dismissal flag with no notice present")
	}
}

// A terminal that grew can leave an offset pointing past a body that now fits,
// which would render an empty box.
func TestHealthOffsetSurvivesAResize(t *testing.T) {
	m := healthModel(t)
	m.width, m.height = 62, 20
	m.hookHealthErr = errors.New("a long enough failure to push the body past one page of a short terminal")
	m.openHealth(ModeList)

	next, _ := m.handleHealthKey(tea.KeyPressMsg{Code: 'G'})
	m = next.(Model)
	if m.healthOffset == 0 {
		t.Fatal("G did not scroll; the test proves nothing")
	}

	wasAt := m.healthOffset

	m.width, m.height = 160, 60
	clampHealthScroll(&m)
	maxOffset, _ := healthScrollBounds(m)
	if maxOffset >= wasAt {
		t.Fatalf("growing the terminal did not shrink the maximum offset (%d from %d); the clamp is untested", maxOffset, wasAt)
	}
	if m.healthOffset != maxOffset {
		t.Fatalf("offset = %d after the resize, want the new maximum %d", m.healthOffset, maxOffset)
	}

	// Pulled back to the new maximum, the end of the body is still what is on
	// screen rather than a window hanging past it.
	out := stripAnsi(renderHealthBox(m))
	lines := dashboard.HealthLines(healthInput(m))
	if last := strings.TrimSpace(stripAnsi(lines[len(lines)-1])); !strings.Contains(out, last) {
		t.Fatalf("final body line %q is off screen after the resize:\n%s", last, out)
	}
}

// The header hint strip and the status bar now read the issue model rather than
// re-walking the raw fields, which is what keeps three surfaces from becoming
// three accounts of one condition.
func TestHealthSurfacesAgreeOnOneReading(t *testing.T) {
	m := healthModel(t)
	m.daemonAlive = false
	m.hookHealth = commands.HookHealth{InSync: []string{"work"}, Missing: []string{"personal"}}

	hints := stripAnsi(renderHealthHints(m))
	body := healthBody(m)
	for _, want := range []string{"daemon down: amx daemon start", "hooks drifted: amx config hooks-sync"} {
		if !strings.Contains(hints, want) {
			t.Fatalf("hint strip missing %q:\n%s", want, hints)
		}
		remedy := want[strings.Index(want, ": ")+2:]
		if !strings.Contains(body, remedy) {
			t.Fatalf("overlay body missing the remedy %q the hint strip advertises:\n%s", remedy, body)
		}
	}
	if !daemonHealthDegraded(m) {
		t.Fatal("status bar does not read a downed daemon as degraded")
	}
	if !hookHealthDegraded(m) {
		t.Fatal("status bar does not read missing hooks as degraded")
	}
}

// An unobserved check is neither degraded nor healthy. The status bar may not
// report it as degraded — that would be a claim about evidence nobody gathered —
// and the hint strip stays silent, exactly as it did before the issue model.
func TestUnobservedHealthIsNotReportedAsDegraded(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 40
	m.now = func() time.Time { return healthOverlayNow }

	if daemonHealthDegraded(m) || hookHealthDegraded(m) {
		t.Fatal("an unobserved check is reported as degraded")
	}
	if got := stripAnsi(renderHealthHints(m)); got != "" {
		t.Fatalf("hint strip = %q before any check ran, want silence", got)
	}
	issues := healthIssues(m)
	for _, id := range []string{dashboard.HealthIssueDaemon, dashboard.HealthIssueHooks} {
		issue, _ := dashboard.FindHealthIssue(issues, id)
		if issue.State != dashboard.HealthUnknown {
			t.Fatalf("%s = %s before any check ran, want unknown", id, issue.State.Label())
		}
	}
	if !strings.Contains(healthBody(m), "not checked yet") {
		t.Fatalf("overlay hides that a check has never run:\n%s", healthBody(m))
	}
}

// The pip strip's guard covers the two checks together, so the ordinary startup
// shows it with one check in and the other outstanding. Each pip has to carry
// its own observation state: read off the raw fields an unchecked hooks scan has
// empty buckets and painted the pip of a clean bill of health.
func TestHealthPipsDistinguishUnobservedFromObserved(t *testing.T) {
	hollow := dashboard.HealthUnknown.Glyph()

	// Daemon in, hooks outstanding. The hooks pip must not be a filled one.
	m := healthModel(t)
	m.hookHealth = commands.HookHealth{}
	m.hookHealthCheckedAt = time.Time{}
	out := stripAnsi(renderHealthPips(m))
	if got := strings.Count(out, healthPipGlyph); got != 1 {
		t.Fatalf("got %d filled pips with only the daemon observed:\n%q", got, out)
	}
	if !strings.Contains(out, hollow+" hooks") {
		t.Fatalf("unobserved hooks scan does not get the hollow pip:\n%q", out)
	}

	// Hooks in, daemon outstanding. The daemon pip must not claim an outage.
	m = healthModel(t)
	m.daemonAlive = false
	m.daemonHealthCheckedAt = time.Time{}
	out = stripAnsi(renderHealthPips(m))
	if !strings.Contains(out, hollow+" daemon") {
		t.Fatalf("unobserved daemon check does not get the hollow pip:\n%q", out)
	}

	// Both observed and clean: two filled pips, no hollow one.
	out = stripAnsi(renderHealthPips(healthModel(t)))
	if got := strings.Count(out, healthPipGlyph); got != 2 {
		t.Fatalf("got %d filled pips from two clean observed checks:\n%q", got, out)
	}
	if strings.Contains(out, hollow) {
		t.Fatalf("observed checks got a hollow pip:\n%q", out)
	}

	// Neither observed: the strip stays off entirely, as it always has.
	blank := loadSessions(testModel(), makeSessions())
	blank.width, blank.height = 120, 40
	if got := renderHealthPips(blank); got != "" {
		t.Fatalf("strip = %q before any check ran, want empty", got)
	}
}

// A warm tree that has aged past the daemon's own staleness threshold is a
// degraded reading the overlay must name, and a cold one that never rendered is
// a failure rather than stale data.
func TestHealthOverlayNamesStaleAndColdTopologySeparately(t *testing.T) {
	warm := healthModel(t)
	warm.topologyObservedAt = healthOverlayNow.Add(-protocol.KittySnapshotStaleAfter - time.Second)
	issue, _ := dashboard.FindHealthIssue(healthIssues(warm), dashboard.HealthIssueTopology)
	if issue.State != dashboard.HealthWarning {
		t.Fatalf("aged tree = %s, want warning", issue.State.Label())
	}
	if !strings.Contains(healthBody(warm), "showing cached sessions") {
		t.Fatalf("overlay hides that the session list is cached:\n%s", healthBody(warm))
	}

	cold := healthModel(t)
	cold.topologyObservedAt = time.Time{}
	cold.err = errors.New("kitty: connection refused")
	issue, _ = dashboard.FindHealthIssue(healthIssues(cold), dashboard.HealthIssueTopology)
	if issue.State != dashboard.HealthFailed {
		t.Fatalf("cold failure = %s, want failed", issue.State.Label())
	}
	if !strings.Contains(healthBody(cold), "no window tree: kitty: connection refused") {
		t.Fatalf("cold failure is not reported as one:\n%s", healthBody(cold))
	}
}

func TestHealthModeIsRegisteredAcrossDispatch(t *testing.T) {
	m := healthModel(t)
	m.openHealth(ModeList)

	// Key dispatch reaches the overlay's own handler rather than the list's.
	next, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	if got := next.(Model).healthOffset; got == 0 && len(dashboard.HealthLines(healthInput(m))) > healthVisibleHeight(m) {
		t.Fatal("j did not reach handleHealthKey through the mode dispatcher")
	}

	if got := uitestModeName(ModeHealth); got != "health" {
		t.Fatalf("uitest mode name = %q, want %q", got, "health")
	}
	if modalBoxRenderer(ModeHealth) == nil {
		t.Fatal("modal-fit detection has no renderer for ModeHealth")
	}
	if !modalUsesResponsiveWidth(ModeHealth) {
		t.Fatal("ModeHealth is not exempt from the clipping check it satisfies by wrapping")
	}
	if got := fallbackStatusText(m); got == "" {
		t.Fatal("status bar has no fallback text for ModeHealth")
	}

	footer := stripAnsi(renderFooter(m))
	for _, want := range []string{"j/k", "esc/q"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q:\n%s", want, footer)
		}
	}
}

// The composited view draws the overlay over a dimmed dashboard rather than
// replacing it.
func TestHealthOverlayComposites(t *testing.T) {
	m := healthModel(t)
	m.openHealth(ModeList)

	out := stripAnsi(m.View().Content)
	if !strings.Contains(out, "Health ·") {
		t.Fatalf("overlay is not on screen:\n%s", out)
	}
	if !strings.Contains(out, "Daemon — running") {
		t.Fatalf("overlay body is not on screen:\n%s", out)
	}
}
