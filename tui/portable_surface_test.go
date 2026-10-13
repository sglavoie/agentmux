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

// Focusing a Kitty tab takes the operator to that tab, so the dashboard quits.
// An attached tmux view ran in the dashboard's own terminal and has given it
// back, so the dashboard carries on.
func TestTheDashboardSurvivesAnAttachedView(t *testing.T) {
	m := testModelWithSessions()
	if _, cmd := m.Update(commands.FocusDoneMsg{}); !producesQuit(cmd) {
		t.Fatal("a Kitty focus must still quit the dashboard")
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
	m := testModelWithSessions()
	if _, cmd := m.Update(commands.PickerOpenDoneMsg{}); !producesQuit(cmd) {
		t.Fatal("opening a Kitty tab must still quit the dashboard")
	}
	if _, cmd := m.Update(commands.PickerOpenDoneMsg{Stay: true}); producesQuit(cmd) {
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
	if !producesQuit(finish()) {
		t.Fatal("a Kitty launch must still hand the operator to the new tab")
	}
	commands.Portable = commands.PortableTerminals{Launch: runtimesession.BackendTmux}
	if producesQuit(finish()) {
		t.Fatal("a tmux launch opens no tab, so the dashboard must stay")
	}
}

// The sort order is computed, never held by a terminal: without Kitty the
// command is still offered, moves nothing, and says the sessions are in order.
func TestResortWithoutKittySaysSessionsAreAlwaysInSortOrder(t *testing.T) {
	m := testModelWithSessions()
	resort := commandByID(t, commands.ResortTabs)
	if availability := resort.AvailabilityFor(m, commands.Context{}); !availability.Available {
		t.Fatalf("resort inside Kitty is unavailable: %s", availability.Reason)
	}
	m.client = nil
	if availability := resort.AvailabilityFor(m, commands.Context{}); !availability.Available {
		t.Fatalf("resort without Kitty is refused: %s", availability.Reason)
	}
	msg := commands.Resort(nil, "alpha")()
	if msg != (commands.ResortDoneMsg{AlwaysSorted: true}) {
		t.Fatalf("resort without Kitty returned %#v, want it to report the sessions always sorted", msg)
	}
	updated, _ := m.Update(msg)
	got := updated.(Model)
	if got.actionErr != nil {
		t.Fatalf("resort without Kitty left an error: %v", got.actionErr)
	}
	if len(got.toasts) == 0 || got.toasts[len(got.toasts)-1].Text != resortAlwaysSortedText {
		t.Fatalf("toasts = %+v, want %q", got.toasts, resortAlwaysSortedText)
	}
}
