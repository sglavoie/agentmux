package tui

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
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

func TestFetchCmdUsesDaemonBulkStateOverTitleParsing(t *testing.T) {
	oldDaemonRequest := daemonRequest
	daemonRequest = func(req daemon.Request) (daemon.Response, error) {
		if req.Type != daemon.TypeStateQuery {
			t.Fatalf("daemon request type = %q, want state_query", req.Type)
		}
		return daemon.Response{
			OK: true,
			States: map[int]daemon.WindowStateSnapshot{
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
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: "/work/api"},
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
	daemonRequest = func(daemon.Request) (daemon.Response, error) {
		return daemon.Response{}, errors.New("daemon unavailable")
	}
	t.Cleanup(func() { daemonRequest = oldDaemonRequest })

	client := NewUITestKittyClient([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    7,
			Title: palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Looping: true}),
			Windows: []kitty.Window{{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: "/work/api"},
			}},
		}},
	}})

	msg := fetchCmd(client, &config.Config{Defaults: config.Defaults{Sort: "alpha"}}, "alpha")().(sessionsMsg)
	if got := msg.sessions[0]; got.State != "stop" || !got.Looping {
		t.Fatalf("session state = %+v, want title-derived stop + looping fallback", got)
	}
}
