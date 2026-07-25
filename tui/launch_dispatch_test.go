package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
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
	// Reuse the production harness's slice detection (uitest_driver.go) so the
	// test helper and the driver agree on what counts as a batch/sequence
	// message. Leaf messages fall through unchanged; batch components are
	// executed and flattened recursively.
	cmds, ok := uitestFlattenCmds(msg)
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

func TestDispatchPickerLaunch_SisOverrideCreatesNewCodexInstances(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "overrides.json"))

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
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "vta", Harness: "claude"},
			{Name: "vta-codex", Harness: "codex"},
		},
		Sessions: []config.Session{
			{Title: "sis", Directory: "/home/user/sis", Profile: "vta"},
		},
	}
	cfg.Projects = cfg.Sessions

	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "sis", Profile: "vta", Account: "vta", Harness: "claude", Project: "sis", Directory: "/home/user/sis", Key: session.SessionKey("vta", "sis")},
	})
	m = pressKey(m, "l")
	if got := m.pickerRunning[session.SessionKey("vta", "sis")]; got != 1 {
		t.Fatalf("expected picker to show one running Claude sis, got %d", got)
	}

	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C, got %d", m.mode)
	}
	if m.configPickerPurpose != ConfigPickerLauncherOverride {
		t.Fatalf("expected launcher-override purpose after C, got %d", m.configPickerPurpose)
	}
	m = pressKey(m, "j")
	m = pressKey(m, "enter")
	if got := m.pickerEffectiveProfile(0); got != "vta-codex" {
		t.Fatalf("expected picker override profile vta-codex, got %q", got)
	}

	selectByTitle(&m, "sis")
	first := m.dispatchPickerLaunch()
	if first == nil {
		t.Fatal("expected non-nil first launch cmd")
	}
	gotFirst := countLaunchProgress(collectLeafMsgs(t, first))
	if len(gotFirst) != 1 || gotFirst[0].err != nil {
		t.Fatalf("expected first vta-codex launch to succeed, got %+v", gotFirst)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected one launched tab after first dispatch, got %d", len(client.launched))
	}
	if got := client.launched[0].UserVars["amx_profile"]; got != "vta-codex" {
		t.Fatalf("expected first launch amx_profile=vta-codex, got %q", got)
	}
	if got := client.launched[0].UserVars["amx_harness"]; got != "codex" {
		t.Fatalf("expected first launch amx_harness=codex, got %q", got)
	}

	client.windows = []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			{
				ID:       10,
				Title:    "sis",
				Windows:  []kitty.Window{{ID: 11, IsActive: true, CWD: "/home/user/sis", UserVars: kitty.UserVars{Profile: "vta", Harness: "claude", Project: "sis", AMXDirectory: "/home/user/sis"}}},
				IsActive: true,
			},
			{
				ID:       20,
				Title:    "sis",
				Windows:  []kitty.Window{{ID: 21, IsActive: true, CWD: "/home/user/sis", UserVars: kitty.UserVars{Profile: "vta-codex", Harness: "codex", Project: "sis", AMXDirectory: "/home/user/sis"}}},
				IsActive: true,
			},
		},
	}}
	m = NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "sis", Profile: "vta", Account: "vta", Harness: "claude", Project: "sis", Directory: "/home/user/sis", Key: session.SessionKey("vta", "sis")},
		{TabID: 20, WindowID: 21, Base: "sis", Profile: "vta-codex", Account: "vta-codex", Harness: "codex", Project: "sis", Directory: "/home/user/sis", Key: session.SessionKey("vta-codex", "sis")},
	})
	m = pressKey(m, "l")
	if got := m.pickerEffectiveProfile(0); got != "vta-codex" {
		t.Fatalf("expected persisted picker override vta-codex, got %q", got)
	}
	if got := m.pickerRunning[session.SessionKey("vta-codex", "sis")]; got != 1 {
		t.Fatalf("expected picker to show one running Codex sis before second launch, got %d", got)
	}

	selectByTitle(&m, "sis")
	second := m.dispatchPickerLaunch()
	if second == nil {
		t.Fatal("expected non-nil second launch cmd")
	}
	gotSecond := countLaunchProgress(collectLeafMsgs(t, second))
	if len(gotSecond) != 1 || gotSecond[0].err != nil {
		t.Fatalf("expected second vta-codex launch to succeed, got %+v", gotSecond)
	}
	if len(client.launched) != 2 {
		t.Fatalf("expected second explicit picker launch to create another tab, got %d launches", len(client.launched))
	}
	if got := client.launched[1].UserVars["amx_profile"]; got != "vta-codex" {
		t.Fatalf("expected second launch amx_profile=vta-codex, got %q", got)
	}
}

func TestDashboardLaunch_SameProfileSelectionStillLaunches(t *testing.T) {
	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:       10,
				Title:    "api",
				Windows:  []kitty.Window{{ID: 11, IsActive: true, CWD: "/home/user/api", UserVars: kitty.UserVars{Profile: "work", Harness: "claude", Project: "api", AMXDirectory: "/home/user/api"}}},
				IsActive: true,
			}},
		}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", Color: "blue"},
			{Name: "personal", Harness: "codex", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Profile: "work"},
		},
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "api", Profile: "work", Account: "work", Harness: "claude", Project: "api", Directory: "/home/user/api", Key: session.SessionKey("work", "api")},
	})

	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after dashboard C, got %d", m.mode)
	}
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after confirming dashboard launch, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd for same-profile dashboard launch")
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 {
		t.Fatalf("expected 1 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	if got[0].err != nil {
		t.Fatalf("expected same-profile dashboard launch to succeed, got %v", got[0].err)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected one launched tab, got %d", len(client.launched))
	}
	if got := client.launched[0].UserVars["amx_profile"]; got != "work" {
		t.Fatalf("expected amx_profile=work, got %q", got)
	}
}

func TestDashboardLaunch_AlternateProfileSelectionLaunchesWithSelectedVars(t *testing.T) {
	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:       10,
				Title:    "api",
				Windows:  []kitty.Window{{ID: 11, IsActive: true, CWD: "/home/user/api", UserVars: kitty.UserVars{Profile: "work", Harness: "claude", Project: "api", AMXDirectory: "/home/user/api"}}},
				IsActive: true,
			}},
		}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", Color: "blue"},
			{Name: "personal", Harness: "codex", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Profile: "work"},
		},
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "api", Profile: "work", Account: "work", Harness: "claude", Project: "api", Directory: "/home/user/api", Key: session.SessionKey("work", "api")},
	})

	m = pressKey(m, "C")
	m = pressKey(m, "j")
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("expected non-nil cmd for alternate-profile dashboard launch")
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 || got[0].err != nil {
		t.Fatalf("expected alternate-profile dashboard launch to succeed, got %+v", got)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected one launched tab, got %d", len(client.launched))
	}
	if got := client.launched[0].UserVars["amx_profile"]; got != "personal" {
		t.Fatalf("expected amx_profile=personal, got %q", got)
	}
	if got := client.launched[0].UserVars["amx_harness"]; got != "codex" {
		t.Fatalf("expected amx_harness=codex, got %q", got)
	}
}

func TestDashboardLaunch_CrossHarnessUsesConfiguredProjectTemplate(t *testing.T) {
	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:    10,
				Title: "docs",
				Windows: []kitty.Window{{
					ID:       11,
					IsActive: true,
					CWD:      "/home/user/docs",
					UserVars: kitty.UserVars{
						Profile:      "claude-work",
						Harness:      "claude",
						Project:      "docs",
						AMXDirectory: "/home/user/docs",
						Args:         "--dangerously-skip-permissions",
					},
				}},
				IsActive: true,
			}},
		}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", Args: []string{"--shared-default"}},
		Accounts: []config.Account{
			{Name: "claude-work", Harness: "claude", Args: []string{"--dangerously-skip-permissions"}},
			{Name: "codex-work", Harness: "codex", Args: []string{"--model", "gpt-5"}},
		},
		Sessions: []config.Session{
			{Title: "docs", Directory: "/home/user/docs", Profile: "claude-work"},
		},
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{
			TabID: 10, WindowID: 11, Base: "docs", Profile: "claude-work",
			Account: "claude-work", Harness: "claude", Project: "docs",
			Directory: "/home/user/docs", Args: "--dangerously-skip-permissions",
			Key: session.SessionKey("claude-work", "docs"),
		},
	})

	m = pressKey(m, "C")
	m = pressKey(m, "j")
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("expected non-nil cmd for cross-harness dashboard launch")
	}
	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 || got[0].err != nil {
		t.Fatalf("expected cross-harness dashboard launch to succeed, got %+v", got)
	}
	if len(client.closed) != 0 {
		t.Fatalf("dashboard C must not close the source tab, closed=%v", client.closed)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected one launched tab, got %d", len(client.launched))
	}
	opts := client.launched[0]
	if got := opts.UserVars["amx_profile"]; got != "codex-work" {
		t.Fatalf("expected amx_profile=codex-work, got %q", got)
	}
	if got := opts.UserVars["amx_harness"]; got != "codex" {
		t.Fatalf("expected amx_harness=codex, got %q", got)
	}
	if got := opts.UserVars["cc_flags"]; got != "--model gpt-5" {
		t.Fatalf("expected target profile args, got cc_flags=%q", got)
	}
	if got := opts.UserVars["cc_explicit_flags"]; got != "" {
		t.Fatalf("expected empty explicit-flags marker for inherited args, got %q", got)
	}
	cmdline := strings.Join(opts.Cmdline, "\x00")
	if !strings.Contains(cmdline, "exec env CODEX_HOME="+config.ExpandTilde("~/.codex")+" RUST_LOG=debug codex --model gpt-5") {
		t.Fatalf("expected Codex command with target args, got %v", opts.Cmdline)
	}
	if strings.Contains(cmdline, "--dangerously-skip-permissions") || strings.Contains(cmdline, "--shared-default") {
		t.Fatalf("dashboard launch leaked source/default args into target harness: %v", opts.Cmdline)
	}
}

func TestDashboardRestartProfile_CrossHarnessUsesConfiguredProjectTemplate(t *testing.T) {
	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:    10,
				Title: "docs",
				Windows: []kitty.Window{{
					ID:       11,
					IsActive: true,
					CWD:      "/home/user/docs",
					UserVars: kitty.UserVars{
						Profile:      "claude-work",
						Harness:      "claude",
						Project:      "docs",
						AMXDirectory: "/home/user/docs",
						Args:         "--dangerously-skip-permissions",
					},
				}},
				IsActive: true,
			}},
		}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", Args: []string{"--shared-default"}},
		Accounts: []config.Account{
			{Name: "claude-work", Harness: "claude", Args: []string{"--dangerously-skip-permissions"}},
			{Name: "codex-work", Harness: "codex", Args: []string{"--model", "gpt-5"}},
		},
		Sessions: []config.Session{
			{Title: "docs", Directory: "/home/user/docs", Profile: "claude-work"},
		},
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{
			TabID: 10, WindowID: 11, Base: "docs", Profile: "claude-work",
			Account: "claude-work", Harness: "claude", Project: "docs",
			Directory: "/home/user/docs", Args: "--dangerously-skip-permissions",
			Key: session.SessionKey("claude-work", "docs"),
		},
	})

	m = pressKey(m, "c")
	m = pressKey(m, "j")
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after restart profile selection, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd for cross-harness restart")
	}
	msg := cmd()
	if done, ok := msg.(actionDoneMsg); !ok || done.err != nil {
		t.Fatalf("expected successful actionDoneMsg, got %#v", msg)
	}
	if len(client.closed) != 1 {
		t.Fatalf("dashboard c should close exactly the source tab, closed=%v", client.closed)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected one launched tab, got %d", len(client.launched))
	}
	opts := client.launched[0]
	if got := opts.UserVars["amx_profile"]; got != "codex-work" {
		t.Fatalf("expected amx_profile=codex-work, got %q", got)
	}
	if got := opts.UserVars["cc_flags"]; got != "--model gpt-5" {
		t.Fatalf("expected target profile args, got cc_flags=%q", got)
	}
	cmdline := strings.Join(opts.Cmdline, "\x00")
	if !strings.Contains(cmdline, "exec env CODEX_HOME="+config.ExpandTilde("~/.codex")+" RUST_LOG=debug codex --model gpt-5") {
		t.Fatalf("expected Codex command with target args, got %v", opts.Cmdline)
	}
	if strings.Contains(cmdline, "--dangerously-skip-permissions") || strings.Contains(cmdline, "--shared-default") {
		t.Fatalf("restart leaked source/default args into target harness: %v", opts.Cmdline)
	}
}

func TestDashboardLaunch_OrphanRowUsesSessionFromRunningTemplate(t *testing.T) {
	client := &fakeLaunchDispatchClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:       10,
				Title:    "orphan",
				Windows:  []kitty.Window{{ID: 11, IsActive: true, CWD: "/home/user/orphan", UserVars: kitty.UserVars{Profile: "work", Harness: "claude", Project: "orphan", AMXDirectory: "/home/user/orphan"}}},
				IsActive: true,
			}},
		}},
	}
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", Color: "blue"},
			{Name: "personal", Harness: "codex", Color: "green"},
		},
	}
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 10, WindowID: 11, Base: "orphan", Profile: "work", Account: "work", Harness: "claude", Project: "orphan", Directory: "/home/user/orphan", Key: session.SessionKey("work", "orphan"), Args: "--danger", Env: "FOO=bar"},
	})

	m = pressKey(m, "C")
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("expected non-nil cmd for orphan-row dashboard launch")
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 || got[0].err != nil {
		t.Fatalf("expected orphan-row dashboard launch to succeed, got %+v", got)
	}
	if len(client.launched) != 1 {
		t.Fatalf("expected one launched tab, got %d", len(client.launched))
	}
	if got := client.launched[0].UserVars["amx_project"]; got != "orphan" {
		t.Fatalf("expected amx_project=orphan, got %q", got)
	}
	if got := client.launched[0].Directory; got != "/home/user/orphan" {
		t.Fatalf("expected directory /home/user/orphan, got %q", got)
	}
}

// TestDispatchPickerLaunch_MissingAccountReportsError verifies that a stale
// or invalid profile reference does not look like a successful launch. The
// progress handler only quits when every launchProgressMsg has nil err, so
// this branch must surface a real error.
func TestDispatchPickerLaunch_MissingAccountReportsError(t *testing.T) {
	m := launchDispatchModel(t, nil)
	m.cfg.Sessions[0].Account = "missing"
	m.pickerSessions = sortPickerSessions(m.cfg.Sessions)
	m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
	m.pickerSelected = make(map[int]bool)
	selectByTitle(&m, "api")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd for selected session with missing account")
	}

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 1 {
		t.Fatalf("expected 1 launchProgressMsg, got %d (%+v)", len(got), got)
	}
	if got[0].err == nil {
		t.Fatal("expected missing account to be reported as launch error")
	}
	if got[0].title != "api" {
		t.Errorf("expected queued title api, got %q", got[0].title)
	}
}

// TestDispatchPickerLaunch_RunningSnapshotIsStable verifies that selected
// sessions are finalized synchronously at dispatch time. Mutating
// m.pickerRunning after dispatch does NOT affect which sessions launch because
// the closures don't re-poll the snapshot.
func TestDispatchPickerLaunch_RunningSnapshotIsStable(t *testing.T) {
	m := launchDispatchModel(t, nil)
	selectByTitle(&m, "api", "backend")

	cmd := m.dispatchPickerLaunch()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	// Simulate the "race" scenario described in the original session plan:
	// A's tab appears in m.sessions (and therefore in any rebuilt
	// pickerRunning snapshot) before B's closure fires. Mutate both maps and
	// verify the already-dispatched closures still emit both progress values.
	m.pickerRunning[session.SessionKey("work", "api")] = 1
	m.pickerRunning[session.SessionKey("work", "backend")] = 1
	m.sessions = append(m.sessions, session.RunningSession{
		TabID: 99, Base: "api", Account: "work",
	})

	got := countLaunchProgress(collectLeafMsgs(t, cmd))
	if len(got) != 2 {
		t.Fatalf("expected 2 launchProgressMsg even after pickerRunning mutation, got %d", len(got))
	}
}
