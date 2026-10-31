//go:build tuitest

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/tui/commands"
	"time"
)

func fixtureFlightReader(events []flightlog.Event) func(flightlog.Filters, []string, int) tea.Cmd {
	return func(filters flightlog.Filters, harnesses []string, sequence int) tea.Cmd {
		return func() tea.Msg {
			snapshot := flightlog.Snapshot{SchemaVersion: 1, TrackingSince: uitestFrozenNow.Add(-time.Hour), Settings: flightlog.Settings{Enabled: true, Celebrations: true}, Events: events}
			return commands.FlightLogMsg{Report: flightlog.Build(snapshot, filters, uitestFrozenNow, harnesses), Sequence: sequence}
		}
	}
}
func flightLogFixture() uitestFixture {
	fixture := basicFixture()
	fixture.Name = "flight_log"
	fixture.Description = "Durable Flight Log with a resumed session, question, and finished turn"
	for i, kind := range []string{"launch", "state", "state", "state"} {
		e := flightlog.Event{ID: kind + time.Duration(i).String(), At: uitestFrozenNow.Add(time.Duration(i*10-60) * time.Second), Kind: kind, Key: "rs-flight", RuntimeSessionID: "rs-flight", Harness: "codex", Project: "API", Profile: "work", Epoch: "fixture", ConversationID: "codex:fixture-conversation"}
		switch i {
		case 0:
			e.LaunchID = "flight-launch"
			e.Operation = "resume"
		case 1:
			e.Icon = "bolt"
		case 2:
			e.BeforeIcon = "bolt"
			e.Icon = "question"
		case 3:
			e.BeforeIcon = "question"
			e.Icon = "stop"
		}
		fixture.FlightEvents = append(fixture.FlightEvents, e)
	}
	return fixture
}
