package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
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
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i).Interface()
		inner, ok := elem.(tea.Cmd)
		if !ok || inner == nil {
			continue
		}
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
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
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
// for launch. Regression guard for the dedup snapshot semantics.
func TestDispatchPickerLaunch_BothQueuedWhenNoneRunning(t *testing.T) {
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

// TestDispatchPickerLaunch_AllDedupedReturnsNil verifies that when every
// selected session is already in pickerRunning, dispatch returns nil and
// does not bump launchBatchID. Regression guard against accidentally
// emitting an empty progress toast.
func TestDispatchPickerLaunch_AllDedupedReturnsNil(t *testing.T) {
	running := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work"},
		{TabID: 2, Base: "backend", Account: "work"},
	}
	m := launchDispatchModel(t, running)
	selectByTitle(&m, "api", "backend")

	beforeID := m.launchBatchID
	cmd := m.dispatchPickerLaunch()
	if cmd != nil {
		t.Fatal("expected nil cmd when every selected session is already running")
	}
	if m.launchBatchID != beforeID {
		t.Fatalf("expected launchBatchID unchanged, got %d → %d", beforeID, m.launchBatchID)
	}
}

// TestDispatchPickerLaunch_OnlyNonRunningIsQueued verifies the mixed case:
// one of two selected sessions is in pickerRunning, so only the other is
// queued. Regression guard for the dedup branch in dispatchPickerLaunch.
func TestDispatchPickerLaunch_OnlyNonRunningIsQueued(t *testing.T) {
	running := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work"},
	}
	m := launchDispatchModel(t, running)
	selectByTitle(&m, "api", "backend")

	beforeID := m.launchBatchID
	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd: backend should launch even though api is deduped")
	}
	if m.launchBatchID != beforeID+1 {
		t.Fatalf("expected launchBatchID to increment by 1, got %d → %d", beforeID, m.launchBatchID)
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 {
		t.Fatalf("expected 1 launchProgressMsg (backend only), got %d (%+v)", len(got), got)
	}
	if got[0].title != "backend" {
		t.Errorf("expected queued title backend, got %q", got[0].title)
	}
	if got[0].total != 1 {
		t.Errorf("expected total=1 in launchProgressMsg, got %d", got[0].total)
	}
}

// TestDispatchPickerLaunch_RunningSnapshotIsStable verifies that the dedup
// decision is finalized synchronously at dispatch time. Mutating
// m.pickerRunning after dispatch does NOT affect which sessions launch —
// the closures don't re-poll the snapshot. This pins the "no race" claim
// from session 5: A's tab appearing in m.sessions before B's closure runs
// can never cause B to be skipped, because dispatch doesn't consult
// m.sessions or re-check m.pickerRunning inside the per-launch closures.
func TestDispatchPickerLaunch_RunningSnapshotIsStable(t *testing.T) {
	m := launchDispatchModel(t, nil)
	selectByTitle(&m, "api", "backend")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	// Simulate the "race" scenario described in the original session plan:
	// A's tab appears in m.sessions (and therefore in any rebuilt
	// pickerRunning snapshot) before B's closure fires. Mutate both maps
	// to seed both sessions as "running" and verify the already-dispatched
	// closures still emit both launchProgressMsg values.
	m.pickerRunning[session.SessionKey("work", "api")] = true
	m.pickerRunning[session.SessionKey("work", "backend")] = true
	m.sessions = append(m.sessions, session.RunningSession{
		TabID: 99, Base: "api", Account: "work",
	})

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg even after pickerRunning mutation, got %d", len(got))
	}
}
