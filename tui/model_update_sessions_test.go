package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestSessionsMsg_RestoresCursorByTabIDWhenBaseDuplicates(t *testing.T) {
	m := testModel()
	initial := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Profile: "work", Account: "work", Project: "api", Harness: "claude", Directory: "/work/api"},
		{TabID: 2, WindowID: 22, Base: "api", Profile: "personal", Account: "personal", Project: "api", Harness: "claude", Directory: "/personal/api"},
	}
	m = loadSessions(m, initial)
	m.cursor = 1

	refreshed := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Profile: "work", Account: "work", Project: "api", Harness: "claude", Directory: "/work/api"},
		{TabID: 2, WindowID: 22, Base: "api", Profile: "personal", Account: "personal", Project: "api", Harness: "claude", Directory: "/personal/api"},
	}
	updated, _ := m.Update(sessionsMsg{sessions: refreshed})
	m = updated.(Model)

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("cursor restored to tab %d, want tab 2", got)
	}
}

func TestSessionsMsg_InitialSourceTabSelectsMatchingSession(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(sessionsMsg{
		sessions: []session.RunningSession{
			{TabID: 1, WindowID: 11, Base: "needs-permission", Account: "work", State: "key"},
			{TabID: 2, WindowID: 22, Base: "source-tab", Account: "work"},
		},
		selectionTabID: 2,
	})
	m = updated.(Model)

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("initial cursor selected tab %d, want source tab 2", got)
	}
}

func TestSessionsMsg_UnmappedInitialSourceDoesNotOverrideSelection(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "first", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "selected", Account: "work"},
	})
	for i := range m.filtered {
		if m.filtered[i].TabID == 2 {
			m.cursor = i
		}
	}

	updated, _ := m.Update(sessionsMsg{
		sessions: []session.RunningSession{
			{TabID: 1, WindowID: 11, Base: "first", Account: "work"},
			{TabID: 2, WindowID: 22, Base: "selected", Account: "work"},
		},
		selectionTabID: 999,
	})
	m = updated.(Model)

	if got := m.filtered[m.cursor].TabID; got != 2 {
		t.Fatalf("unmapped source changed selection to tab %d, want tab 2", got)
	}
}

func TestSessionsMsg_InitialSourceWithNoSessionsLeavesCursorUnchanged(t *testing.T) {
	m := testModel()
	m.cursor = 0
	updated, _ := m.Update(sessionsMsg{selectionTabID: 2})
	m = updated.(Model)

	if m.cursor != 0 || len(m.filtered) != 0 {
		t.Fatalf("empty startup state = cursor %d, rows %d; want cursor 0 and no rows", m.cursor, len(m.filtered))
	}
}

func TestSessionsMsg_DuplicateSelectionRequestDoesNotStealCursorAgain(t *testing.T) {
	sessions := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "first", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "requested", Account: "work"},
	}
	m := testModel()
	updated, _ := m.Update(sessionsMsg{
		sessions:       sessions,
		selectionTabID: 2,
		selectionToken: "request-1",
	})
	m = updated.(Model)
	for i := range m.filtered {
		if m.filtered[i].TabID == 1 {
			m.cursor = i
		}
	}

	updated, _ = m.Update(sessionsMsg{
		sessions:       sessions,
		selectionTabID: 2,
		selectionToken: "request-1",
	})
	m = updated.(Model)

	if got := m.filtered[m.cursor].TabID; got != 1 {
		t.Fatalf("duplicate request moved cursor to tab %d, want current tab 1", got)
	}
}

func TestInitialFetchCmdConsumesExistingDashboardSelectionRequest(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	client := NewUITestKittyClient([]kitty.OSWindow{{
		Tabs: []kitty.Tab{
			{
				ID:    7,
				Title: "api",
				Windows: []kitty.Window{{
					ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", LaunchID: "api-launch"},
				}},
			},
			{
				ID: 70,
				Windows: []kitty.Window{{
					ID: 700, UserVars: kitty.UserVars{DashboardSelect: "7:request-1"},
				}},
			},
		},
	}})

	msg := fetchCmd(client, &config.Config{}, "alpha")().(sessionsMsg)
	if msg.selectionTabID != 7 || msg.selectionToken != "request-1" {
		t.Fatalf("selection request = tab %d token %q, want tab 7 token request-1", msg.selectionTabID, msg.selectionToken)
	}
	calls := client.CallsFor("SetUserVars")
	if len(calls) != 1 || calls[0].Match != kitty.MatchWindowByID(700) || calls[0].Vars[kitty.DashboardSelectionVar] != "" {
		t.Fatalf("request clear calls = %+v, want dashboard window 700 cleared", calls)
	}
}

func TestInitialFetchCmdResolvesSourcePaneToContainingTab(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	client := NewUITestKittyClient([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    7,
			Title: "api",
			Windows: []kitty.Window{
				{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", LaunchID: "api-launch"}},
				{ID: 99, IsActive: true},
			},
		}},
	}})

	msg, ok := initialFetchCmd(client, &config.Config{}, "alpha", 99)().(sessionsMsg)
	if !ok {
		t.Fatal("initialFetchCmd returned an unexpected message type")
	}
	if msg.selectionTabID != 7 {
		t.Fatalf("source tab ID = %d, want 7", msg.selectionTabID)
	}
	if len(msg.sessions) != 1 || msg.sessions[0].TabID != 7 {
		t.Fatalf("sessions = %+v, want one managed row for tab 7", msg.sessions)
	}
}

func TestFetchCmdRemovesClosedSessionInsteadOfReanchoringDashboardOverlay(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	katanaDir := t.TempDir()
	pulseDir := t.TempDir()
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Sessions: []config.Session{
			{Title: "Katana", Account: "work", Directory: katanaDir},
			{Title: "Pulse", Account: "work", Directory: pulseDir},
		},
	}
	client := NewUITestKittyClient([]kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{
			{
				ID:       40,
				IsActive: true,
				Title:    "amx",
				Windows: []kitty.Window{{
					ID:       401,
					IsActive: true,
					CWD:      katanaDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"amx", "--window-id", "400"},
						CWD:     katanaDir,
					}},
				}},
			},
			{
				ID:    50,
				Title: "Pulse",
				Windows: []kitty.Window{{
					ID:       501,
					IsActive: true,
					CWD:      pulseDir,
					UserVars: kitty.UserVars{
						Profile: "work", Harness: "codex", Project: "Pulse",
						AMXDirectory: pulseDir, LaunchID: "pulse-launch",
					},
				}},
			},
		},
	}})

	m := loadSessions(NewModel(cfg, client), []session.RunningSession{
		{TabID: 40, WindowID: 400, Base: "Katana", Profile: "work", Account: "work", Project: "Katana"},
		{TabID: 50, WindowID: 501, Base: "Pulse", Profile: "work", Account: "work", Project: "Pulse"},
	})
	m.cursor = 0

	msg := fetchCmd(client, cfg, "alpha")().(sessionsMsg)
	if len(msg.sessions) != 1 || msg.sessions[0].Project != "Pulse" {
		t.Fatalf("post-close sessions = %+v, want only Pulse", msg.sessions)
	}
	if calls := client.CallsFor("SetUserVars"); len(calls) != 0 {
		t.Fatalf("dashboard overlay was re-anchored: %+v", calls)
	}

	updated, _ := m.Update(msg)
	m = updated.(Model)
	if len(m.filtered) != 1 || m.filtered[0].Project != "Pulse" || m.cursor != 0 {
		t.Fatalf("refreshed dashboard = rows %+v cursor %d, want only Pulse at cursor 0", m.filtered, m.cursor)
	}
}

func TestFetchCmdUsesDaemonBulkStateOverTitleParsing(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(req protocol.Request) (protocol.Response, error) {
		if req.Type != protocol.TypeStateQuery {
			t.Fatalf("daemon request type = %q, want state_query", req.Type)
		}
		return protocol.Response{
			OK: true,
			States: map[int]protocol.WindowStateSnapshot{
				42: {IconState: "key", Looping: true, LastAction: "permission", LastEventAt: 123, GuardAction: "permission"},
			},
		}, nil
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	client := NewUITestKittyClient([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    7,
			Title: palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"}),
			Windows: []kitty.Window{{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: "/work/api", LaunchID: "api-launch"},
			}},
		}},
	}})

	msg, ok := fetchCmd(client, &config.Config{Defaults: config.Defaults{Sort: "alpha"}}, "alpha")().(sessionsMsg)
	if !ok {
		t.Fatal("fetchCmd returned an unexpected message type")
	}
	if len(msg.sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(msg.sessions))
	}
	if got := msg.sessions[0]; got.State != "key" || !got.Looping {
		t.Fatalf("session state = %+v, want daemon state key + looping", got)
	}
}

func TestFetchCmdFallsBackToTitleStateWhenDaemonIsUnreachable(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	client := NewUITestKittyClient([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    7,
			Title: palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Looping: true}),
			Windows: []kitty.Window{{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: "/work/api", LaunchID: "api-launch"},
			}},
		}},
	}})

	msg := fetchCmd(client, &config.Config{Defaults: config.Defaults{Sort: "alpha"}}, "alpha")().(sessionsMsg)
	if got := msg.sessions[0]; got.State != "stop" || !got.Looping {
		t.Fatalf("session state = %+v, want title-derived stop + looping fallback", got)
	}
}

func TestFetchCmdPreservesRuntimeProfileOverrideInConfigPicker(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	const projectDir = "/projects/sis"
	client := NewUITestKittyClient([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    68,
			Title: "sis",
			Windows: []kitty.Window{{
				ID:       201,
				IsActive: true,
				CWD:      projectDir,
				UserVars: kitty.UserVars{
					Profile:      "tradition-claude",
					Account:      "tradition-claude",
					Harness:      "claude",
					Project:      "sis",
					AMXDirectory: projectDir,
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"claude", "--dangerously-skip-permissions"},
					CWD:     projectDir,
				}},
			}},
		}},
	}})
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "vta-claude", Harness: "claude", Color: "red"},
			{Name: "tradition-claude", Harness: "claude", Color: "blue"},
		},
		Sessions: []config.Session{{
			Title: "sis", Profile: "vta-claude", Directory: projectDir,
		}},
	}

	msg := fetchCmd(client, cfg, "alpha")().(sessionsMsg)
	if len(msg.sessions) != 1 || msg.sessions[0].EffectiveProfile() != "tradition-claude" {
		t.Fatalf("fetched sessions = %+v, want tradition-claude/sis", msg.sessions)
	}
	if calls := client.CallsFor("SetUserVars"); len(calls) != 0 {
		t.Fatalf("runtime profile override was re-anchored: %+v", calls)
	}

	m := NewModel(cfg, client)
	updated, _ := m.Update(msg)
	m = updated.(Model)
	m = pressKey(m, "c")
	if m.configPickerCurrent != "tradition-claude" {
		t.Fatalf("picker current profile = %q, want tradition-claude", m.configPickerCurrent)
	}
	out := stripAnsi(renderConfigPicker(m))
	var traditionRow string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "tradition-claude") {
			traditionRow = line
			break
		}
	}
	if !strings.Contains(traditionRow, "(current)") {
		t.Fatalf("picker did not mark tradition-claude current:\n%s", out)
	}

	m = pressKey(m, "k") // tradition-claude -> vta-claude
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("mode after choosing vta-claude = %d, want ModeList", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("choosing vta-claude was suppressed as current profile")
	}
}
