package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// These run a real private tmux server and take it away.

func lostFixture(t *testing.T, titles ...string) (*tmuxtest.Fixture, *config.Config) {
	t.Helper()
	f := tmuxtest.New(t)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude, Color: "blue"}}}
	for _, title := range titles {
		project := config.Session{Title: title, Directory: t.TempDir(), Profile: "work"}
		cfg.Sessions = append(cfg.Sessions, project)
		if _, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, project, cfg.Accounts[0], cfg, session.LaunchPolicyAlwaysNewInstance); err != nil {
			t.Fatalf("launch %s: %v", title, err)
		}
	}
	usePortable(t, PortableTerminals{Tmux: f.Backend, Launch: runtimesession.BackendTmux, Registry: f.Registry})
	stubLocalKittySocket(t, "")
	stubStateQuery(t, func() (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") })
	return f, cfg
}

func refreshWith(t *testing.T, cfg *config.Config) SessionsMsg {
	t.Helper()
	msg, ok := Refresh(nil, cfg, "alpha", 1, 0)().(SessionsMsg)
	if !ok {
		t.Fatalf("refresh did not return sessions")
	}
	return msg
}

// A tmux server that disappears under running agents does not leave an empty
// list behind: the sessions it took stay listed, marked as lost, and the
// refresh says the server is gone. A lost row is not a running session, and
// closing it takes it off the list.
func TestAVanishedTmuxServerLeavesItsSessionsListedAsLost(t *testing.T) {
	f, cfg := lostFixture(t, "api", "web")
	before := refreshWith(t, cfg)
	if len(before.Sessions) != 2 || before.PortableErr != nil {
		t.Fatalf("before = %d sessions, %v; want both agents", len(before.Sessions), before.PortableErr)
	}

	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	var lost SessionsMsg
	for range 2 {
		lost = refreshWith(t, cfg)
		if len(lost.Sessions) != 2 {
			t.Fatalf("after the server vanished the list has %d rows; want the 2 it took", len(lost.Sessions))
		}
		var outage *PortableOutage
		if !errors.As(lost.PortableErr, &outage) || outage.Lost != 2 || outage.Err != nil {
			t.Fatalf("outage = %v; want 2 sessions lost with the server", lost.PortableErr)
		}
		if !strings.Contains(lost.PortableErr.Error(), "server is gone") {
			t.Fatalf("outage reads %q; want it to say the server is gone", lost.PortableErr)
		}
		for _, rs := range lost.Sessions {
			if !PortableLost(rs) || rs.State != "error" {
				t.Fatalf("row %s: lost=%v state=%q; want it marked lost", rs.Project, PortableLost(rs), rs.State)
			}
		}
	}
	// Nothing is running: the probe actions rely on must not count them.
	live, err := TerminalDependencies(nil, cfg).LiveSessions(cfg)
	if err != nil || len(live) != 0 {
		t.Fatalf("live probe = %d sessions, %v; want none", len(live), err)
	}

	// Closing a lost row dismisses it and touches no server.
	done, ok := CloseSessionRow(nil, cfg, lost.Sessions[0])().(ActionDoneMsg)
	if !ok || done.Err != nil || done.Unconfirmed {
		t.Fatalf("closing a lost row = %+v; want it dismissed", done)
	}
	after := refreshWith(t, cfg)
	if len(after.Sessions) != 1 || after.Sessions[0].RuntimeSessionID != lost.Sessions[1].RuntimeSessionID {
		t.Fatalf("after dismissing one, the list = %+v; want the other lost row", after.Sessions)
	}
}

// Sessions the operator ended are not an outage: the server exits with its
// last session, and the list is simply empty.
func TestClosingEverySessionEmptiesTheListWithoutAnOutage(t *testing.T) {
	_, cfg := lostFixture(t, "api")
	before := refreshWith(t, cfg)
	if len(before.Sessions) != 1 {
		t.Fatalf("before = %d sessions; want the agent", len(before.Sessions))
	}
	done, ok := CloseSessionRow(nil, cfg, before.Sessions[0])().(ActionDoneMsg)
	if !ok || done.Err != nil {
		t.Fatalf("close = %+v", done)
	}
	after := refreshWith(t, cfg)
	if len(after.Sessions) != 0 || after.PortableErr != nil {
		t.Fatalf("after closing everything: %d rows, %v; want an empty list and no outage", len(after.Sessions), after.PortableErr)
	}
}

// A server that answers again replaces whatever an outage kept.
func TestANewServerReplacesLostRows(t *testing.T) {
	f, cfg := lostFixture(t, "api")
	refreshWith(t, cfg)
	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	if lost := refreshWith(t, cfg); len(lost.Sessions) != 1 || lost.PortableErr == nil {
		t.Fatalf("lost = %d rows, %v; want the lost agent", len(lost.Sessions), lost.PortableErr)
	}
	project := config.Session{Title: "web", Directory: t.TempDir(), Profile: "work"}
	if _, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, project, cfg.Accounts[0], cfg, session.LaunchPolicyAlwaysNewInstance); err != nil {
		t.Fatalf("launch on a new server: %v", err)
	}
	after := refreshWith(t, cfg)
	if len(after.Sessions) != 1 || after.Sessions[0].Project != "web" || after.PortableErr != nil || PortableLost(after.Sessions[0]) {
		t.Fatalf("after a new server: %+v, %v; want only the new agent and no outage", after.Sessions, after.PortableErr)
	}
}
