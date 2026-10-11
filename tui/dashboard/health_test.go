package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/layout"
)

var healthNow = time.Unix(1_700_000_000, 0)

// healthyFacts is a reading in which every source has been observed and none of
// them has anything to report. Tests degrade exactly one field from it, so a
// failure names the field that caused it.
func healthyFacts() HealthFacts {
	return HealthFacts{
		Now:                healthNow,
		DaemonCheckedAt:    healthNow.Add(-3 * time.Second),
		DaemonAlive:        true,
		HooksCheckedAt:     healthNow.Add(-4 * time.Second),
		HookInSync:         []string{"work", "personal"},
		TopologyWarm:       true,
		TopologyAge:        time.Second,
		TopologyStaleAfter: 6 * time.Second,
		TopologySource:     "daemon",
		TopologyGeneration: 12,
	}
}

// healthSqueeze drops styling and every space, so a wrapped body can be compared
// against the text it was laid out from.
func healthSqueeze(s string) string {
	return strings.Join(strings.Fields(ansi.Strip(s)), "")
}

func healthIssueByID(t *testing.T, issues []HealthIssue, id string) HealthIssue {
	t.Helper()
	issue, ok := FindHealthIssue(issues, id)
	if !ok {
		t.Fatalf("no %q issue in reading", id)
	}
	return issue
}

func healthStates(t *testing.T, f HealthFacts) map[string]HealthState {
	t.Helper()
	issues := BuildHealthIssues(f)
	if len(issues) != len(healthIssueOrder) {
		t.Fatalf("got %d issues, want one per source (%d)", len(issues), len(healthIssueOrder))
	}
	states := make(map[string]HealthState, len(issues))
	for _, issue := range issues {
		if _, dup := states[issue.ID]; dup {
			t.Fatalf("issue %q built twice in one reading", issue.ID)
		}
		states[issue.ID] = issue.State
	}
	return states
}

func TestBuildHealthIssuesCoversEverySourceExactlyOnce(t *testing.T) {
	states := healthStates(t, healthyFacts())
	for _, id := range healthIssueOrder {
		if _, ok := states[id]; !ok {
			t.Fatalf("source %q produced no issue", id)
		}
	}
	for id, state := range states {
		if state != HealthOK {
			t.Fatalf("%s: got %s from a clean reading, want ok", id, state.Label())
		}
	}
	if got := HealthOverall(BuildHealthIssues(healthyFacts())); got != HealthOK {
		t.Fatalf("overall = %s, want ok", got.Label())
	}
	if got := HealthCompactLabel(BuildHealthIssues(healthyFacts())); got != "all checks ok" {
		t.Fatalf("compact label = %q, want %q", got, "all checks ok")
	}
}

// An unobserved check must not be reported as a healthy one — the review
// criterion the whole HealthUnknown state exists for. A zeroed observation time
// is indistinguishable from a failed check if it is resolved either way.
func TestUnobservedChecksAreUnknownRatherThanOK(t *testing.T) {
	f := healthyFacts()
	f.DaemonCheckedAt = time.Time{}
	f.DaemonAlive = false
	f.HooksCheckedAt = time.Time{}
	f.HookInSync = nil
	f.TopologyWarm = false
	f.TopologyAge = 0

	states := healthStates(t, f)
	for _, id := range []string{HealthIssueDaemon, HealthIssueHooks, HealthIssueTopology} {
		if states[id] != HealthUnknown {
			t.Fatalf("%s: got %s from an unobserved check, want unknown", id, states[id].Label())
		}
	}
	// Configuration is read at startup rather than polled, so it is the one
	// source with nothing to be unobserved about.
	if states[HealthIssueConfig] != HealthOK {
		t.Fatalf("config: got %s, want ok", states[HealthIssueConfig].Label())
	}

	issues := BuildHealthIssues(f)
	if got := HealthOverall(issues); got != HealthUnknown {
		t.Fatalf("overall = %s across three unobserved checks, want unknown", got.Label())
	}
	// Three outstanding checks are spelled, not counted: "3 issues" would claim
	// three things are wrong where nothing has been looked at yet.
	if got := HealthCompactLabel(issues); got != "checking health" {
		t.Fatalf("compact label = %q, want %q", got, "checking health")
	}
	daemon := healthIssueByID(t, issues, HealthIssueDaemon)
	if daemon.Observed != "never checked" {
		t.Fatalf("daemon observed = %q, want %q", daemon.Observed, "never checked")
	}
	if daemon.Hint != "" {
		t.Fatalf("daemon hint = %q, want none: an unrun check has no remediation", daemon.Hint)
	}
}

func TestDaemonDownIsFailedWithItsRemedy(t *testing.T) {
	f := healthyFacts()
	f.DaemonAlive = false

	issue := healthIssueByID(t, BuildHealthIssues(f), HealthIssueDaemon)
	if issue.State != HealthFailed {
		t.Fatalf("state = %s, want failed", issue.State.Label())
	}
	if issue.Remedy != "amx daemon start" {
		t.Fatalf("remedy = %q, want %q", issue.Remedy, "amx daemon start")
	}
	if issue.Hint != "daemon down: amx daemon start" {
		t.Fatalf("hint = %q", issue.Hint)
	}
	if !strings.HasPrefix(issue.Observed, "checked ") {
		t.Fatalf("observed = %q, want a checked-ago phrase", issue.Observed)
	}
}

// The severity sort must lead with the worst reading, and unknown must sort
// between ok and warning: an unrun check is a smaller claim than a clean one and
// a smaller claim than evidence of a problem.
func TestBuildHealthIssuesSortsByAttentionThenCanonicalOrder(t *testing.T) {
	f := healthyFacts()
	f.HooksCheckedAt = time.Time{} // hooks: unknown
	f.HookInSync = nil
	f.DaemonAlive = false                                                            // daemon: failed
	f.Deprecations = []HealthDeprecation{{Key: "accounts", Replacement: "profiles"}} // config: warning

	var got []string
	for _, issue := range BuildHealthIssues(f) {
		got = append(got, issue.ID)
	}
	want := []string{HealthIssueDaemon, HealthIssueConfig, HealthIssueHooks, HealthIssueTopology}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildHealthIssuesTieBreaksOnCanonicalOrder(t *testing.T) {
	f := healthyFacts()
	f.DaemonAlive = false
	f.HookDrifted = []string{"work"}

	issues := BuildHealthIssues(f)
	if issues[0].ID != HealthIssueDaemon || issues[1].ID != HealthIssueHooks {
		t.Fatalf("two failures tie-broke to %q,%q, want daemon,hooks", issues[0].ID, issues[1].ID)
	}
}

// The counts are the headline and the names are a detail, because the buckets
// literally carry both. Nothing may derive a profile from a count.
func TestHookDriftLeadsWithCountsAndNamesOnlyWhatItHolds(t *testing.T) {
	f := healthyFacts()
	f.HookInSync = []string{"work"}
	f.HookDrifted = []string{"personal"}
	f.HookMissing = []string{"review"}

	issue := healthIssueByID(t, BuildHealthIssues(f), HealthIssueHooks)
	if issue.State != HealthFailed {
		t.Fatalf("state = %s, want failed", issue.State.Label())
	}
	if issue.Summary != "1 drifted, 1 missing" {
		t.Fatalf("summary = %q, want the aggregate counts", issue.Summary)
	}
	joined := strings.Join(issue.Details, "\n")
	for _, want := range []string{
		"counted 1 in sync, 1 drifted, 1 missing",
		"drifted: personal",
		"missing: review",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("details %q missing %q", joined, want)
		}
	}
	if issue.Remedy != "amx config hooks-sync" {
		t.Fatalf("remedy = %q", issue.Remedy)
	}
}

// A profile whose check errored lands in none of the three buckets, so the
// counts are a floor rather than a census. The issue has to say so, or three
// small numbers stand for a complete survey that never happened.
func TestHookCheckErrorIsAWarningThatQualifiesItsCounts(t *testing.T) {
	f := healthyFacts()
	f.HookErr = "read /home/user/.claude/settings.json: permission denied"

	issue := healthIssueByID(t, BuildHealthIssues(f), HealthIssueHooks)
	if issue.State != HealthWarning {
		t.Fatalf("state = %s, want warning: a partial scan is not a failure and not a clean bill", issue.State.Label())
	}
	joined := strings.Join(issue.Details, "\n")
	if !strings.Contains(joined, f.HookErr) {
		t.Fatalf("details drop the error text: %q", joined)
	}
	if !strings.Contains(joined, "counted 2 in sync, 0 drifted, 0 missing") {
		t.Fatalf("details drop the counts: %q", joined)
	}
	if !strings.Contains(joined, "floor rather than a census") {
		t.Fatalf("details fail to qualify the counts: %q", joined)
	}
	if issue.Remedy != "amx config hooks-status" {
		t.Fatalf("remedy = %q", issue.Remedy)
	}
}

// An error outranks the unchecked arm even when no scan ever completed:
// something was attempted and it failed, which is an observation.
func TestHookCheckErrorOutranksNeverChecked(t *testing.T) {
	f := healthyFacts()
	f.HooksCheckedAt = time.Time{}
	f.HookInSync = nil
	f.HookErr = "boom"

	issue := healthIssueByID(t, BuildHealthIssues(f), HealthIssueHooks)
	if issue.State != HealthWarning {
		t.Fatalf("state = %s, want warning", issue.State.Label())
	}
}

// Cold and degraded are two different conditions with two different remedies. A
// tree that was never rendered leaves nothing on screen, so the refresh failure
// is the whole truth; once one has been rendered the same outage only ages it.
func TestColdTopologyFailureIsNotDegradedCachedData(t *testing.T) {
	cold := healthyFacts()
	cold.TopologyWarm = false
	cold.TopologyAge = 0
	cold.TopologyErr = "kitty: connection refused"

	issue := healthIssueByID(t, BuildHealthIssues(cold), HealthIssueTopology)
	if issue.State != HealthFailed {
		t.Fatalf("cold state = %s, want failed", issue.State.Label())
	}
	if !strings.Contains(issue.Summary, cold.TopologyErr) {
		t.Fatalf("cold summary = %q, want the refresh error as its headline", issue.Summary)
	}
	if issue.Remedy != "amx doctor --strict" {
		t.Fatalf("cold remedy = %q", issue.Remedy)
	}

	warm := healthyFacts()
	warm.TopologyAge = 30 * time.Second
	warm.TopologyErr = "kitty: connection refused"

	issue = healthIssueByID(t, BuildHealthIssues(warm), HealthIssueTopology)
	if issue.State != HealthWarning {
		t.Fatalf("warm state = %s, want warning: cached sessions are still on screen", issue.State.Label())
	}
	if issue.Summary != "showing cached sessions (30s old)" {
		t.Fatalf("warm summary = %q", issue.Summary)
	}
	if !strings.Contains(strings.Join(issue.Details, "\n"), "last refresh failed: "+warm.TopologyErr) {
		t.Fatalf("warm details drop the refresh error: %v", issue.Details)
	}
}

// A stale-data condition may not be hidden: neither by a fresh-looking age when
// the daemon has flagged the snapshot, nor by a clean state once the tree on
// screen has aged past the threshold with no message arriving.
func TestStaleTopologyIsAlwaysVisible(t *testing.T) {
	f := healthyFacts()
	f.TopologyAge = 6 * time.Second // exactly at the threshold
	issue := healthIssueByID(t, BuildHealthIssues(f), HealthIssueTopology)
	if issue.State != HealthWarning {
		t.Fatalf("at the threshold: state = %s, want warning", issue.State.Label())
	}

	// The daemon's own Stale verdict is reported ALONGSIDE the derived age
	// rather than instead of it, so a snapshot that is flagged but young still
	// says so somewhere the operator can read it.
	flagged := healthyFacts()
	flagged.TopologyStale = true
	issue = healthIssueByID(t, BuildHealthIssues(flagged), HealthIssueTopology)
	if issue.State != HealthOK {
		t.Fatalf("young flagged snapshot: state = %s, want ok", issue.State.Label())
	}
	joined := strings.Join(issue.Details, "\n")
	if !strings.Contains(joined, "marked this snapshot stale") {
		t.Fatalf("details hide the daemon's stale verdict: %q", joined)
	}
	if !strings.Contains(joined, "source: daemon snapshot (generation 12)") {
		t.Fatalf("details drop the provenance: %q", joined)
	}
	if !strings.Contains(issue.Observed, "on screen is 1s old") {
		t.Fatalf("observed = %q, want the on-screen age", issue.Observed)
	}
}

// A dismissal is scoped to the notice it was aimed at: it drops that issue from
// the attention set and moves nothing else.
func TestDismissalIsScopedToTheConfigurationNotice(t *testing.T) {
	f := healthyFacts()
	f.Deprecations = []HealthDeprecation{{Key: "accounts", Replacement: "profiles"}}
	f.DaemonAlive = false

	live := BuildHealthIssues(f)
	if got := len(HealthAttention(live)); got != 2 {
		t.Fatalf("attention = %d issues, want 2", got)
	}

	f.DeprecationsDismissed = true
	dismissed := BuildHealthIssues(f)

	config := healthIssueByID(t, dismissed, HealthIssueConfig)
	if !config.Dismissible || !config.Dismissed {
		t.Fatalf("config: dismissible=%v dismissed=%v, want both true", config.Dismissible, config.Dismissed)
	}
	if config.State != HealthWarning {
		t.Fatalf("config state = %s: a dismissal silences a notice, it does not resolve it", config.State.Label())
	}
	if config.WantsAttention() {
		t.Fatal("config still wants attention after an explicit dismissal")
	}

	attention := HealthAttention(dismissed)
	if len(attention) != 1 || attention[0].ID != HealthIssueDaemon {
		t.Fatalf("attention = %v, want the undismissed daemon failure alone", attention)
	}
	if got := HealthOverall(dismissed); got != HealthFailed {
		t.Fatalf("overall = %s: dismissing a notice must not suppress an unresolved failure", got.Label())
	}
	if got := HealthCompactLabel(dismissed); got != "daemon down" {
		t.Fatalf("compact label = %q, want the surviving issue's own phrase", got)
	}
	// The issue stays in the list at its own state; only its rank changes.
	if got := len(dismissed); got != len(healthIssueOrder) {
		t.Fatalf("dismissal dropped an issue from the list: %d remain", got)
	}
	if dismissed[len(dismissed)-1].ID == HealthIssueConfig {
		return
	}
	t.Fatalf("dismissed warning did not sort down to clean: order = %v", dismissed)
}

func TestDismissalOfTheOnlyIssueLeavesACleanReading(t *testing.T) {
	f := healthyFacts()
	f.Deprecations = []HealthDeprecation{{Key: "sessions", Replacement: "projects"}}
	f.DeprecationsDismissed = true

	issues := BuildHealthIssues(f)
	if got := HealthOverall(issues); got != HealthOK {
		t.Fatalf("overall = %s, want ok", got.Label())
	}
	if got := HealthCompactLabel(issues); got != "all checks ok" {
		t.Fatalf("compact label = %q", got)
	}
	if got := HealthHints(issues); len(got) != 0 {
		t.Fatalf("hints = %v, want none", got)
	}
}

// HealthHints is the single source the header strip draws from, so a hint an
// operator reads above the list and the entry they open in the overlay are two
// renderings of one issue.
func TestHealthHintsCarryOnlyUndismissedIssuesWithEstablishedCopy(t *testing.T) {
	f := healthyFacts()
	f.DaemonAlive = false
	f.HookDrifted = []string{"work"}
	f.Deprecations = []HealthDeprecation{{Key: "accounts", Replacement: "profiles"}}

	got := HealthHints(BuildHealthIssues(f))
	want := []string{"daemon down: amx daemon start", "hooks drifted: amx config hooks-sync"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("hints = %v, want %v (the configuration notice has no header hint)", got, want)
	}
}

func TestHealthCompactLabelDistinguishesEmptyFromClean(t *testing.T) {
	if got := HealthCompactLabel(nil); got != "" {
		t.Fatalf("label for a reading that has not been built = %q, want empty", got)
	}
	if got := HealthOverall(nil); got != HealthOK {
		t.Fatalf("overall for an empty list = %s", got.Label())
	}
}

func TestHealthStateGlyphsAndLabelsAreDistinct(t *testing.T) {
	seen := map[string]HealthState{}
	for _, s := range []HealthState{HealthUnknown, HealthOK, HealthWarning, HealthFailed} {
		if prev, dup := seen[s.Glyph()]; dup {
			t.Fatalf("%s reuses the glyph of %s", s.Label(), prev.Label())
		}
		seen[s.Glyph()] = s
		if layout.Width(s.Glyph()) != 1 {
			t.Fatalf("%s glyph %q is %d cells wide, want 1", s.Label(), s.Glyph(), layout.Width(s.Glyph()))
		}
	}
	if HealthUnknown.attentionWeight() <= HealthOK.attentionWeight() {
		t.Fatal("an unrun check must outrank a clean one")
	}
	if HealthUnknown.attentionWeight() >= HealthWarning.attentionWeight() {
		t.Fatal("an unrun check must not outrank evidence of a problem")
	}
}

// The body wraps rather than truncating: half a remedy cannot be run and half an
// error supports a conclusion its second clause would have ruled out.
func TestHealthLinesWrapRatherThanTruncate(t *testing.T) {
	f := healthyFacts()
	f.HookErr = "read /home/user/.config/agent-mux/profiles/personal/settings.json: permission denied"

	in := HealthInput{Issues: BuildHealthIssues(f), Width: 34, Height: 100}
	lines := HealthLines(in)

	// Reassembly is whitespace-insensitive because that is exactly what wrapping
	// spends: every continuation row re-pays the indent, and a break taken at a
	// space consumes it. What must survive is the text, not its layout.
	joined := healthSqueeze(strings.Join(lines, ""))
	for _, want := range []string{f.HookErr, "amx config hooks-status"} {
		if !strings.Contains(joined, healthSqueeze(want)) {
			t.Fatalf("wrapped body dropped %q", want)
		}
	}
	for i, line := range lines {
		if w := layout.Width(ansi.Strip(line)); w > in.Width {
			t.Fatalf("line %d is %d cells wide at width %d: %q", i, w, in.Width, ansi.Strip(line))
		}
	}
	// A narrower box must produce at least as many lines: wrapping trades width
	// for height rather than dropping content.
	narrow := in
	narrow.Width = 24
	if len(HealthLines(narrow)) <= len(lines) {
		t.Fatalf("narrowing from %d to %d did not add lines (%d vs %d)",
			in.Width, narrow.Width, len(HealthLines(narrow)), len(lines))
	}
}

func TestHealthLinesRenderEverySourceWithABlankBetween(t *testing.T) {
	in := HealthInput{Issues: BuildHealthIssues(healthyFacts()), Width: 60, Height: 100}
	body := ansi.Strip(strings.Join(HealthLines(in), "\n"))

	for _, want := range []string{
		"Daemon — running",
		"profiles in sync",
		"Sessions — refreshed 1s ago",
		"Configuration — no deprecated keys",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "\n\n"); got != len(healthIssueOrder)-1 {
		t.Fatalf("got %d blank separators, want %d", got, len(healthIssueOrder)-1)
	}
}

func TestHealthLinesAdvertiseDismissalOnlyOnTheDismissibleIssue(t *testing.T) {
	f := healthyFacts()
	f.Deprecations = []HealthDeprecation{{Key: "accounts", Replacement: "profiles"}}
	in := HealthInput{Issues: BuildHealthIssues(f), Width: 60, Height: 100}

	body := ansi.Strip(strings.Join(HealthLines(in), "\n"))
	if !strings.Contains(body, "press d to dismiss this notice for the session") {
		t.Fatalf("live notice does not advertise the key:\n%s", body)
	}
	if !strings.Contains(body, "accounts → profiles") {
		t.Fatalf("notice does not name the key and its replacement:\n%s", body)
	}

	f.DeprecationsDismissed = true
	in.Issues = BuildHealthIssues(f)
	body = ansi.Strip(strings.Join(HealthLines(in), "\n"))
	if strings.Contains(body, "press d to dismiss") {
		t.Fatalf("dismissed notice still advertises the key:\n%s", body)
	}
	if !strings.Contains(body, "dismissed for this session") {
		t.Fatalf("dismissed notice does not say so:\n%s", body)
	}
	if !strings.Contains(body, "1 deprecated key") {
		t.Fatalf("dismissed notice lost its reading:\n%s", body)
	}
}

func TestRenderHealthWindowsTheBodyAndHeadsWithTheOverallReading(t *testing.T) {
	f := healthyFacts()
	f.DaemonAlive = false
	in := HealthInput{Issues: BuildHealthIssues(f), Width: 60, Height: 5}

	top := ansi.Strip(RenderHealth(in))
	if !strings.HasPrefix(top, "Health · daemon down") {
		t.Fatalf("heading = %q", strings.SplitN(top, "\n", 2)[0])
	}
	if strings.Contains(top, "▲") {
		t.Fatal("unscrolled body shows a top marker")
	}
	if !strings.Contains(top, "▼") {
		t.Fatal("clipped body shows no bottom marker")
	}

	in.Offset = 3
	mid := ansi.Strip(RenderHealth(in))
	if !strings.Contains(mid, "▲") {
		t.Fatal("scrolled body shows no top marker")
	}
}

func TestRenderHealthSaysSoWhenThereIsNothingToShow(t *testing.T) {
	out := ansi.Strip(RenderHealth(HealthInput{Width: 40, Height: 6}))
	if !strings.Contains(out, "(no health checks available)") {
		t.Fatalf("empty reading rendered as %q", out)
	}
	if strings.Contains(out, "all checks ok") {
		t.Fatal("an unbuilt reading claimed a clean bill of health")
	}
}

// Tmux rows kept through an outage make the Sessions source a warning on a
// tree that is otherwise fresh, in the outage's own words.
func TestHealthReportsKeptTmuxSessions(t *testing.T) {
	facts := healthyFacts()
	if state := healthStates(t, facts)[HealthIssueTopology]; state != HealthOK {
		t.Fatalf("healthy reading = %v; want ok", state)
	}
	facts.PortableErr = "tmux is not answering; showing the last 2 sessions listed (timeout)"
	issue := healthIssueByID(t, BuildHealthIssues(facts), HealthIssueTopology)
	if issue.State != HealthWarning || issue.Summary != facts.PortableErr || issue.Compact != "tmux sessions stale" || !issue.WantsAttention() {
		t.Fatalf("issue = %+v; want a warning carrying the outage", issue)
	}
}
