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

// TestBranchReachesTheSelectedSessionContextRatherThanTheRow is where the
// retired branch column went. The queue row must not name a branch at any width
// — that was the column's whole cost, charged to every row in the view for a fact
// about one — and the inspector must name it for the selection.
func TestBranchReachesTheSelectedSessionContextRatherThanTheRow(t *testing.T) {
	s := session.RunningSession{
		WindowID: 1, Base: "api", Project: "api",
		Directory: "/home/user/projects/worktrees/api-feature-x",
		Branch:    "amx/feat-x", Dirty: true, State: "bolt",
	}
	items := BuildItemsFor(ViewUrgency, []session.RunningSession{s}, nil)

	queue := BuildQueue(QueueInput{Rows: items.Rows})
	for _, width := range rowTestWidths {
		row := stripAnsi(FormatRow(RowInput{Queue: queue[0], Width: width, Columns: ScanQueueColumns(queue)}))
		if strings.Contains(row, "amx/feat-x") {
			t.Errorf("width %d: the queue row reserved a branch column: %q", width, row)
		}
	}

	lines := renderInspectorLines(t, InspectorInput{Width: 78, Content: InspectorReady, Session: s})
	if got := lines[3]; !strings.Contains(got, "amx/feat-x"+dirtyMarker) {
		t.Errorf("inspector branch line %q is missing the branch and its dirty marker", got)
	}
}

// TestBranchIsUncoloredAlongsideTheDirectory pins the styling decision that
// survived the column. The branch and the directory together answer one question
// — where is this session — so they sit in the same labelled block wearing the
// same dim label and no color of their own, leaving the pane's only color on the
// headline two lines above.
func TestBranchIsUncoloredAlongsideTheDirectory(t *testing.T) {
	s := session.RunningSession{WindowID: 1, Base: "api", Project: "api",
		Directory: "/tmp/api", Branch: "main"}
	out := RenderInspector(inspectorInput(InspectorInput{Width: 78, Content: InspectorReady, Session: s}))
	lines := strings.Split(out, "\n")

	for _, tc := range []struct {
		idx   int
		label string
		value string
	}{
		{3, "Branch", "main"},
		{2, "Dir", "/tmp/api"},
	} {
		line := lines[tc.idx]
		if want := styleDim.Render(layout.PadRight(tc.label, inspectorLabelWidth)); !strings.Contains(line, want) {
			t.Errorf("%s line %q does not wear the dim label column", tc.label, line)
		}
		// The value is written through with no styling of its own: what tells the
		// two lines apart is what they say, not how they are painted.
		if !strings.Contains(line, tc.value) {
			t.Fatalf("%s line %q is missing %q", tc.label, line, tc.value)
		}
		if styled := strings.Index(line, tc.value); styled >= 0 && strings.Contains(line[styled:], "\x1b[") {
			t.Errorf("%s line %q colors its value", tc.label, line)
		}
	}
}

// TestBranchLongEnoughToHaveNeededACapIsStillDrawnWhole is the negative of the
// retired branchMaxWidth. The cap existed because the column's width was charged
// to every row; nothing is charged now, so a release branch is shown in full and
// only the line's own clamp can shorten it.
func TestBranchLongEnoughToHaveNeededACapIsStillDrawnWhole(t *testing.T) {
	s := session.RunningSession{WindowID: 1, Base: "api", Project: "api",
		Branch: "release/2026-q3-migration-rollback"}
	lines := renderInspectorLines(t, InspectorInput{Width: 120, Content: InspectorReady, Session: s})
	if line := lines[2]; !strings.Contains(line, s.Branch) {
		t.Fatalf("inspector branch line %q truncated a branch it had room for", line)
	}
}
