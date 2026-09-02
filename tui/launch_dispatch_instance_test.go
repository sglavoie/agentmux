package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
)

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
	if m.configPickerPurpose != launch.ConfigPickerLauncherOverride {
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
