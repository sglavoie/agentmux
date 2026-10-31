package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestFlightLogRejectsStaleReadsAndRetainsReportOnFailure(t *testing.T) {
	m := testModel()
	m.width = 48
	m.height = 20
	m, cmd := m.enterFlightLog()
	if cmd == nil || m.mode != ModeFlightLog || !m.flightLoading {
		t.Fatal("did not enter Flight Log")
	}
	stale, _ := m.handleFlightLog(commands.FlightLogMsg{Sequence: m.flightSequence - 1, Report: flightlog.Report{}})
	if stale.(Model).flightReport != nil {
		t.Fatal("accepted stale read")
	}
	report := flightlog.Report{Filters: flightlog.Filters{Period: "week"}, Projects: []string{"API"}, Sessions: []flightlog.Session{}}
	for i := range 30 {
		report.Sessions = append(report.Sessions, flightlog.Session{Key: fmt.Sprintf("rs-%02d", i), Project: "A project with a long name that wraps in a narrow terminal", Harness: "codex"})
	}
	updated, _ := m.handleFlightLog(commands.FlightLogMsg{Sequence: m.flightSequence, Report: report})
	m = updated.(Model)
	m = pressKey(m, "2")
	m = pressKey(m, "G")
	if m.flightCursor != 29 || !strings.Contains(m.render(), "rs-29") {
		t.Fatalf("last session scrolled out of view: %s", m.render())
	}
	m = pressKey(m, "enter")
	if !m.flightDetail {
		t.Fatal("scorecard did not open")
	}
	m = pressKey(m, "e")
	if m.flightEventKind != "question" {
		t.Fatal("event filter did not change")
	}
	m = pressKey(m, "esc")
	if m.mode != ModeFlightLog || m.flightDetail {
		t.Fatal("scorecard back lost Flight Log")
	}
	failed, _ := m.handleFlightLog(commands.FlightLogMsg{Sequence: m.flightSequence, Err: errors.New("ledger unavailable")})
	m = failed.(Model)
	if m.flightReport == nil || m.flightError != "ledger unavailable" {
		t.Fatal("read failure discarded the previous report")
	}
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatal("back did not restore dashboard")
	}
}
