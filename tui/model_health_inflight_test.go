package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

type healthDispatch struct {
	daemon bool
	hooks  bool
}

func recordHealthChecks(m Model) (Model, *[]healthDispatch) {
	dispatches := &[]healthDispatch{}
	m.backgroundCommands.health = func(_ Model, checkDaemon, checkHooks bool) tea.Cmd {
		*dispatches = append(*dispatches, healthDispatch{daemon: checkDaemon, hooks: checkHooks})
		return func() tea.Msg { return nil }
	}
	return m, dispatches
}

// TestTicksStartNoHealthCheckWhileOneIsOut holds the rule that keeps a slow
// hooks scan from being joined by another every tick: a check that has not
// answered yet is the only one of its kind, however overdue the next one is.
func TestTicksStartNoHealthCheckWhileOneIsOut(t *testing.T) {
	m, dispatches := recordHealthChecks(testModel())

	m = send(m, commands.TickMsg{})
	want := []healthDispatch{{daemon: true}, {hooks: true}}
	if len(*dispatches) != 2 || (*dispatches)[0] != want[0] || (*dispatches)[1] != want[1] {
		t.Fatalf("first tick dispatched %+v, want the daemon and hooks checks as separate commands", *dispatches)
	}

	for range 5 {
		m = send(m, commands.TickMsg{})
	}
	if len(*dispatches) != 2 {
		t.Fatalf("ticks during in-flight checks dispatched %d more, want 0", len(*dispatches)-2)
	}

	// The daemon answers; the hooks scan is still out.
	m = send(m, commands.HealthMsg{DaemonChecked: true, DaemonAlive: true})
	m.daemonHealthCheckedAt = time.Now().Add(-2 * daemonHealthInterval)
	m = send(m, commands.TickMsg{})
	if len(*dispatches) != 3 || (*dispatches)[2] != (healthDispatch{daemon: true}) {
		t.Fatalf("after the daemon answered, the tick dispatched %+v, want one more daemon check and no hooks check", (*dispatches)[2:])
	}

	m = send(m, commands.HealthMsg{HookChecked: true})
	m.hookHealthCheckedAt = time.Now().Add(-2 * hookHealthInterval)
	m = send(m, commands.TickMsg{})
	if len(*dispatches) != 4 || (*dispatches)[3] != (healthDispatch{hooks: true}) {
		t.Fatalf("after the hooks check answered, the tick dispatched %+v, want one more hooks check", (*dispatches)[3:])
	}
}
