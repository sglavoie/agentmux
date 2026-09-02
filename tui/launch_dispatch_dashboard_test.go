package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

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
	if done, ok := msg.(commands.ActionDoneMsg); !ok || done.Err != nil {
		t.Fatalf("expected successful commands.ActionDoneMsg, got %#v", msg)
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
