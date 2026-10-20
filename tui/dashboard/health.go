// This file is the dashboard's health vocabulary: the one issue model every
// health fact the app holds is projected into, and the scrollable details view
// drawn from it.
//
// Three properties are the model rather than incidental to it.
//
// An UNOBSERVED check is not a healthy one. Every source carries its own
// observation time and answers HealthUnknown until it has one, because "the
// daemon is not alive" and "we have never asked" are the same zero bit read two
// ways, and a reading that resolved the second into the first would report a
// clean bill of health for a check that has never run.
//
// A COLD failure is not a DEGRADED one. A window tree that has never been
// rendered leaves nothing on screen, so a refresh failure is the whole truth and
// the issue is a failure. Once one has been rendered, the same outage only makes
// what is already shown older, which is a different state with a different
// remedy — and the age it is judged by is the caller's, resolved from the tree
// on screen rather than recomputed here from a capture time that says nothing
// about a refresh which never reached a daemon at all.
//
// A DISMISSAL is scoped to the notice it was aimed at. The configuration notice
// is the only dismissible issue; dismissing it drops it from what the reading
// calls ATTENTION and from nothing else. It stays in the list, it keeps its
// state, and no other issue's state moves with it — an operator who silenced a
// deprecated key last week must still be told the daemon is down today.

package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

// HealthState is what is currently known about one health source.
//
// HealthUnknown is deliberately its own state rather than a flavour of OK or of
// failure: a check that has not run yet has produced no evidence in either
// direction, and the only honest thing to say about it is that nobody has
// looked.
type HealthState int

const (
	// HealthUnknown means the check has never been observed.
	HealthUnknown HealthState = iota
	// HealthOK means the check was observed and found nothing wrong.
	HealthOK
	// HealthWarning means the check was observed and something is degraded or
	// could not be fully evaluated.
	HealthWarning
	// HealthFailed means the check was observed and the thing it checks is down.
	HealthFailed
)

// Label is the stable lower-case word for a state, used in machine-facing
// output and in tests.
func (s HealthState) Label() string {
	switch s {
	case HealthOK:
		return "ok"
	case HealthWarning:
		return "warning"
	case HealthFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Glyph is the one-cell mark drawn beside an issue. Unknown gets a hollow pip
// rather than a colored one, because the strip is read at a glance and a filled
// pip in any color claims an observation was made.
func (s HealthState) Glyph() string {
	switch s {
	case HealthOK:
		return "●"
	case HealthWarning:
		return "▲"
	case HealthFailed:
		return "✖"
	default:
		return "○"
	}
}

// attentionWeight orders the states by how much they want to be looked at. It
// is not the numeric order of the constants: HealthUnknown sits above HealthOK
// because an unrun check is a smaller claim than a clean one, and below the two
// states that carry actual evidence of a problem.
func (s HealthState) attentionWeight() int {
	switch s {
	case HealthFailed:
		return 3
	case HealthWarning:
		return 2
	case HealthUnknown:
		return 1
	default:
		return 0
	}
}

// Stable issue identifiers. They are the vocabulary the header entry point, the
// status bar, and the details overlay all name the same condition by, so a
// consumer can single one out without matching on its title.
const (
	HealthIssueDaemon   = "daemon"
	HealthIssueHooks    = "hooks"
	HealthIssueTopology = "topology"
	HealthIssueConfig   = "config"
)

// healthIssueOrder is the canonical order issues are built and tie-broken in.
var healthIssueOrder = []string{HealthIssueDaemon, HealthIssueHooks, HealthIssueTopology, HealthIssueConfig}

// HealthIssue is one health source, projected into the single shape every
// health surface presents.
//
// Summary, Compact, and Hint are three lengths of the same sentence rather than
// three facts: Summary is the details overlay's line, Compact is what a header
// with a dozen columns can afford, and Hint is the existing under-header wording
// this issue already had. An issue with no established one-line surface carries
// an empty Hint rather than inventing copy for a strip it does not appear in.
type HealthIssue struct {
	// ID is the stable identifier above.
	ID string
	// Title is the operator-facing name of the source.
	Title string
	// State is what the cached evidence says.
	State HealthState
	// Summary is the one-line reading, drawn beside the title.
	Summary string
	// Compact is the short phrase a header entry point spends on this issue when
	// it is the only one wanting attention.
	Compact string
	// Hint is the existing right-aligned header hint for this issue, or "" when
	// the issue has no such surface. It exists so the hint strip and the details
	// overlay cannot drift into two different accounts of the same condition.
	Hint string
	// Details are the diagnostic lines available under the summary: error text,
	// affected names, provenance. They are facts read out of the cached payload,
	// never inferred from it.
	Details []string
	// Remedy is the documented command that addresses the issue, or "" when the
	// state needs none.
	Remedy string
	// ObservedAt is when this source was last read, zero when it never was.
	ObservedAt time.Time
	// Observed is the rendered phrase for ObservedAt, resolved against the same
	// clock the rest of the projection used.
	Observed string
	// Dismissible reports that this issue may be silenced by an explicit
	// operator action. Only the configuration notice is.
	Dismissible bool
	// Dismissed reports that the operator has silenced it for this session. A
	// dismissed issue keeps its State and stays in the list; it is only dropped
	// from the attention set.
	Dismissed bool
}

// WantsAttention reports whether this issue should still be counted against the
// operator: anything the evidence does not call OK, minus an explicit dismissal.
func (i HealthIssue) WantsAttention() bool {
	return i.State != HealthOK && !i.Dismissed
}

// HealthDeprecation is one deprecated configuration key and the key that
// replaces it.
type HealthDeprecation struct {
	Key         string
	Replacement string
}

// HealthFacts is the complete cached evidence the issue model is built from.
//
// It is deliberately a value of plain scalars rather than the model's own types:
// projecting once, at the seam, is what keeps every derivation — which clock an
// age is measured from, which threshold separates fresh from stale — in the one
// place that owns it, and leaves this package with nothing to look up.
type HealthFacts struct {
	// Now is the clock ages are measured against, passed in so a projection is
	// reproducible.
	Now time.Time

	// DaemonCheckedAt is zero until the first daemon ping lands.
	DaemonCheckedAt time.Time
	DaemonAlive     bool

	// HooksCheckedAt is zero until the first hooks scan lands. The three slices
	// are the scan's own buckets of profile names; HookErr is the joined failure
	// for the profiles it could not read, which appear in NO bucket.
	HooksCheckedAt time.Time
	HookInSync     []string
	HookDrifted    []string
	HookMissing    []string
	HookErr        string

	// TopologyWarm reports that a window tree has been rendered at least once,
	// which is the whole of the cold/degraded distinction. TopologyAge is the
	// effective age of the tree currently on screen, already resolved by the
	// caller that owns that rule.
	TopologyWarm       bool
	TopologyAge        time.Duration
	TopologyStaleAfter time.Duration
	TopologySource     string
	TopologyStale      bool
	TopologyGeneration uint64
	TopologyErr        string
	// PortableErr is set while the tmux rows on screen are kept from an
	// earlier listing: the backend failed to answer, or its server is gone.
	PortableErr string

	Deprecations          []HealthDeprecation
	DeprecationsDismissed bool
}

// BuildHealthIssues projects the cached facts into the issue list, ordered by
// how much each wants attention and tie-broken by the canonical source order.
//
// Every source produces exactly one issue at every reading, including the ones
// with nothing to report. A list that omitted its healthy entries could not be
// told apart from one whose checks had not run.
func BuildHealthIssues(f HealthFacts) []HealthIssue {
	issues := []HealthIssue{
		healthDaemonIssue(f),
		healthHooksIssue(f),
		healthTopologyIssue(f),
		healthConfigIssue(f),
	}
	rank := make(map[string]int, len(healthIssueOrder))
	for i, id := range healthIssueOrder {
		rank[id] = i
	}
	sort.SliceStable(issues, func(a, b int) bool {
		wa, wb := healthAttentionWeight(issues[a]), healthAttentionWeight(issues[b])
		if wa != wb {
			return wa > wb
		}
		return rank[issues[a].ID] < rank[issues[b].ID]
	})
	return issues
}

// healthAttentionWeight sorts a dismissed issue as though it were clean. The
// operator has said they do not want to be led to it, and leading with it is
// exactly what a severity sort would otherwise do.
func healthAttentionWeight(i HealthIssue) int {
	if i.Dismissed {
		return 0
	}
	return i.State.attentionWeight()
}

// HealthAttention returns the issues still wanting the operator: every issue the
// evidence does not call OK, minus explicit dismissals.
func HealthAttention(issues []HealthIssue) []HealthIssue {
	var out []HealthIssue
	for _, issue := range issues {
		if issue.WantsAttention() {
			out = append(out, issue)
		}
	}
	return out
}

// HealthOverall is the worst state across the issues that still want attention,
// and HealthOK when none do. A dismissed warning does not raise it, which is
// what makes the dismissal worth pressing; a failure that nothing dismissed
// still does, which is what keeps the dismissal honest.
func HealthOverall(issues []HealthIssue) HealthState {
	worst := HealthOK
	for _, issue := range HealthAttention(issues) {
		if issue.State.attentionWeight() > worst.attentionWeight() {
			worst = issue.State
		}
	}
	return worst
}

// HealthCompactLabel is the reading a header entry point draws: the single
// issue's own phrase when exactly one is at the worst state, a count when
// several are, and the clean word when none want attention.
//
// It counts the WORST band rather than the whole attention set, and that is the
// load-bearing choice. The set mixes severities by construction — three sources
// are polled and one is read once at startup — so a straight count reports "two
// issues" for a deprecated config key sitting beside a window tree that simply
// has not rendered yet, and sends the operator into the overlay to find out
// which of the two is the news. Naming the worst band is the same information
// the severity sort already puts at the top of that overlay, so the one line the
// header can afford agrees with the first line the overlay opens on. Nothing is
// hidden: every issue, at every state, is in the list this label was computed
// from, and HealthOverall still paints the line the worst band's color.
//
// Several UNOBSERVED checks are spelled rather than counted, because "3 issues"
// is a claim that three things are wrong and a cold start is a claim that
// nothing has been looked at yet. A single unobserved check keeps its own
// phrase, which is more specific than either.
//
// It returns "" for an empty issue list, which is the "nothing has been built
// yet" case rather than a claim of health.
func HealthCompactLabel(issues []HealthIssue) string {
	if len(issues) == 0 {
		return ""
	}
	state := HealthOverall(issues)
	if state == HealthOK {
		return "all checks ok"
	}
	var worst []HealthIssue
	for _, issue := range HealthAttention(issues) {
		if issue.State == state {
			worst = append(worst, issue)
		}
	}
	if len(worst) == 1 {
		return worst[0].Compact
	}
	if state == HealthUnknown {
		return "checking health"
	}
	return fmt.Sprintf("%d issues", len(worst))
}

// HealthHints returns the established one-line header hints, in issue order.
// It is the single source the header strip draws from, so the hint an operator
// reads above the list and the entry they open in the details overlay are two
// renderings of one issue rather than two descriptions of one condition.
func HealthHints(issues []HealthIssue) []string {
	var out []string
	for _, issue := range issues {
		if issue.Hint != "" && issue.WantsAttention() {
			out = append(out, issue.Hint)
		}
	}
	return out
}

// FindHealthIssue returns the issue with the given ID, and false when the list
// carries none.
func FindHealthIssue(issues []HealthIssue, id string) (HealthIssue, bool) {
	for _, issue := range issues {
		if issue.ID == id {
			return issue, true
		}
	}
	return HealthIssue{}, false
}

// ── per-source projections ───────────────────────────────────────────────────

func healthDaemonIssue(f HealthFacts) HealthIssue {
	issue := HealthIssue{
		ID:         HealthIssueDaemon,
		Title:      "Daemon",
		ObservedAt: f.DaemonCheckedAt,
	}
	issue.Observed = healthObservedText(f.DaemonCheckedAt, f.Now)
	switch {
	case f.DaemonCheckedAt.IsZero():
		issue.State = HealthUnknown
		issue.Summary = "not checked yet"
		issue.Compact = "daemon unchecked"
	case f.DaemonAlive:
		issue.State = HealthOK
		issue.Summary = "running"
		issue.Compact = "daemon ok"
	default:
		issue.State = HealthFailed
		issue.Summary = "not running"
		issue.Compact = "daemon down"
		issue.Hint = "daemon down: amx daemon start"
		issue.Remedy = "amx daemon start"
		issue.Details = []string{"tab titles, notifications, and window state stop updating while it is down"}
	}
	return issue
}

// healthHooksIssue reads the cached hooks scan.
//
// Two honesty rules shape it. The counts are the buckets' own lengths and the
// named profiles are the buckets' own contents — nothing here derives a profile
// from a count. And a scan error means some profiles were never classified at
// all: they appear in none of the three buckets, so the counts are a floor
// rather than a census, and the issue says so instead of letting three small
// numbers stand for a complete survey.
func healthHooksIssue(f HealthFacts) HealthIssue {
	issue := HealthIssue{
		ID:         HealthIssueHooks,
		Title:      "Hooks",
		ObservedAt: f.HooksCheckedAt,
	}
	issue.Observed = healthObservedText(f.HooksCheckedAt, f.Now)
	inSync, drifted, missing := len(f.HookInSync), len(f.HookDrifted), len(f.HookMissing)
	counts := fmt.Sprintf("counted %d in sync, %d drifted, %d missing", inSync, drifted, missing)

	switch {
	case f.HookErr != "":
		// An error is itself an observation, so it outranks the unchecked case
		// even when no scan has completed: something was attempted and failed.
		issue.State = HealthWarning
		issue.Summary = "check failed"
		issue.Compact = "hooks check failed"
		issue.Hint = "hooks check failed: " + f.HookErr
		issue.Remedy = "amx config hooks-status"
		issue.Details = []string{
			f.HookErr,
			counts,
			"a profile whose check failed is in none of those counts, so they are a floor rather than a census",
		}
	case f.HooksCheckedAt.IsZero():
		issue.State = HealthUnknown
		issue.Summary = "not checked yet"
		issue.Compact = "hooks unchecked"
	case drifted == 0 && missing == 0:
		issue.State = HealthOK
		issue.Summary = fmt.Sprintf("%d profile%s in sync", inSync, healthPlural(inSync))
		issue.Compact = "hooks ok"
	default:
		issue.State = HealthFailed
		issue.Summary = fmt.Sprintf("%d drifted, %d missing", drifted, missing)
		issue.Compact = "hooks drifted"
		issue.Hint = "hooks drifted: amx config hooks-sync"
		issue.Remedy = "amx config hooks-sync"
		issue.Details = []string{counts}
		if drifted > 0 {
			issue.Details = append(issue.Details, "drifted: "+strings.Join(f.HookDrifted, ", "))
		}
		if missing > 0 {
			issue.Details = append(issue.Details, "missing: "+strings.Join(f.HookMissing, ", "))
		}
	}
	return issue
}

// healthTopologyIssue reads the window tree the dashboard is currently drawing.
//
// The cold arm and the warm arm answer two different questions. Cold, there is
// no tree at all and the only thing that matters is why; warm, there is one and
// the only thing that matters is how old it has become. A refresh error is
// reported in both, but it is the headline in the first and a footnote in the
// second, because a fresh tree with a failed retry behind it is still a fresh
// tree.
func healthTopologyIssue(f HealthFacts) HealthIssue {
	issue := HealthIssue{
		ID:    HealthIssueTopology,
		Title: "Sessions",
	}
	if !f.TopologyWarm {
		issue.Observed = "no session list has been rendered yet"
		if f.TopologyErr != "" {
			issue.State = HealthFailed
			issue.Summary = "no session list: " + f.TopologyErr
			issue.Compact = "sessions unavailable"
			issue.Remedy = "amx doctor --strict"
			issue.Details = []string{"the dashboard has never rendered a session list, so there is nothing cached to fall back on"}
			return issue
		}
		issue.State = HealthUnknown
		issue.Summary = "waiting for the first session list"
		issue.Compact = "sessions loading"
		return issue
	}

	age := f.TopologyAge
	if age < 0 {
		age = 0
	}
	issue.ObservedAt = f.Now.Add(-age)
	issue.Observed = "tree on screen is " + layout.AgeUnits(age) + " old"
	issue.Details = healthTopologyDetails(f)
	if f.TopologyStaleAfter > 0 && age >= f.TopologyStaleAfter {
		issue.State = HealthWarning
		issue.Summary = fmt.Sprintf("showing cached sessions (%ds old)", int(age/time.Second))
		issue.Compact = "sessions " + layout.AgeUnits(age) + " stale"
		if f.TopologyErr != "" {
			issue.Remedy = "amx doctor --strict"
		}
		return issue
	}
	if f.PortableErr != "" {
		// The tree is fresh and the tmux rows in it are not: they are the
		// last ones listed, kept because emptying the list would hide an
		// outage as an absence of sessions.
		issue.State = HealthWarning
		issue.Summary = f.PortableErr
		issue.Compact = "tmux sessions stale"
		issue.Remedy = "amx doctor --strict"
		return issue
	}
	issue.State = HealthOK
	issue.Summary = "refreshed " + layout.AgeUnits(age) + " ago"
	issue.Compact = "sessions ok"
	return issue
}

// healthTopologyDetails names the provenance of the warm tree. The daemon's own
// Stale verdict is reported alongside the derived age rather than instead of it:
// the flag describes one snapshot at the moment it answered, and the age
// describes what is on screen now, so a reading that showed only one of them
// would hide exactly the case where a refresh stopped arriving.
func healthTopologyDetails(f HealthFacts) []string {
	var details []string
	switch f.TopologySource {
	case "daemon":
		detail := "source: daemon snapshot"
		if f.TopologyGeneration > 0 {
			detail += fmt.Sprintf(" (generation %d)", f.TopologyGeneration)
		}
		details = append(details, detail)
	case "direct":
		details = append(details, "source: direct kitty listing")
	}
	if f.TopologyStale {
		details = append(details, "the daemon has marked this snapshot stale; it drives no kitty writes")
	}
	if f.TopologyErr != "" {
		details = append(details, "last refresh failed: "+f.TopologyErr)
	}
	return details
}

// healthConfigIssue reads the deprecated configuration keys captured at startup.
// It is the one dismissible issue, and the dismissal is recorded on it rather
// than applied by removing it: the notice is silenced, not resolved, and the
// details overlay is where the difference is still visible.
func healthConfigIssue(f HealthFacts) HealthIssue {
	issue := HealthIssue{
		ID:       HealthIssueConfig,
		Title:    "Configuration",
		Observed: "read from the configuration file at startup",
	}
	if len(f.Deprecations) == 0 {
		issue.State = HealthOK
		issue.Summary = "no deprecated keys"
		issue.Compact = "config ok"
		return issue
	}
	issue.State = HealthWarning
	issue.Summary = fmt.Sprintf("%d deprecated key%s", len(f.Deprecations), healthPlural(len(f.Deprecations)))
	issue.Compact = "config deprecated keys"
	issue.Remedy = "amx config edit"
	issue.Dismissible = true
	issue.Dismissed = f.DeprecationsDismissed
	for _, d := range f.Deprecations {
		issue.Details = append(issue.Details, d.Key+" → "+d.Replacement)
	}
	return issue
}

func healthObservedText(at, now time.Time) string {
	if at.IsZero() {
		return "never checked"
	}
	return "checked " + layout.AgeUnits(now.Sub(at)) + " ago"
}

func healthPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ── details overlay ──────────────────────────────────────────────────────────

var (
	styleHealthOK      = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary))
	styleHealthWarning = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleHealthFailed  = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
)

func healthStateStyle(s HealthState) lipgloss.Style {
	switch s {
	case HealthOK:
		return styleHealthOK
	case HealthWarning:
		return styleHealthWarning
	case HealthFailed:
		return styleHealthFailed
	default:
		return styleDim
	}
}

// HealthInput is the complete, explicit rendering state for the health details
// overlay's inner content. Width is the content width inside the overlay box and
// Height is the rows the scrollable body may spend including its ▲/▼ markers;
// the heading is drawn outside that budget.
type HealthInput struct {
	Issues []HealthIssue
	Width  int
	Height int
	Offset int
}

// healthDetailIndent is the column the sub-lines of an issue start at: the two
// cells its glyph and the following space occupy, plus two more so the block
// reads as subordinate to its heading.
const healthDetailIndent = "    "

// HealthLines is the complete, unwindowed body of the overlay: one block per
// issue, separated by a blank line. It is exported because the scroll arithmetic
// in the root needs the same line count the renderer will page through, and two
// answers to that question is how an offset ends up pointing past the end.
//
// Content WRAPS rather than truncating, which is why the line count depends on
// the width. A joined hooks error and a list of drifted profiles are the payload
// here, and half a diagnostic is worse than a scrollbar: an operator who reads
// `amx config hooks-syn` cannot run it, and one who reads the first clause of an
// error draws a conclusion the second clause would have ruled out.
func HealthLines(in HealthInput) []string {
	width := in.Width
	if width < 1 {
		width = 1
	}
	var lines []string
	for i, issue := range in.Issues {
		if i > 0 {
			lines = append(lines, "")
		}
		style := healthStateStyle(issue.State)
		if issue.Dismissed {
			style = styleDim
		}
		head := issue.State.Glyph() + " " + issue.Title + " — " + issue.Summary
		lines = append(lines, healthWrap(head, "", width, style)...)
		for _, detail := range issue.Details {
			lines = append(lines, healthSubLines(detail, width)...)
		}
		if issue.Observed != "" {
			lines = append(lines, healthSubLines(issue.Observed, width)...)
		}
		if issue.Remedy != "" {
			lines = append(lines, healthSubLines("fix: "+issue.Remedy, width)...)
		}
		if issue.Dismissed {
			lines = append(lines, healthSubLines("dismissed for this session", width)...)
		} else if issue.Dismissible {
			lines = append(lines, healthSubLines("press d to dismiss this notice for the session", width)...)
		}
	}
	return lines
}

func healthSubLines(text string, width int) []string {
	return healthWrap(text, healthDetailIndent, width, styleDim)
}

// healthWrap lays plain text out at width and renders each resulting row with
// style. The indent is charged against the width and repeated on every
// continuation row, so a wrapped detail stays both inside the box and visibly
// subordinate to the heading above it. Styling is applied per row rather than
// before wrapping, because the wrapper measures plain text.
func healthWrap(text, indent string, width int, style lipgloss.Style) []string {
	if text == "" {
		return nil
	}
	budget := width - layout.Width(indent)
	if budget < 1 {
		budget = 1
	}
	rows := layout.WrapHard(text, budget)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, style.Render(indent+row))
	}
	return out
}

// RenderHealth draws the overlay's inner content: a heading carrying the overall
// reading, then a window over HealthLines with its ▲/▼ markers.
func RenderHealth(in HealthInput) string {
	width := in.Width
	if width < 0 {
		width = 0
	}
	var sb strings.Builder
	sb.WriteString(healthHeader(in.Issues, width))
	sb.WriteByte('\n')

	lines := HealthLines(in)
	if len(lines) == 0 {
		sb.WriteString(styleDim.Render("  (no health checks available)"))
		sb.WriteByte('\n')
		return sb.String()
	}

	window := layout.ListWindowFor(len(lines), in.Offset, in.Height)
	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := window.Start; i < window.End; i++ {
		sb.WriteString(lines[i])
		sb.WriteByte('\n')
	}
	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// healthHeader names the overlay and states the overall reading beside it, in
// the same words the compact header entry point will use.
func healthHeader(issues []HealthIssue, width int) string {
	label := HealthCompactLabel(issues)
	line := "Health"
	if label != "" {
		line += " · " + label
	}
	return layout.StyledTruncate(healthStateStyle(HealthOverall(issues)).Render(line), width, "…")
}
