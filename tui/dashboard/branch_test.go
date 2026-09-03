package dashboard

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestBranchTextRendersTheDirtyMarkerOnlyOnANamedBranch pins the one asymmetry
// in the two facts the daemon probes independently: they are probed
// independently (a detached HEAD still reports its dirty bit) but they are
// RENDERED together, because a bare `*` in the branch column would be a mark
// with nothing to mark.
func TestBranchTextRendersTheDirtyMarkerOnlyOnANamedBranch(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		dirty  bool
		want   string
	}{
		{"clean checkout", "main", false, "main"},
		{"dirty checkout", "main", true, "main*"},
		{"worktree branch", "amx/feature-name", false, "amx/feature-name"},
		{"no repository", "", false, ""},
		{"dirty but unnamed", "", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := branchText(session.RunningSession{Branch: tc.branch, Dirty: tc.dirty})
			if got != tc.want {
				t.Fatalf("branchText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBranchColumnWidthIsMeasuredOncePerViewAndDeclinesWhenEmpty pins both
// halves of the reservation rule: the width is the widest text in the WHOLE
// filtered view (not per row), and zero — the column not being reserved at all —
// is what a view with nothing to say returns. The zero return IS the
// reservation predicate, so there is no second boolean that could disagree.
func TestBranchColumnWidthIsMeasuredOncePerViewAndDeclinesWhenEmpty(t *testing.T) {
	none := []session.RunningSession{{Directory: "/tmp"}, {Directory: "/var"}}
	if got := branchColumnWidth(none); got != 0 {
		t.Fatalf("width with no branch in view = %d, want 0", got)
	}

	// A dirty bit with no branch is still nothing to render, so it must not
	// reserve the column for every other row either.
	if got := branchColumnWidth([]session.RunningSession{{Dirty: true}}); got != 0 {
		t.Fatalf("width for a dirty non-repository = %d, want 0", got)
	}

	mixed := []session.RunningSession{
		{Branch: "main"},
		{Branch: "amx/feat-x", Dirty: true}, // 10 + 1 marker = 11, the widest
		{},                                  // a non-repository row still gets the column
	}
	if got, want := branchColumnWidth(mixed), 12; got != want {
		t.Fatalf("width = %d, want %d (widest text + one trailing space)", got, want)
	}
}

// TestBranchColumnWidthCapsOneLongBranch is the reason branchMaxWidth exists.
// The width is charged to every row's directory budget, not just to the row that
// set it, so one release branch nobody is looking at must not be able to take
// forty cells off every other row in the view.
func TestBranchColumnWidthCapsOneLongBranch(t *testing.T) {
	rows := []session.RunningSession{
		{Branch: "main"},
		{Branch: "release/2026-q3-migration-rollback", Dirty: true},
	}
	if got, want := branchColumnWidth(rows), branchMaxWidth+1; got != want {
		t.Fatalf("width = %d, want %d (cap + one trailing space)", got, want)
	}
}

// TestBranchCellTruncatesFromTheLeftAndKeepsTheDirtyMarker is the whole reason
// the column uses layout.TruncateLeft rather than layout.Truncate: two worktrees
// of one repository share a prefix and differ in the tail, so cutting from the
// right would render both as `amx/featu…`. Truncating the branch and its marker
// as one string is what keeps the marker, since it is the last grapheme and
// TruncateLeft drops leading ones.
func TestBranchCellTruncatesFromTheLeftAndKeepsTheDirtyMarker(t *testing.T) {
	s := session.RunningSession{Branch: "amx/an-extremely-long-topic-branch", Dirty: true}
	cell := stripAnsi(renderBranchCell(s, branchMaxWidth+1))

	if !strings.HasSuffix(strings.TrimRight(cell, " "), "branch"+dirtyMarker) {
		t.Fatalf("cell %q should end with the branch tail and the dirty marker", cell)
	}
	if !strings.Contains(cell, "…") {
		t.Fatalf("cell %q should carry the truncation ellipsis", cell)
	}
	if strings.Contains(cell, "amx/an-extremely") {
		t.Fatalf("cell %q should have dropped the shared leading segment", cell)
	}
	if got, want := layout.Width(cell), branchMaxWidth+1; got != want {
		t.Fatalf("truncated cell width = %d, want %d", got, want)
	}
}

// TestBranchCellPadsRatherThanCollapsing pins the alignment property the whole
// once-per-pass measurement exists for: a non-repository row keeps its directory
// in the same column as the worktree row beside it, so a blank branch reads as a
// gap in one column rather than as a differently-shaped row. It also pins that a
// zero width renders nothing at all rather than a single space.
func TestBranchCellPadsRatherThanCollapsing(t *testing.T) {
	width := branchColumnWidth([]session.RunningSession{{Branch: "main"}, {}})

	named := renderBranchCell(session.RunningSession{Branch: "main"}, width)
	blank := renderBranchCell(session.RunningSession{}, width)

	if layout.StyledWidth(named) != width || layout.Width(blank) != width {
		t.Fatalf("cells disagree on width: named=%d blank=%d want %d",
			layout.StyledWidth(named), layout.Width(blank), width)
	}
	if strings.TrimSpace(blank) != "" {
		t.Fatalf("blank cell = %q, want only spaces", blank)
	}
	if got := renderBranchCell(session.RunningSession{Branch: "main"}, 0); got != "" {
		t.Fatalf("unreserved cell = %q, want empty", got)
	}
}

// TestBranchCellIsDimLikeTheDirectory pins the styling decision. The branch and
// the directory together answer one question — where is this row — so they are
// styled alike and neither competes with the state icon for attention.
func TestBranchCellIsDimLikeTheDirectory(t *testing.T) {
	cell := renderBranchCell(session.RunningSession{Branch: "main"}, 6)
	if cell == stripAnsi(cell) {
		t.Fatalf("branch cell %q carries no styling", cell)
	}
	if want := styleDim.Render("main"); !strings.Contains(cell, want) {
		t.Fatalf("branch cell %q is not rendered with styleDim", cell)
	}
}

// TestBranchColumnChargesTheDirectoryAndNotTheViewport is the width audit in
// test form. The branch column is width the FLEX column gives up, so reserving
// it must shrink the directory by exactly the cell's width and must leave the
// composed row's total width unchanged.
func TestBranchColumnChargesTheDirectoryAndNotTheViewport(t *testing.T) {
	const width = 100
	s := session.RunningSession{
		Account:   "work",
		Base:      "api",
		Directory: "/home/user/projects/worktrees/api-feature-x/internal/deep",
	}

	without := FormatRow(s, nil, nil, nil, width, "1: ", "", "", "", "")
	cell := renderBranchCell(session.RunningSession{Branch: "amx/feat-x"}, 11)
	with := FormatRow(s, nil, nil, nil, width, "1: ", "", "", "", cell)

	if layout.StyledWidth(without) != layout.StyledWidth(with) {
		t.Fatalf("row width changed with the branch column: %d without, %d with",
			layout.StyledWidth(without), layout.StyledWidth(with))
	}
	if !strings.Contains(stripAnsi(with), "amx/feat-x") {
		t.Fatalf("row %q lost the branch", stripAnsi(with))
	}
	if strings.Contains(stripAnsi(without), "amx/feat-x") {
		t.Fatalf("unreserved row %q gained a branch", stripAnsi(without))
	}

	// The directory is the only cell that could have paid for those 11 cells.
	// It is left-truncated onto an ellipsis in both rows and is the row's
	// suffix, so the text after the last ellipsis IS the directory.
	dirOf := func(row string) string {
		stripped := stripAnsi(row)
		i := strings.LastIndex(stripped, "…")
		if i < 0 {
			t.Fatalf("row %q was expected to have a truncated directory", stripped)
		}
		return stripped[i:]
	}
	shrank := layout.Width(dirOf(without)) - layout.Width(dirOf(with))
	if shrank != 11 {
		t.Fatalf("directory shrank by %d cells, want 11 (the whole branch cell)", shrank)
	}
}
