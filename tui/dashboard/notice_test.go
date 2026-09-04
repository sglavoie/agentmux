// These tests pin the notice gutter: the two cells to the left of every row
// that carry a transient state-transition pulse, a durable unread-completion
// dot, or nothing at all. The two questions they keep separate are what the
// gutter SAYS about one line and what it COSTS every other line, because the
// column is reserved for a whole render pass and a marker that reserved without
// drawing (or drew without reserving) would move every row two cells sideways.

package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// noticeInput renders rows in the urgency view at the given width with no
// height bound and no pulse layer, so an assertion about the gutter is not also
// an assertion about the fit walk.
func noticeInput(rows []session.RunningSession, width int) ListInput {
	return ListInput{
		Cfg:           &config.Config{},
		Items:         BuildItems(rows),
		Width:         width,
		IdleThreshold: projectIdle,
		Now:           time.Unix(1_700_000_000, 0),
	}
}

// pulsingWindows returns the pulse predicate that reports exactly the given
// window IDs as pulsing.
func pulsingWindows(ids ...int) func(int) bool {
	return func(windowID int) bool {
		for _, id := range ids {
			if id == windowID {
				return true
			}
		}
		return false
	}
}

// contentOffset is the cell the row's own content starts at, measured from a
// token only that row carries. It is how a reserved-but-blank gutter is told
// apart from no gutter at all: both look like whitespace, and only the offset
// says which one was drawn.
func contentOffset(t *testing.T, line, token string) int {
	t.Helper()
	i := strings.Index(line, token)
	if i < 0 {
		t.Fatalf("rendered line %q does not contain %q", line, token)
	}
	return layout.Width(line[:i])
}

// isRowLine reports whether a STRIPPED rendered line stands for an item rather
// than for the chrome around one: a group heading, a blank separator, or a
// scroll indicator.
func isRowLine(stripped string) bool {
	trimmed := strings.TrimSpace(stripped)
	if trimmed == "" || trimmed == "▲" || trimmed == "▼" {
		return false
	}
	return !strings.HasPrefix(trimmed, "Needs Attention (") &&
		!strings.HasPrefix(trimmed, "Active (") &&
		!strings.HasPrefix(trimmed, "Idle (")
}

// rowLines drops the group headings and the scroll indicators, leaving the
// lines that stand for an item.
func rowLines(lines []string) []string {
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		if isRowLine(line) {
			rows = append(rows, line)
		}
	}
	return rows
}

// TestUnreadStopMarksItsRowAndASeenStopDoesNot is the marker's whole claim:
// two rows the harness cannot tell apart — both resting on a finished turn —
// are separated by whether the operator has looked at them.
func TestUnreadStopMarksItsRowAndASeenStopDoesNot(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "novel", State: "stop", Account: "work", Unseen: true},
		{WindowID: 2, Base: "viewed", State: "stop", Account: "work"},
	}
	lines := rowLines(renderedLines(noticeInput(rows, 100)))
	if len(lines) != 2 {
		t.Fatalf("expected 2 rendered rows, got %d: %q", len(lines), lines)
	}
	unread := lineWith(t, lines, "novel")
	read := lineWith(t, lines, "viewed")

	if !strings.Contains(unread, UnreadNoticeGlyph) {
		t.Errorf("the unseen stop carries no unread dot: %q", unread)
	}
	if strings.Contains(read, UnreadNoticeGlyph) {
		t.Errorf("a stop the operator has already seen still claims to be news: %q", read)
	}
	// The seen row pays the column it does not draw in, or the two rows would
	// not line up.
	if got, want := contentOffset(t, read, "viewed"), contentOffset(t, unread, "novel"); got != want {
		t.Errorf("seen row content starts at cell %d and the unread row at %d; both must share one gutter", got, want)
	}
}

// TestUnseenBitOnANonStopStateDrawsNothing pins the fail-closed half of the
// leaf rule. Unseen is a bare bool on a struct any caller may build, and a
// session that is still working has not completed anything to be unread about.
func TestUnseenBitOnANonStopStateDrawsNothing(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "busy", State: "bolt", Account: "work", Unseen: true},
		{WindowID: 2, Base: "asking", State: "key", Account: "work", Unseen: true},
		{WindowID: 3, Base: "odd", State: "not-a-state", Account: "work", Unseen: true},
	}
	in := noticeInput(rows, 100)
	if anyItemHasNotice(in) {
		t.Fatal("an unseen bit on a non-stop state reserved the gutter")
	}
	out := stripAnsi(RenderList(in))
	if strings.Contains(out, UnreadNoticeGlyph) {
		t.Errorf("an unseen bit on a non-stop state drew a completion dot:\n%s", out)
	}
}

// TestGutterIsRemovedEntirelyWhenNoLineHasNews measures the column by its cost:
// the same rows with nothing to say render exactly noticeColumnWidth cells
// further left, so the gutter is genuinely absent rather than blank.
func TestGutterIsRemovedEntirelyWhenNoLineHasNews(t *testing.T) {
	marked := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "stop", Account: "work", Unseen: true},
	}
	quiet := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "stop", Account: "work"},
	}
	markedLine := lineWith(t, rowLines(renderedLines(noticeInput(marked, 100))), "api")
	quietLine := lineWith(t, rowLines(renderedLines(noticeInput(quiet, 100))), "api")

	if got, want := contentOffset(t, markedLine, "api")-contentOffset(t, quietLine, "api"), noticeColumnWidth; got != want {
		t.Errorf("the gutter cost %d cells, want %d\nmarked: %q\nquiet:  %q", got, want, markedLine, quietLine)
	}
}

// TestPulseOutranksTheUnreadDotAndTheDotOutlivesIt is the precedence rule and
// its fallback in one pass. Both notices can be true of the same row: the pulse
// is that row's news said louder, and when it decays the completion is still
// unread, because seenness is not cleared by time or by a keypress.
func TestPulseOutranksTheUnreadDotAndTheDotOutlivesIt(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "stop", Account: "work", Unseen: true},
	}
	pulsing := noticeInput(rows, 100)
	pulsing.ActivePulse = pulsingWindows(1)

	withPulse := lineWith(t, rowLines(renderedLines(pulsing)), "api")
	if !strings.Contains(withPulse, GroupBorderGlyph) {
		t.Errorf("a pulsing unread row does not draw the pulse glyph: %q", withPulse)
	}
	if strings.Contains(withPulse, UnreadNoticeGlyph) {
		t.Errorf("a pulsing row drew both notices at once: %q", withPulse)
	}

	// The pulse decays (or a ModeList keypress clears the whole pulse layer);
	// nothing about the session changed.
	decayed := lineWith(t, rowLines(renderedLines(noticeInput(rows, 100))), "api")
	if !strings.Contains(decayed, UnreadNoticeGlyph) {
		t.Errorf("the completion stopped being unread when its pulse decayed: %q", decayed)
	}
	if got, want := contentOffset(t, decayed, "api"), contentOffset(t, withPulse, "api"); got != want {
		t.Errorf("row content moved from cell %d to %d when the pulse decayed; both notices share one gutter", want, got)
	}
}

// TestMarkedAndUnmarkedRowsShareOneGutterColumn is the alignment claim over a
// mixed pass: one row has news, three do not, and all four start their content
// at the same cell.
func TestMarkedAndUnmarkedRowsShareOneGutterColumn(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "one", State: "key", Account: "work"},
		{WindowID: 2, Base: "two", State: "stop", Account: "work", Unseen: true},
		{WindowID: 3, Base: "three", State: "key", Account: "work"},
		{WindowID: 4, Base: "four", State: "key", Account: "work"},
	}
	lines := rowLines(renderedLines(noticeInput(rows, 100)))
	if len(lines) != 4 {
		t.Fatalf("expected 4 rendered rows, got %d: %q", len(lines), lines)
	}
	want := contentOffset(t, lines[0], "one")
	for i, name := range []string{"one", "two", "three", "four"} {
		if got := contentOffset(t, lines[i], name); got != want {
			t.Errorf("row %q starts at cell %d, want %d: %q", name, got, want, lines[i])
		}
	}
	if marked := strings.Count(strings.Join(lines, "\n"), UnreadNoticeGlyph); marked != 1 {
		t.Errorf("expected exactly one unread dot in the pass, got %d:\n%s", marked, strings.Join(lines, "\n"))
	}
}

// TestGutterSitsOutsideTheGroupBorderAndSurvivesNarrowWidths pins the two
// composition invariants a new leftmost element could break: the order the
// prefixes are stacked in, and the ANSI-aware clamp that keeps a composed row
// inside the pane it was given.
func TestGutterSitsOutsideTheGroupBorderAndSurvivesNarrowWidths(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "unread", State: "stop", Account: "work", Directory: "~/p", Unseen: true},
		{WindowID: 2, Base: "busy", State: "bolt", Account: "work", Directory: "~/p"},
		{WindowID: 3, Base: "done", State: "stop", Account: "work", Directory: "~/p"},
	}
	// Three urgency buckets, so borders and headings are both drawn.
	lines := rowLines(renderedLines(noticeInput(rows, 100)))
	if len(lines) != 3 {
		t.Fatalf("expected 3 rendered rows, got %d: %q", len(lines), lines)
	}
	if got, want := lines[0][:len([]byte(UnreadNoticeGlyph+" "+GroupBorderGlyph+" "))], UnreadNoticeGlyph+" "+GroupBorderGlyph+" "; got != want {
		t.Errorf("marked row prefix = %q, want %q (notice gutter outside the group border)", got, want)
	}
	for _, line := range lines[1:] {
		if got, want := line[:len([]byte("  "+GroupBorderGlyph+" "))], "  "+GroupBorderGlyph+" "; got != want {
			t.Errorf("unmarked row prefix = %q, want %q", got, want)
		}
	}

	// Row lines only: an urgency heading is not clamped to the pane and never
	// was, which is a separate (and pre-existing) claim from the composed row's
	// ANSI-aware truncation.
	//
	// Both measurements are made, because they can disagree in exactly the way a
	// new styled prefix would cause: the stripped line is what the terminal
	// shows, and the styled line is what the truncation actually cut, so a clamp
	// that counted escape bytes as cells would pass one and fail the other.
	for _, width := range []int{80, 40, 20, 10, 4, 2, 1} {
		in := noticeInput(rows, width)
		stripped := renderedLines(in)
		styled := strings.Split(strings.TrimRight(RenderList(in), "\n"), "\n")
		if len(stripped) != len(styled) {
			t.Fatalf("at width %d the stripped render has %d lines and the styled render %d", width, len(stripped), len(styled))
		}
		for i, line := range stripped {
			if !isRowLine(line) {
				continue
			}
			if got := layout.Width(line); got > width {
				t.Errorf("at width %d a stripped line rendered %d cells wide: %q", width, got, line)
			}
			if got := layout.StyledWidth(styled[i]); got > width {
				t.Errorf("at width %d a styled line rendered %d cells wide: %q", width, got, styled[i])
			}
		}
	}
}

// TestOffScreenNoticeReservesTheGutterSoScrollingDoesNotShiftColumns is why the
// reservation scans the whole projection rather than the rendered window. Every
// row here is in one urgency bucket, so the only thing that could move a column
// is the gutter itself.
func TestOffScreenNoticeReservesTheGutterSoScrollingDoesNotShiftColumns(t *testing.T) {
	rows := make([]session.RunningSession, 0, 12)
	for i := 0; i < 11; i++ {
		rows = append(rows, session.RunningSession{WindowID: i + 1, Base: "row", State: "key", Account: "work", Directory: "~/p"})
	}
	// The only marked row is the last one, far below any three-line window.
	rows = append(rows, session.RunningSession{WindowID: 99, Base: "tail", State: "stop", Account: "work", Directory: "~/p", Unseen: true})

	top := noticeInput(rows, 100)
	top.Height = 4
	bottom := top
	bottom.Offset = len(rows) - 1

	topLines := rowLines(renderedLines(top))
	if len(topLines) == 0 {
		t.Fatal("no rows rendered at the top of the list")
	}
	topOffset := contentOffset(t, topLines[0], "row")

	bottomLines := rowLines(renderedLines(bottom))
	tail := lineWith(t, bottomLines, "tail")
	if !strings.Contains(tail, UnreadNoticeGlyph) {
		t.Errorf("the scrolled-to unread row lost its dot: %q", tail)
	}
	if got := contentOffset(t, tail, "tail"); got != topOffset {
		t.Errorf("content starts at cell %d after scrolling and %d before; an off-screen notice must reserve the gutter", got, topOffset)
	}
}

// TestCollapsedFamilyRollsUpUnreadAndAnExpandedOneLeavesItToTheLeaf is the two
// halves of family aggregation. A folded family's members project to no lines,
// so the summary is the only place their news can be said; an unfolded one's
// members are lines of their own, so saying it again on the summary would print
// the same news twice on adjacent rows.
func TestCollapsedFamilyRollsUpUnreadAndAnExpandedOneLeavesItToTheLeaf(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "bolt", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{WindowID: 2, Base: "web", State: "stop", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo", Unseen: true},
	}

	folded := rowLines(renderedLines(projectsInput(rows, map[string]bool{"git:/repo": true}, 140)))
	if len(folded) != 1 {
		t.Fatalf("collapsed family rendered %d line(s), want 1:\n%s", len(folded), strings.Join(folded, "\n"))
	}
	if !strings.Contains(folded[0], UnreadNoticeGlyph) {
		t.Errorf("a folded family stopped reporting its unread member: %q", folded[0])
	}

	expanded := rowLines(renderedLines(projectsInput(rows, nil, 140)))
	summary := lineWith(t, expanded, "monorepo")
	if strings.Contains(summary, UnreadNoticeGlyph) {
		t.Errorf("an expanded summary duplicated a dot its own child already carries: %q", summary)
	}
	child := lineWith(t, expanded, "/repo/web")
	if !strings.Contains(child, UnreadNoticeGlyph) {
		t.Errorf("the unread child of an expanded family carries no dot: %q", child)
	}
	if quiet := lineWith(t, expanded, "/repo/api"); strings.Contains(quiet, UnreadNoticeGlyph) {
		t.Errorf("a working sibling was marked by its family's news: %q", quiet)
	}
}

// TestAFoldedMemberPulseIsNeitherDrawnNorReservedFor pins the half of family
// aggregation that does NOT roll up. A pulse belongs to a window, a summary has
// none, and synthesizing one would tell the operator a folded family just
// changed state without saying which member did. Because the reservation is
// taken from the projection rather than the rows, the gutter the pulse cannot be
// drawn in is not reserved either.
func TestAFoldedMemberPulseIsNeitherDrawnNorReservedFor(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "bolt", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{WindowID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	}
	in := projectsInput(rows, map[string]bool{"git:/repo": true}, 140)
	in.ActivePulse = pulsingWindows(2)

	if anyItemHasNotice(in) {
		t.Error("a pulse on a folded member reserved a gutter no line can draw in")
	}
	line := lineWith(t, rowLines(renderedLines(in)), "monorepo")
	if strings.Contains(line, GroupBorderGlyph) {
		t.Errorf("a folded family synthesized a pulse of its own: %q", line)
	}
}

// TestFilteredAndSingletonFamiliesMarkOnlyWhatTheyProject asserts the marker
// cannot come from outside the projection. The row set a projection is built
// from is already filtered, so a member the filter removed is not a member; and
// a family filtered down to one row is a leaf, which marks itself.
func TestFilteredAndSingletonFamiliesMarkOnlyWhatTheyProject(t *testing.T) {
	// The unread member of the family is filtered out: the two rows that reach
	// the projection have nothing to say, so no gutter is reserved at all.
	visible := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "bolt", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{WindowID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
	}
	filtered := projectsInput(visible, map[string]bool{"git:/repo": true}, 140)
	if anyItemHasNotice(filtered) {
		t.Error("a session outside the filtered projection reserved the gutter")
	}
	if out := stripAnsi(RenderList(filtered)); strings.Contains(out, UnreadNoticeGlyph) {
		t.Errorf("a session outside the filtered projection drew a dot:\n%s", out)
	}

	// One visible member is a leaf, not a summary, and carries its own dot even
	// though it names a family.
	singleton := []session.RunningSession{
		{WindowID: 2, Base: "web", State: "stop", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo", Unseen: true},
	}
	lines := rowLines(renderedLines(projectsInput(singleton, map[string]bool{"git:/repo": true}, 140)))
	if len(lines) != 1 {
		t.Fatalf("a one-member family rendered %d line(s), want 1:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], UnreadNoticeGlyph) {
		t.Errorf("the surviving member of a filtered family lost its dot: %q", lines[0])
	}
}

// TestUnreadNoticeIgnoresInvalidSummaryMembers mirrors the range checks GroupAt
// and SummaryCounts make. A summary and the row set it points into travel as
// separate fields, so an index that names nothing must contribute nothing
// rather than panic or guess.
func TestUnreadNoticeIgnoresInvalidSummaryMembers(t *testing.T) {
	l := ItemList{
		Rows: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "stop", Unseen: true},
		},
	}
	onlyInvalid := Item{Kind: ItemFamilySummary, Row: -1, Members: []int{-1, 7, 99}, Collapsed: true}
	if l.HasUnreadNotice(onlyInvalid) {
		t.Error("a summary whose every member index is out of range claimed unread news")
	}
	mixed := Item{Kind: ItemFamilySummary, Row: -1, Members: []int{-3, 0, 42}, Collapsed: true}
	if !l.HasUnreadNotice(mixed) {
		t.Error("a valid unread member was lost among out-of-range indexes")
	}
	// A leaf whose Row is out of range resolves to no session and so has no news.
	if l.HasUnreadNotice(Item{Kind: ItemSession, Row: 9}) {
		t.Error("a leaf pointing outside the row set claimed unread news")
	}
}

// TestNoticeGutterRendersDeterministically asserts the gutter adds no ordering
// of its own: the same projection renders byte-for-byte the same twice, in both
// arrangements and with both notices in play.
func TestNoticeGutterRendersDeterministically(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "api", State: "stop", Directory: "/repo/api", FamilyKey: "git:/repo", FamilyLabel: "monorepo", Unseen: true},
		{WindowID: 2, Base: "web", State: "bolt", Directory: "/repo/web", FamilyKey: "git:/repo", FamilyLabel: "monorepo"},
		{WindowID: 3, Base: "cli", State: "stop", Directory: "~/cli"},
	}
	urgency := noticeInput(rows, 120)
	urgency.ActivePulse = pulsingWindows(2)
	projects := projectsInput(rows, map[string]bool{"git:/repo": true}, 120)

	for name, in := range map[string]ListInput{"urgency": urgency, "projects": projects} {
		if first, second := RenderList(in), RenderList(in); first != second {
			t.Errorf("%s render is not deterministic:\nfirst:\n%s\nsecond:\n%s", name, first, second)
		}
	}
}

// TestNoticeGutterChangesNoBucketOrOrder is the scope guard. The pulse layer
// moves no row between buckets, so turning it on must leave the urgency
// headings, their counts, and the row order exactly as they were — the gutter
// is presentation, and the only difference in the pass is the two cells it
// prepends.
func TestNoticeGutterChangesNoBucketOrOrder(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "one", State: "key", Account: "work", Directory: "~/p"},
		{WindowID: 2, Base: "two", State: "bolt", Account: "work", Directory: "~/p"},
		{WindowID: 3, Base: "three", State: "stop", Account: "work", Directory: "~/p"},
	}
	quiet := renderedLines(noticeInput(rows, 120))
	marked := noticeInput(rows, 120)
	marked.ActivePulse = pulsingWindows(2)
	loud := renderedLines(marked)

	if len(quiet) != len(loud) {
		t.Fatalf("the gutter changed the line count: %d without, %d with\n%s\n---\n%s",
			len(quiet), len(loud), strings.Join(quiet, "\n"), strings.Join(loud, "\n"))
	}
	for i := range quiet {
		if got, want := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(loud[i], UnreadNoticeGlyph), GroupBorderGlyph)), strings.TrimSpace(quiet[i]); got != want {
			t.Errorf("line %d reads %q with the gutter and %q without", i, got, want)
		}
	}
	for _, want := range []string{"Needs Attention (1)", "Active (1)", "Idle (1)"} {
		if !strings.Contains(strings.Join(loud, "\n"), want) {
			t.Errorf("urgency heading %q is missing once the gutter is drawn:\n%s", want, strings.Join(loud, "\n"))
		}
	}
}
