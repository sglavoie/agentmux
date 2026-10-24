package asl

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// TestDetailPanelShowsReasonAtEveryWidth pins that why a run is listed stays
// on screen when the wide row spends its last column on the workspace.
func TestDetailPanelShowsReasonAtEveryWidth(t *testing.T) {
	row := aslbridge.Row{
		RowID:         "r1",
		Label:         "demo",
		PrimaryState:  "paused-for-reply",
		UrgencyGroup:  "needs-action",
		PauseReason:   "supervisor question open",
		WorkspaceRoot: "/work/demo",
	}
	for _, width := range []int{80, 140} {
		in := RunsInput{Rows: []aslbridge.Row{row}, Width: width, Loaded: true, Status: aslbridge.Status{Available: true}}
		panel := strings.Join(in.detailPanel(), "\n")
		if !strings.Contains(panel, "reason: ") || !strings.Contains(panel, "supervisor question open") {
			t.Errorf("width %d: detail panel lacks the reason:\n%s", width, panel)
		}
	}
}
