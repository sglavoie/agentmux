package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/tui/commands"
)

func producesQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, inner := range msg {
			if producesQuit(inner) {
				return true
			}
		}
	}
	return false
}

// A switched terminal needs its popup dashboard to close. An attached tmux
// view that returns to the dashboard's terminal leaves the dashboard running.
func TestTheDashboardSurvivesAnAttachedView(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	if _, cmd := m.Update(commands.AttachDoneMsg{Switched: true}); !producesQuit(cmd) {
		t.Fatal("a switched terminal left its popup dashboard open")
	}
	next, cmd := m.Update(commands.AttachDoneMsg{})
	if producesQuit(cmd) {
		t.Fatal("returning from an attached session quit the dashboard")
	}
	if next.(Model).actionErr != nil {
		t.Fatalf("clean detach recorded an error: %v", next.(Model).actionErr)
	}
	failure := errors.New("runtime session is stale")
	next, cmd = m.Update(commands.AttachDoneMsg{Err: failure})
	if producesQuit(cmd) || !errors.Is(next.(Model).actionErr, failure) {
		t.Fatalf("failed attach: quit=%v err=%v, want the error shown and the dashboard kept", producesQuit(cmd), next.(Model).actionErr)
	}
}

// A dashboard that is a popup over a terminal switched that terminal to the
// session: it ends, which closes the popup and uncovers the session. A switch
// that was refused keeps it up.
func TestAPopupDashboardEndsOnceItsTerminalIsSwitched(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	if _, cmd := m.Update(commands.AttachDoneMsg{Switched: true}); !producesQuit(cmd) {
		t.Fatal("the popup stayed over the session it switched the terminal to")
	}
	failure := errors.New("the agent exited")
	next, cmd := m.Update(commands.AttachDoneMsg{Err: failure})
	if producesQuit(cmd) || !errors.Is(next.(Model).actionErr, failure) {
		t.Fatalf("refused switch: quit=%v err=%v, want the error shown and the dashboard kept", producesQuit(cmd), next.(Model).actionErr)
	}
}

func TestAViewThatRanInThisTerminalDoesNotQuitTheDashboard(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	if _, cmd := m.Update(commands.PickerOpenDoneMsg{}); producesQuit(cmd) {
		t.Fatal("a shell or editor that ran in this terminal quit the dashboard")
	}
}

func TestAPortableLaunchLeavesTheDashboardRunning(t *testing.T) {
	previous := commands.Portable
	t.Cleanup(func() { commands.Portable = previous })
	finish := func() tea.Cmd {
		m := testModelWithSessions()
		m.launchBatchID, m.launchBatchQuitOnSuccess = 3, true
		_, cmd := m.Update(launchProgressMsg{batchID: 3, total: 1, title: "api"})
		return cmd
	}
	commands.Portable = commands.PortableTerminals{}
	if producesQuit(finish()) {
		t.Fatal("the dashboard should stay open after launching a tmux session")
	}
	commands.Portable = commands.PortableTerminals{Launch: runtimesession.BackendTmux}
	if producesQuit(finish()) {
		t.Fatal("a tmux launch opens no tab, so the dashboard must stay")
	}
}
