package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

type recordingWorkspaceMarker struct {
	workspaceID string
	through     time.Time
	oneCalls    int
	allCalls    int
}

func (m *recordingWorkspaceMarker) MarkWorkspaceRead(_ context.Context, workspaceID, _ string, through time.Time) (bool, error) {
	m.workspaceID, m.through = workspaceID, through
	m.oneCalls++
	return true, nil
}

func (m *recordingWorkspaceMarker) MarkAllWorkspacesRead(context.Context, string) (int64, error) {
	m.allCalls++
	return 2, nil
}

func workspaceHomeFixtureDriver(t *testing.T, size UITestSize) (*UITestDriver, *UITestKittyClient) {
	t.Helper()
	fx, err := UITestFixtureByName("workspace_attention")
	if err != nil {
		t.Fatalf("workspace fixture: %v", err)
	}
	return NewUITestFixtureDriver(fx, size)
}

func TestRunConstructorStartsAtWorkspaceHome(t *testing.T) {
	fx, err := UITestFixtureByName("workspace_attention")
	if err != nil {
		t.Fatal(err)
	}
	m := NewModelFromWindow(fx.Config, NewUITestKittyClient(fx.Windows), 0)
	if m.mode != ModeWorkspace {
		t.Fatalf("Run model mode = %v, want ModeWorkspace", m.mode)
	}
}

func TestWorkspaceHomeSplitRendersSectionedDetail(t *testing.T) {
	d, _ := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	plain := stripAnsi(d.Model().render())
	for _, want := range []string{"Workspaces", "Attention", "Live sessions", "Recent conversations", "Plans", "Activity (2 unread)", "Permission requested"} {
		if !strings.Contains(plain, want) {
			t.Errorf("workspace home missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "│") {
		t.Errorf("wide workspace home did not render split panes:\n%s", plain)
	}
}

func TestOpeningWorkspaceMarksOnlyLoadedActivityAndKeepsLiveAttention(t *testing.T) {
	d, _ := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	marker := &recordingWorkspaceMarker{}
	d.model.workspaceActivityMarker = marker
	loadedThrough := d.model.workspaceSnapshot.Workspaces[0].ActivityLoadedThrough
	if err := d.Key("tab"); err != nil {
		t.Fatal(err)
	}
	item := d.Model().workspaceSnapshot.Workspaces[0]
	if marker.oneCalls != 1 || marker.workspaceID != "api" || !marker.through.Equal(loadedThrough) {
		t.Fatalf("marker = %+v, want api through %v", marker, loadedThrough)
	}
	if item.UnreadCount != 0 || item.Urgency != workspace.UrgencyAttention {
		t.Fatalf("workspace after open = unread %d urgency %v", item.UnreadCount, item.Urgency)
	}
	if err := d.Key("u"); err != nil {
		t.Fatal(err)
	}
	if marker.oneCalls != 1 {
		t.Fatalf("u unexpectedly marked read: %+v", marker)
	}
}

func TestWorkspaceActivityEnterFocusesRelatedLiveSession(t *testing.T) {
	d, client := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	if err := d.Key("tab"); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := d.Key("j"); err != nil {
			t.Fatal(err)
		}
	}
	selected, ok := d.Model().selectedWorkspaceDetail()
	if !ok || selected.kind != workspaceDetailActivity || selected.activity.ID != 2 {
		t.Fatalf("selected activity = %+v", selected)
	}
	if err := d.Key("enter"); err != nil {
		t.Fatal(err)
	}
	if got := len(client.CallsFor("FocusTab")); got != 1 {
		t.Fatalf("activity Enter focus calls = %d, want 1", got)
	}
}

func TestWorkspaceProjectionPreservesWorkspaceAndActivitySelectionAcrossReorder(t *testing.T) {
	old := workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{
		{ID: "a", DisplayName: "A"},
		{ID: "b", DisplayName: "B", Activity: []workspace.ActivityItem{{ID: 2, Summary: "newer"}, {ID: 1, Summary: "selected"}}},
	}}
	m := Model{workspaceProjectionSeq: 1, workspaceSnapshot: old, workspaceCursor: 1, workspaceFocus: workspaceFocusDetail}
	for i, item := range workspaceItems(old.Workspaces[1]) {
		if workspaceDetailID(item) == "activity:1" {
			m.workspaceDetailCursor = i
		}
	}
	newSnapshot := workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{
		{ID: "b", DisplayName: "B", Activity: []workspace.ActivityItem{{ID: 3, Summary: "arrived"}, {ID: 2, Summary: "newer"}, {ID: 1, Summary: "selected"}}},
		{ID: "a", DisplayName: "A"},
	}}
	updated, _ := m.Update(workspaceProjectionMsg{seq: 1, snapshot: newSnapshot})
	got := updated.(Model)
	selectedWorkspace, _ := got.selectedWorkspace()
	selectedDetail, _ := got.selectedWorkspaceDetail()
	if selectedWorkspace.ID != "b" || selectedDetail.kind != workspaceDetailActivity || selectedDetail.activity.ID != 1 {
		t.Fatalf("selection after reorder = workspace %q detail %+v", selectedWorkspace.ID, selectedDetail)
	}
}

func TestPaletteWorkspaceReadActionsKeepUndoKeyIndependent(t *testing.T) {
	d, _ := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	marker := &recordingWorkspaceMarker{}
	d.model.workspaceActivityMarker = marker
	m := d.Model().enterPalette()
	entries := paletteEntries(m)
	var one, all *paletteEntry
	for i := range entries {
		switch entries[i].actionID {
		case actions.MarkRead:
			one = &entries[i]
		case actions.MarkAllRead:
			all = &entries[i]
		}
	}
	if one == nil || all == nil || !one.availability.Available || !all.availability.Available {
		t.Fatalf("read actions = one %+v all %+v", one, all)
	}
	updated, cmd := m.runPaletteEntry(*one)
	if cmd == nil {
		t.Fatal("mark workspace read returned no command")
	}
	msg := cmd()
	if done, ok := msg.(actionDoneMsg); !ok || done.err != nil {
		t.Fatalf("mark workspace result = %#v", msg)
	}
	if marker.oneCalls != 1 || updated.(Model).workspaceSnapshot.Workspaces[0].UnreadCount != 0 {
		t.Fatalf("mark workspace state marker=%+v model=%+v", marker, updated.(Model).workspaceSnapshot.Workspaces[0])
	}
}

func TestWorkspaceHomeNarrowEnterDrillsIntoSameItems(t *testing.T) {
	d, _ := workspaceHomeFixtureDriver(t, UITestSize{Width: 80, Height: 22})
	if err := d.Key("enter"); err != nil {
		t.Fatal(err)
	}
	m := d.Model()
	if !m.workspaceDrillDown || m.workspaceFocus != workspaceFocusDetail {
		t.Fatalf("narrow Enter = drill %v focus %v, want detail drill-down", m.workspaceDrillDown, m.workspaceFocus)
	}
	plain := stripAnsi(m.render())
	for _, want := range []string{"Attention", "worker", "server", "Recent conversations", "Plans", "Activity"} {
		if !strings.Contains(plain, want) {
			t.Errorf("narrow drill-down missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "│") {
		t.Errorf("narrow drill-down retained a split divider:\n%s", plain)
	}
}

func TestWorkspaceTabAndShiftTabSwitchPanes(t *testing.T) {
	d, _ := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	if err := d.Key("tab"); err != nil {
		t.Fatal(err)
	}
	if d.Model().workspaceFocus != workspaceFocusDetail {
		t.Fatalf("tab focus = %v, want detail", d.Model().workspaceFocus)
	}
	if err := d.Key("shift+tab"); err != nil {
		t.Fatal(err)
	}
	if d.Model().workspaceFocus != workspaceFocusProjects {
		t.Fatalf("shift+tab focus = %v, want projects", d.Model().workspaceFocus)
	}
}

func TestWorkspaceLiveItemEnterFocusesAndVReturnsToLegacyList(t *testing.T) {
	d, client := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	if err := d.Key("tab"); err != nil {
		t.Fatal(err)
	}
	if err := d.Key("enter"); err != nil {
		t.Fatal(err)
	}
	if got := len(client.CallsFor("FocusTab")); got != 1 {
		t.Fatalf("workspace live Enter recorded %d FocusTab calls, want 1", got)
	}
	if err := d.Key("v"); err != nil {
		t.Fatal(err)
	}
	if d.Model().mode != ModeList {
		t.Fatalf("v from workspace mode = %v, want ModeList", d.Model().mode)
	}
	if err := d.Key("v"); err != nil {
		t.Fatal(err)
	}
	if d.Model().mode != ModeWorkspace {
		t.Fatalf("v from legacy list mode = %v, want ModeWorkspace", d.Model().mode)
	}
}

func TestWorkspacePlanEnterOpensViewer(t *testing.T) {
	d, _ := workspaceHomeFixtureDriver(t, UITestSize{Width: 120, Height: 30})
	if err := d.Key("tab"); err != nil {
		t.Fatal(err)
	}
	// Detail navigation skips section headers: two more live rows, one recent
	// conversation, then the linked plan.
	for range 4 {
		if err := d.Key("j"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Key("enter"); err != nil {
		t.Fatal(err)
	}
	if d.Model().mode != ModePlanView {
		t.Fatalf("workspace plan Enter mode = %v, want ModePlanView", d.Model().mode)
	}
	if d.Model().planViewReturnMode != ModeWorkspace {
		t.Fatalf("plan return mode = %v, want ModeWorkspace", d.Model().planViewReturnMode)
	}
}

func TestWorkspaceProjectionKeepsConversationMetadataForSmartContinue(t *testing.T) {
	// The projection carries a display-only conversation. Its source metadata
	// must remain available separately so Enter can run the existing smart
	// continue action instead of attempting to reconstruct a transcript path.
	m := Model{workspaceProjectionSeq: 1}
	meta := transcript.SessionMeta{
		ID: "conversation-1", Harness: "claude", Profile: "work", Profiles: []string{"work"},
		Path: "/tmp/conversation.jsonl", LastAt: time.Now(),
	}
	updated, _ := m.Update(workspaceProjectionMsg{
		seq:           1,
		conversations: map[string]transcript.SessionMeta{transcript.IndexKey(meta.Harness, meta.ID): meta},
	})
	got := updated.(Model).workspaceConversationMeta[transcript.IndexKey(meta.Harness, meta.ID)]
	if got.Path != meta.Path || len(got.Profiles) != 1 || got.Profiles[0] != "work" {
		t.Fatalf("stored conversation metadata = %+v, want complete copy of %+v", got, meta)
	}
}
