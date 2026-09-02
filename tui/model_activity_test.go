package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func TestSessionsMsgStoresActivity(t *testing.T) {
	m := testModel()
	now := time.Now()
	activity := map[int]time.Time{42: now}
	updated, _ := m.Update(sessionsMsg{
		sessions: makeSessions(),
		activity: activity,
	})
	m2 := updated.(Model)
	if m2.lastActivity == nil {
		t.Fatal("expected lastActivity to be set after sessionsMsg")
	}
	if !m2.lastActivity[42].Equal(now) {
		t.Errorf("expected activity[42]=%v, got %v", now, m2.lastActivity[42])
	}
}

// TestSessionsMsgStoresWatchdog asserts that the watchdog snapshot rides on
// sessionsMsg alongside the existing activity map and is stored on the model
// for the next render pass to consume.
func TestSessionsMsgStoresWatchdog(t *testing.T) {
	m := testModel()
	wd := map[int]protocol.WatchdogState{
		7: {LastAction: "active", LastEventAt: time.Now().Unix(), ThresholdSec: 600},
	}
	updated, _ := m.Update(sessionsMsg{
		sessions: makeSessions(),
		watchdog: wd,
	})
	m2 := updated.(Model)
	if m2.watchdog == nil {
		t.Fatal("expected watchdog to be set after sessionsMsg")
	}
	got, ok := m2.watchdog[7]
	if !ok {
		t.Fatal("expected watchdog entry for window 7")
	}
	if got.LastAction != "active" || got.ThresholdSec != 600 {
		t.Errorf("watchdog entry mismatch: %+v", got)
	}
}

func TestSessionsMsgNilActivityClassifiesAsActive(t *testing.T) {
	// With no activity map, an empty-state session must not flip to Idle.
	m := testModel()
	sessions := []session.RunningSession{
		{TabID: 1, WindowID: 1, Base: "fresh", Account: "work", State: ""},
	}
	m = loadSessions(m, sessions)
	if g := dashboard.StateGroup(m.filtered[0], m.lastActivity, m.idleThreshold); g != 1 {
		t.Errorf("expected fresh empty-state session to be Active (1), got %d", g)
	}
}

func TestSessionsMsgStaleActivityFlipsToIdle(t *testing.T) {
	m := testModel()
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 9, Base: "stale", Account: "work", State: ""},
	})
	// Force a tight threshold and a stale timestamp.
	m.idleThreshold = time.Second
	m.lastActivity = map[int]time.Time{9: time.Now().Add(-time.Hour)}
	if g := dashboard.StateGroup(m.filtered[0], m.lastActivity, m.idleThreshold); g != 2 {
		t.Errorf("expected stale empty-state session to be Idle (2), got %d", g)
	}
}

func TestNewModelDefaultsIdleThreshold(t *testing.T) {
	// Zero IdleThreshold in config should still produce a non-zero duration —
	// otherwise stateGroup's threshold check disables itself.
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, IdleThreshold: 0},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	if m.idleThreshold <= 0 {
		t.Errorf("expected non-zero idleThreshold default, got %v", m.idleThreshold)
	}
}

func TestNewModelHonorsConfiguredIdleThreshold(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, IdleThreshold: 42},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	if want := 42 * time.Second; m.idleThreshold != want {
		t.Errorf("expected idleThreshold=%v, got %v", want, m.idleThreshold)
	}
}

// ---- C key (dashboard launch) tests ----

func TestCapitalCKeyOpensDashboardLaunchPicker(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C, got %d", m.mode)
	}
	if m.configPickerPurpose != ConfigPickerDashboardLaunch {
		t.Fatalf("expected dashboard-launch purpose after C, got %d", m.configPickerPurpose)
	}
}

func TestCapitalCKeyEmptyListIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(keyPress("C"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after C on empty list, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after C with no sessions")
	}
}

func TestCapitalCUsesDashboardLaunchPurpose(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C, got %d", m.mode)
	}
	if m.configPickerPurpose == ConfigPickerRestartProfile {
		t.Fatal("uppercase C should not reuse restart-profile purpose")
	}
}

// ---- Profile override persistence tests ----

func TestPickerProfileOverride_Persists(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "overrides.json"))

	// testModelWithSessions has: work/api, work/backend, personal/dotfiles.
	// sortPickerSessions sorts by account then title:
	//   [0] personal/dotfiles  [1] work/api  [2] work/backend
	m := testModelWithSessions()
	m = pressKey(m, "l") // open picker; cursor=0 → dotfiles (personal)

	// Open config picker for dotfiles. Accounts: [work(0), personal(1)].
	// configPickerCursor starts at 1 (personal matches dotfiles' default profile).
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C in picker, got %d", m.mode)
	}

	// Move cursor to work (index 0) and confirm.
	m = pressKey(m, "k")
	m = pressKey(m, "enter")

	if m.mode != ModePicker {
		t.Fatalf("expected to return to ModePicker, got %d", m.mode)
	}
	if m.pickerProfileOverride[0] != "work" {
		t.Fatalf("expected in-memory override[0]=work, got %q", m.pickerProfileOverride[0])
	}

	// Simulate a fresh dashboard: new model, open picker — override must reload from disk.
	m2 := testModelWithSessions()
	m2 = pressKey(m2, "l")
	if m2.pickerProfileOverride[0] != "work" {
		t.Fatalf("expected persisted override[0]=work after reload, got %q", m2.pickerProfileOverride[0])
	}
}

func TestClearStatusCmdUsesDaemonWithoutWritingTabTitle(t *testing.T) {
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	oldDaemonRequest := daemonRequest
	var gotRequest protocol.Request
	daemonRequest = func(req protocol.Request) (protocol.Response, error) {
		gotRequest = req
		return protocol.Response{OK: true}, nil
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	target := session.RunningSession{
		TabID: 7, WindowID: 99, Base: "🦊api", State: "bolt", Account: "work", Starred: true, Muted: true, Looping: true,
	}
	msg := clearStatusCmd(client, target)()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("expected actionDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Errorf("expected nil error from mock Kitty client, got %v", done.err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("clear must not write Kitty titles, got calls: %v", runner.Calls)
	}
	if gotRequest.Type != protocol.TypeStateUpdate || gotRequest.Action != "user_clear" || gotRequest.WindowID != 99 {
		t.Errorf("daemon request = %+v, want user_clear for window 99", gotRequest)
	}
}
