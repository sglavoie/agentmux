package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/cmdtest"
)

type fakeLaunchDispatchClient struct {
	windows  []kitty.OSWindow
	launched []kitty.LaunchOpts
	closed   []string
	focused  []string
}

func (f *fakeLaunchDispatchClient) ListWindows() ([]kitty.OSWindow, error) { return f.windows, nil }
func (f *fakeLaunchDispatchClient) LaunchTab(opts kitty.LaunchOpts) error {
	f.launched = append(f.launched, opts)
	return nil
}
func (f *fakeLaunchDispatchClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (f *fakeLaunchDispatchClient) CloseTab(match string) error {
	f.closed = append(f.closed, match)
	if idText, ok := strings.CutPrefix(match, "id:"); ok {
		for wi := range f.windows {
			tabs := f.windows[wi].Tabs
			for ti := range tabs {
				if idText == strconv.Itoa(tabs[ti].ID) {
					f.windows[wi].Tabs = append(tabs[:ti], tabs[ti+1:]...)
					return nil
				}
			}
		}
	}
	return nil
}
func (f *fakeLaunchDispatchClient) MoveTab(string, int) error { return nil }
func (f *fakeLaunchDispatchClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (f *fakeLaunchDispatchClient) SetTabTitle(string, string) error            { return nil }
func (f *fakeLaunchDispatchClient) SetUserVars(string, map[string]string) error { return nil }
func (f *fakeLaunchDispatchClient) FocusTab(match string) error {
	f.focused = append(f.focused, match)
	return nil
}
func (f *fakeLaunchDispatchClient) FocusWindow(string) error               { return nil }
func (f *fakeLaunchDispatchClient) GetText(string, string) (string, error) { return "", nil }
func (f *fakeLaunchDispatchClient) LaunchOverlayPager(string) error        { return nil }

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

func selectByTitleAndProfile(m *Model, title, profile string) {
	if m.pickerSelected == nil {
		m.pickerSelected = make(map[int]bool)
	}
	for i, s := range m.pickerSessions {
		if s.Title == title && s.EffectiveProfile() == profile {
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

func TestDispatchPickerLaunch_RunningRowBypassesLaunchDedup(t *testing.T) {
	t.Parallel()

	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:       10,
				Title:    "api",
				Windows:  []kitty.Window{{ID: 11, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Project: "api"}}},
				IsActive: true,
			}},
		}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
		},
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "api", Account: "work", Profile: "work", Project: "api", Key: session.SessionKey("work", "api")},
	})
	m = pressKey(m, "l")
	selectByTitle(&m, "api")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd for selected running row")
	}
	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 {
		t.Fatalf("expected 1 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected LaunchTab to be called once for running row, got %d", len(client.launched))
	}
}

func TestDispatchPickerLaunch_SameProjectDifferentProfileLaunches(t *testing.T) {
	t.Parallel()

	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:       10,
				Title:    "sis",
				Windows:  []kitty.Window{{ID: 11, IsActive: true, CWD: "/home/user/sis", UserVars: kitty.UserVars{Profile: "vta", Harness: "claude", Project: "sis", AMXDirectory: "/home/user/sis"}}},
				IsActive: true,
			}},
		}},
	}
	projects := []config.Session{
		{Title: "sis", Directory: "/home/user/sis", Profile: "vta"},
		{Title: "sis", Directory: "/home/user/sis", Profile: "vta-codex"},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "vta", Harness: "claude", Color: "blue"},
			{Name: "vta-codex", Harness: "codex", Color: "green"},
		},
		Projects: projects,
		Sessions: projects,
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "sis", Profile: "vta", Account: "vta", Harness: "claude", Project: "sis", Directory: "/home/user/sis", Key: session.SessionKey("vta", "sis")},
	})
	m = pressKey(m, "l")
	selectByTitleAndProfile(&m, "sis", "vta-codex")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd for selected vta-codex row while vta row is running")
	}
	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 {
		t.Fatalf("expected 1 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	if got[0].err != nil {
		t.Fatalf("expected launch to succeed, got %v", got[0].err)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected LaunchTab to be called once for vta-codex/sis, got %d", len(client.launched))
	}
	if got := client.launched[0].UserVars["amx_profile"]; got != "vta-codex" {
		t.Errorf("expected amx_profile=vta-codex, got %q", got)
	}
	if got := client.launched[0].UserVars["amx_harness"]; got != "codex" {
		t.Errorf("expected amx_harness=codex, got %q", got)
	}
	if got := client.launched[0].UserVars["amx_project"]; got != "sis" {
		t.Errorf("expected amx_project=sis, got %q", got)
	}
}
