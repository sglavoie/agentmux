package dashboard

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
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

// TestBranchReachesTheSelectedSessionContextRatherThanTheRow is where the
// retired branch column went. The queue row must not name a branch at any width
// — that was the column's whole cost, charged to every row in the view for a fact
// about one — and the strip under the list must name it for the selection.
func TestBranchReachesTheSelectedSessionContextRatherThanTheRow(t *testing.T) {
	s := session.RunningSession{
		WindowID: 1, Base: "api", Project: "api",
		Directory: "/home/user/projects/worktrees/api-feature-x",
		Branch:    "amx/feat-x", Dirty: true, State: "bolt",
	}
	items := BuildItemsFor(ViewUrgency, []session.RunningSession{s}, nil)
	item, _ := items.At(0)

	queue := BuildQueue(QueueInput{Rows: items.Rows})
	for _, width := range rowTestWidths {
		row := stripAnsi(FormatRow(RowInput{Queue: queue[0], Width: width, Columns: ScanQueueColumns(queue)}))
		if strings.Contains(row, "amx/feat-x") {
			t.Errorf("width %d: the queue row reserved a branch column: %q", width, row)
		}
	}

	lines := RenderDetailStrip(DetailStripInput{Width: 78, Items: items, Item: item, Selected: true})
	if got := stripAnsi(lines[1]); !strings.Contains(got, "amx/feat-x"+dirtyMarker) {
		t.Errorf("strip line 2 %q is missing the branch and its dirty marker", got)
	}
}

// TestBranchIsDimAlongsideTheDirectory pins the styling decision that survived
// the column. The branch and the directory together answer one question — where
// is this session — so they are styled alike and neither competes with the status
// beside the name on the line above.
func TestBranchIsDimAlongsideTheDirectory(t *testing.T) {
	s := session.RunningSession{WindowID: 1, Base: "api", Project: "api",
		Directory: "/tmp/api", Branch: "main"}
	items := BuildItemsFor(ViewUrgency, []session.RunningSession{s}, nil)
	item, _ := items.At(0)
	line := RenderDetailStrip(DetailStripInput{Width: 78, Items: items, Item: item, Selected: true})[1]
	if line == stripAnsi(line) {
		t.Fatalf("strip line 2 %q carries no styling", line)
	}
	if want := styleDim.Render("main"); !strings.Contains(line, want) {
		t.Fatalf("strip line 2 %q does not render the branch with styleDim", line)
	}
}

// TestBranchLongEnoughToHaveNeededACapIsStillDrawnWhole is the negative of the
// retired branchMaxWidth. The cap existed because the column's width was charged
// to every row; nothing is charged now, so a release branch is shown in full and
// only the line's own clamp can shorten it.
func TestBranchLongEnoughToHaveNeededACapIsStillDrawnWhole(t *testing.T) {
	s := session.RunningSession{WindowID: 1, Base: "api", Project: "api",
		Branch: "release/2026-q3-migration-rollback"}
	items := BuildItemsFor(ViewUrgency, []session.RunningSession{s}, nil)
	item, _ := items.At(0)
	line := stripAnsi(RenderDetailStrip(DetailStripInput{Width: 120, Items: items, Item: item, Selected: true})[1])
	if !strings.Contains(line, s.Branch) {
		t.Fatalf("strip line 2 %q truncated a branch it had room for", line)
	}
}
