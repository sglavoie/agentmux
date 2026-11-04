package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/cmdtest"
)

// collectLeafMsgs invokes cmd and recursively expands any tea.Sequence /
// tea.Batch slice results into the leaf tea.Msg values they produce. Used
// to count the per-session launchProgressMsg outputs of dispatchPickerLaunch
// without depending on the unexported sequenceMsg/BatchMsg type names.
func collectLeafMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return flattenMsg(t, cmd())
}

func flattenMsg(t *testing.T, msg tea.Msg) []tea.Msg {
	t.Helper()
	// Reuse the scenario harness's slice detection (tui/cmdtest) so the
	// test helper and the driver agree on what counts as a batch/sequence
	// message. Leaf messages fall through unchanged; batch components are
	// executed and flattened recursively.
	cmds, ok := cmdtest.Flatten(msg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, inner := range cmds {
		out = append(out, flattenMsg(t, inner())...)
	}
	return out
}

func countLaunchProgress(msgs []tea.Msg) []launchProgressMsg {
	var out []launchProgressMsg
	for _, m := range msgs {
		if lp, ok := m.(launchProgressMsg); ok {
			out = append(out, lp)
		}
	}
	return out
}

func launchDispatchModel(t *testing.T, running []session.RunningSession) Model {
	t.Helper()
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
		},
	}
	m := NewModel(cfg, nil)
	m = loadSessions(m, running)
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	return m
}

// selectByTitle marks every picker row whose Title is in titles. Used by the
// dispatch tests so they don't have to count picker rows in sorted order.
func selectByTitle(m *Model, titles ...string) {
	wanted := make(map[string]bool, len(titles))
	for _, t := range titles {
		wanted[t] = true
	}
	if m.pickerSelected == nil {
		m.pickerSelected = make(map[int]bool)
	}
	for i, s := range m.pickerSessions {
		if wanted[s.Title] {
			m.pickerSelected[i] = true
		}
	}
}

// TestDispatchPickerLaunch_BothQueuedWhenNoneRunning verifies that selecting
// two sessions and dispatching while neither is in pickerRunning queues both
// for launch.
func TestDispatchPickerLaunch_BothQueuedWhenNoneRunning(t *testing.T) {
	t.Parallel()

	m := launchDispatchModel(t, nil)
	selectByTitle(&m, "api", "backend")

	beforeID := m.launchBatchID
	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when both sessions are launchable")
	}
	if m.launchBatchID != beforeID+1 {
		t.Fatalf("expected launchBatchID to increment by 1, got %d → %d", beforeID, m.launchBatchID)
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	for _, lp := range got {
		if lp.total != 2 {
			t.Errorf("expected launchProgressMsg.total=2, got %d", lp.total)
		}
		if lp.batchID != m.launchBatchID {
			t.Errorf("expected batchID=%d, got %d", m.launchBatchID, lp.batchID)
		}
	}
	titles := map[string]bool{}
	for _, lp := range got {
		titles[lp.title] = true
	}
	if !titles["api"] || !titles["backend"] {
		t.Errorf("expected api and backend titles, got %v", titles)
	}
}

// TestDispatchPickerLaunch_RunningRowsStillQueued verifies that running rows
// stay launchable from the picker.
func TestDispatchPickerLaunch_RunningRowsStillQueued(t *testing.T) {
	t.Parallel()

	running := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work"},
		{TabID: 2, Base: "backend", Account: "work"},
	}
	m := launchDispatchModel(t, running)
	selectByTitle(&m, "api", "backend")

	beforeID := m.launchBatchID
	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when every selected session is already running")
	}
	if m.launchBatchID != beforeID+1 {
		t.Fatalf("expected launchBatchID to increment by 1, got %d → %d", beforeID, m.launchBatchID)
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg, got %d (%+v)", len(got), got)
	}
}

// TestDispatchPickerLaunch_RunningAndStoppedRowsQueued verifies the mixed case:
// running and non-running rows are both launch requests.
func TestDispatchPickerLaunch_RunningAndStoppedRowsQueued(t *testing.T) {
	t.Parallel()

	running := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work"},
	}
	m := launchDispatchModel(t, running)
	selectByTitle(&m, "api", "backend")

	beforeID := m.launchBatchID
	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if m.launchBatchID != beforeID+1 {
		t.Fatalf("expected launchBatchID to increment by 1, got %d → %d", beforeID, m.launchBatchID)
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	titles := map[string]bool{}
	for _, lp := range got {
		titles[lp.title] = true
	}
	if !titles["api"] || !titles["backend"] {
		t.Errorf("expected api and backend titles, got %v", titles)
	}
	for _, lp := range got {
		if lp.total != 2 {
			t.Errorf("expected total=2 in launchProgressMsg, got %d", lp.total)
		}
	}
}
