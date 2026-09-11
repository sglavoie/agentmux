package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// projectsInput renders the given rows in the Projects view at the given width
// with no height bound, so an assertion about arrangement is not also an
// assertion about the fit walk.
func projectsInput(rows []session.RunningSession, collapsed map[string]bool, width int) ListInput {
	return ListInput{
		Items:         BuildProjectItems(rows, collapsed),
		View:          ViewProjects,
		Width:         width,
		IdleThreshold: projectIdle,
		Now:           time.Unix(1_700_000_000, 0),
	}
}

func renderedLines(in ListInput) []string {
	return strings.Split(strings.TrimRight(stripAnsi(RenderList(in)), "\n"), "\n")
}

// lineWith returns the first rendered line containing want, failing the test
// when nothing does — a missing line is the more useful failure than an index
// out of range three assertions later.
func lineWith(t *testing.T, lines []string, want string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no rendered line contains %q; got:\n%s", want, strings.Join(lines, "\n"))
	return ""
}

// TestSummaryRowShowsChevronWorstStateLabelAndCounts is the summary's
// presentation contract, asserted on one line at a spacious width so every
// element is present at once.
func TestSummaryRowShowsChevronWorstStateLabelAndCounts(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "bolt", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, Base: "web", State: "key", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 3, Base: "cli", State: "stop", Directory: "/repo/cli", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	}
	line := lineWith(t, renderedLines(projectsInput(rows, nil, 140)), "monorepo")

	if !strings.Contains(line, summaryChevronExpanded) {
		t.Errorf("summary line has no expanded chevron: %q", line)
	}
	// The worst child is the key row, so the key icon is what the family shows —
	// not the first member's bolt.
	if !strings.Contains(line, "🔑") {
		t.Errorf("summary line does not carry its worst child's state icon: %q", line)
	}
	if strings.Contains(line, "⚡") {
		t.Errorf("summary line carries the first member's icon rather than the worst child's: %q", line)
	}
	for _, want := range []string{"1 attention", "1 active", "1 idle"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary tally is missing %q: %q", want, line)
		}
	}
	// The grouping key is built from filesystem paths and must never be shown.
	if strings.Contains(strings.Join(renderedLines(projectsInput(rows, nil, 140)), "\n"), "git:/repo") {
		t.Error("the family key reached a rendered surface")
	}
}

// TestSummaryRowOmitsEmptyBuckets asserts the tally reports what is there rather
// than a fixed three-slot template padded with zeros.
func TestSummaryRowOmitsEmptyBuckets(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, Base: "web", State: "bolt", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	}
	line := lineWith(t, renderedLines(projectsInput(rows, nil, 140)), "monorepo")

	if !strings.Contains(line, "2 active") {
		t.Errorf("summary tally does not report its two active members: %q", line)
	}
	for _, absent := range []string{"attention", "idle", "0 "} {
		if strings.Contains(line, absent) {
			t.Errorf("summary tally names an empty bucket (%q): %q", absent, line)
		}
	}
}

// TestCollapsedSummaryHidesChildrenAndKeepsItsUrgency asserts the two halves of
// folding: the children stop being lines, and the family keeps saying that one
// of them needs attention.
func TestCollapsedSummaryHidesChildrenAndKeepsItsUrgency(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "key", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	}
	folded := renderedLines(projectsInput(rows, map[string]bool{"git:/repo": true}, 140))

	if got, want := len(folded), 1; got != want {
		t.Fatalf("collapsed family rendered %d line(s), want %d:\n%s", got, want, strings.Join(folded, "\n"))
	}
	line := folded[0]
	if !strings.Contains(line, summaryChevronCollapsed) {
		t.Errorf("collapsed summary does not show the collapsed chevron: %q", line)
	}
	if strings.Contains(line, summaryChevronExpanded) {
		t.Errorf("collapsed summary still shows the expanded chevron: %q", line)
	}
	if !strings.Contains(line, "🔑") || !strings.Contains(line, "1 attention") {
		t.Errorf("collapsed summary stopped reporting the member that needs attention: %q", line)
	}
	for _, dir := range []string{"/repo/api", "/repo/web"} {
		if strings.Contains(line, dir) {
			t.Errorf("a folded member still rendered its own row content (%q): %q", dir, line)
		}
	}
}

// TestFamilyChildrenAreIndentedAndSingletonsAreNot pins the nesting the whole
// arrangement is read by: a member of a summarized family is offset from the
// left edge, and a leaf that stands for itself is not.
func TestFamilyChildrenAreIndentedAndSingletonsAreNot(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "bolt", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 3, Base: "solo", State: "bolt", Directory: "/solo", FamilyKey: "git:/solo", FamilyLabel: "solo"},
		{TabID: 4, Base: "orphan", State: "bolt", Directory: "/orphan"},
	}
	lines := renderedLines(projectsInput(rows, nil, 140))

	// Group borders are drawn here (all four rows are Active, so CountGroups is
	// 1 and there is no border), which keeps the leading-space comparison about
	// indentation and nothing else.
	child := lineWith(t, lines, "api")
	singleton := lineWith(t, lines, "solo")
	orphan := lineWith(t, lines, "orphan")

	indent := leadingSpaces(child) - leadingSpaces(singleton)
	if indent != childIndentWidth {
		t.Errorf("a family child is indented %d cell(s) past a singleton leaf, want %d\nchild:     %q\nsingleton: %q",
			indent, childIndentWidth, child, singleton)
	}
	if got, want := leadingSpaces(orphan), leadingSpaces(singleton); got != want {
		t.Errorf("an orphan leaf is indented %d, want %d (the same depth as a singleton)", got, want)
	}
	// The summary's leading run IS comparable to a session row's now that the
	// queue row opens with its own name, but the claim being made is about depth
	// rather than about the two agreeing, so it is stated against the pane edge.
	summary := lineWith(t, lines, "monorepo")
	if !strings.HasPrefix(summary, summaryChevronExpanded) {
		t.Errorf("the summary does not start at the pane edge, so it is not top-level: %q", summary)
	}
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// TestProjectsOmitsUrgencyHeadings asserts the heading suppression and, in the
// same fixture, that it is a property of the view rather than of the rows: the
// identical row set under ViewUrgency still draws its headings.
func TestProjectsOmitsUrgencyHeadings(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "key", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{TabID: 3, Base: "old", State: "stop", Directory: "/old"},
	}

	projects := strings.Join(renderedLines(projectsInput(rows, nil, 140)), "\n")
	for _, heading := range groupLabels {
		if strings.Contains(projects, heading+" (") {
			t.Errorf("Projects drew the %q heading:\n%s", heading, projects)
		}
	}
	if strings.Contains(projects, "\n\n") {
		t.Errorf("Projects drew a blank group separator:\n%q", projects)
	}

	urgency := ListInput{Items: BuildItems(rows), View: ViewUrgency, Width: 140, IdleThreshold: projectIdle, Now: time.Unix(1_700_000_000, 0)}
	rendered := stripAnsi(RenderList(urgency))
	if !strings.Contains(rendered, groupLabels[0]+" (") {
		t.Errorf("the same rows under ViewUrgency lost their headings, so the omission is not view-scoped:\n%s", rendered)
	}

	// The per-row urgency border stays in both views: it qualifies one row rather
	// than introducing a run of them.
	if !strings.Contains(projects, GroupBorderGlyph) {
		t.Errorf("Projects dropped the per-row urgency border along with the headings:\n%s", projects)
	}
}

// TestSummaryRowDropsItsTallyBeforeItsIdentity is the narrow-width contract. The
// tally goes whole rather than being cut, because a truncated "2 atten…" reads
// as a smaller number than it is; the chevron, the icon, and as much of the
// label as fits survive, and the line never draws past the pane.
func TestSummaryRowDropsItsTallyBeforeItsIdentity(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "key", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "a-rather-long-repository-name"},
		{TabID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "a-rather-long-repository-name"},
	}
	// The whole tally at this fixture's width. The label is long enough that 50
	// cells hold the identity but not the tally, which is the width the
	// all-or-nothing rule is actually decided at.
	const tally = "1 attention · 1 active"

	for _, width := range []int{140, 60, 50, 40, 20, 10, 4} {
		summary := renderedLines(projectsInput(rows, nil, width))[0]
		if got := len([]rune(summary)); got > width {
			t.Errorf("width %d: the summary drew %d cell(s) past the pane: %q", width, got-width, summary)
		}
		// The separator plus a count is the tally starting. Wherever it starts it
		// must also finish: a cut tally reads as a smaller number than it is.
		if strings.Contains(summary, "  1 ") && !strings.Contains(summary, tally) {
			t.Errorf("width %d: the tally was cut instead of dropped: %q", width, summary)
		}
		if width >= 60 && !strings.Contains(summary, tally) {
			t.Errorf("width %d: the tally was dropped while it still fit: %q", width, summary)
		}
		if width == 50 {
			// Wide enough for the identity, too narrow for the tally: the label
			// survives whole and the tally is gone.
			if !strings.Contains(summary, rows[0].FamilyLabel) {
				t.Errorf("width %d: the label was cut to make room for a tally that does not fit: %q", width, summary)
			}
			if strings.Contains(summary, "attention") {
				t.Errorf("width %d: a tally survived into a pane too narrow for it: %q", width, summary)
			}
		}
		if width >= 10 && !strings.Contains(summary, summaryChevronExpanded) {
			t.Errorf("width %d: the chevron was dropped before the tally: %q", width, summary)
		}
	}
}

// TestUnnamedFamilyRendersAPlaceholder asserts a family whose label was lost to
// a degraded probe says nothing rather than saying its key.
func TestUnnamedFamilyRendersAPlaceholder(t *testing.T) {
	rows := []session.RunningSession{
		{TabID: 1, Base: "api", State: "bolt", FamilyKey: "git:/repo"},
		{TabID: 2, Base: "web", State: "bolt", FamilyKey: "git:/repo"},
	}
	summary := renderedLines(projectsInput(rows, nil, 140))[0]
	if !strings.Contains(summary, unnamedFamilyLabel) {
		t.Errorf("an unnamed family rendered %q, want the placeholder", summary)
	}
	if strings.Contains(summary, "git:") {
		t.Errorf("an unnamed family fell back to its key: %q", summary)
	}
}

// TestProjectsPaginationChargesOneLinePerItem is the windowing claim. With no
// headings to charge, the line cost is exactly the item count, a folded family
// costs one line instead of three, and the fit walk emits as many lines as the
// budget allows and no more.
func TestProjectsPaginationChargesOneLinePerItem(t *testing.T) {
	rows := projectRows()
	expandedItems := BuildProjectItems(rows, nil)
	foldedItems := BuildProjectItems(rows, map[string]bool{"git:/repo/.git": true})

	scroll := ScrollInput{Items: expandedItems, IdleThreshold: projectIdle, GroupHeadings: false}
	if got, want := scroll.LineCost(0, expandedItems.Len()-1), expandedItems.Len(); got != want {
		t.Errorf("LineCost over the whole list = %d, want %d (one line per item, no headings)", got, want)
	}
	folded := ScrollInput{Items: foldedItems, IdleThreshold: projectIdle, GroupHeadings: false}
	if got, want := folded.LineCost(0, foldedItems.Len()-1), expandedItems.Len()-2; got != want {
		t.Errorf("folding a two-member family changed the cost to %d, want %d (its children stop being lines)", got, want)
	}

	// The renderer agrees with the math it was charged: rendering with a height
	// budget emits exactly that many lines, indicators included.
	for _, height := range []int{3, 5, 8} {
		in := projectsInput(rows, nil, 140)
		in.Height = height
		if got := len(renderedLines(in)); got > height {
			t.Errorf("height %d: the renderer emitted %d lines", height, got)
		}
	}

	// And the cursor the scroll math says fits is one the renderer actually
	// draws, for every offset/cursor pair the list admits.
	for cursor := 0; cursor < expandedItems.Len(); cursor++ {
		for offset := 0; offset <= cursor; offset++ {
			if !scroll.CursorFits(offset, cursor, 6) {
				continue
			}
			in := projectsInput(rows, nil, 140)
			in.Height, in.Offset, in.Cursor = 6, offset, cursor
			body := strings.Join(renderedLines(in), "\n")
			want := expectedRowMarker(expandedItems, cursor)
			if !strings.Contains(body, want) {
				t.Errorf("offset %d cursor %d: CursorFits said yes but %q is not in the rendered window:\n%s", offset, cursor, want, body)
			}
		}
	}
}

// expectedRowMarker returns a string that appears on the item's rendered line
// and nowhere else, so "the renderer drew the cursor line" can be asserted
// without reproducing the row layout.
//
// A session's marker is its display title plus one trailing space, which is what
// keeps `notes` from matching the `notes-2` row beside it: the Session column is
// padded to the pass's widest name whenever anything follows it, so a name is
// always followed by a space and never by the rest of a longer name. The
// directory it used to use is on no row at all now — it moved to the detail strip
// with the rest of the selected session's context.
func expectedRowMarker(list ItemList, i int) string {
	if s, ok := list.SessionAt(i); ok {
		return SessionDisplayTitle(s) + " "
	}
	it, _ := list.At(i)
	return it.FamilyLabel
}
