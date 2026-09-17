package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// stubStateQuery replaces the dashboard refresh's daemon seam for one test.
// Every test that exercises a real refresh must install one: the default seam
// dials the operator's live daemon socket.
func stubStateQuery(t *testing.T, query func() (protocol.Response, error)) {
	t.Helper()
	previous := commands.StateQuery
	commands.StateQuery = query
	t.Cleanup(func() { commands.StateQuery = previous })
}

// stubLocalKittySocket pins the Kitty instance the refresh believes it is
// running inside, so a test that expects the daemon's answer to be consumed
// does not depend on whether `go test` was launched from inside Kitty. The
// refresh takes nothing from a daemon it cannot prove shares its instance —
// neither the window tree nor the per-window state keyed by that instance's
// window IDs.
func stubLocalKittySocket(t *testing.T, socket string) {
	t.Helper()
	previous := commands.LocalKittySocket
	commands.LocalKittySocket = func() string { return socket }
	t.Cleanup(func() { commands.LocalKittySocket = previous })
}

// stubUnreachableDaemon is the common case: no daemon at all, so the refresh
// must fall back to listing Kitty directly.
func stubUnreachableDaemon(t *testing.T) {
	t.Helper()
	stubStateQuery(t, func() (protocol.Response, error) {
		return protocol.Response{}, errors.New("daemon unavailable")
	})
}

// testModel returns a Model with a minimal config and mock client.
func testModel() Model {
	cfg := &config.Config{
		// Preview carries config.Load's defaults for the same reason the uitest
		// fixtures do: a literal config never runs the defaulting pass, and a
		// zero Preview block describes a world no installation produces — ratio
		// 0 pins the inspector to its 40-column floor at every width, and
		// min_cols 0 lowers the split floor from the configured 100 to the pane
		// minimums' own 89.
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: uitestPreviewDefaults()},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	return NewModel(cfg, client)
}

// requireInspector asserts the inspector is actually on screen at the model's
// current size.
//
// It is what the retired `m.previewEnabled = true` became. The inspector is on
// by default now, so a test no longer turns it on — it only has to be sure the
// terminal it chose still affords one. Saying so is not ceremony: a test that
// picked a width below the split floors would get a queue-only dashboard, and
// every preview assertion under it would pass vacuously against a pane that was
// never drawn.
func requireInspector(t *testing.T, m Model) {
	t.Helper()
	if !m.inspectorActive() {
		t.Fatalf("no inspector at %dx%d: split widths fit=%v, body rows=%d (floor %d)",
			m.width, m.height, splitWidthsFit(m), dashboardInspectorBodyRows(m), inspectorMinBodyRows)
	}
}

func splitWidthsFit(m Model) bool {
	_, _, ok := dashboardSplitWidths(m)
	return ok
}

// withListRows installs rows as the dashboard's filtered list and rebuilds the
// item projection the cursor indexes, which is what every production write to
// filtered does. Tests that set filtered directly must go through here: the
// projection is a stored field, so a bare assignment leaves the model claiming
// an empty list and the assertion silently exercises the empty-list branch
// instead of the one it names.
// It also seeds the unfiltered list when that is empty, because the shared
// session-action controller resolves a captured target against m.sessions
// rather than against the filtered view: a model holding rows the full list
// never heard of would refuse every action as "no longer listed". A test that
// has already populated m.sessions itself keeps its own list, which is what
// lets the filtered-subset cases stay expressible.
func withListRows(m Model, rows ...session.RunningSession) Model {
	m.filtered = rows
	if len(m.sessions) == 0 {
		m.sessions = rows
	}
	m.rebuildItems()
	return m
}

// loadSessions feeds a set of RunningSession values into the model
// as if they came from a Kitty query.
func loadSessions(m Model, sessions []session.RunningSession) Model {
	return applySessions(m, commands.SessionsMsg{Sessions: sessions})
}

// applySessions feeds msg through the model as the completion of a refresh the
// model itself started. The refresh controller only accepts the sequence it is
// currently waiting on, so a hand-built message has to be stamped with it or it
// is (correctly) dropped as obsolete.
// It also answers the inspector read the refresh starts. The pane is on by
// default, so applying a session list retargets it and takes the single
// in-flight read slot; a helper that dropped that command would hand every
// later test a controller that can no longer fetch. feedPreviewReplies says
// more about why.
func applySessions(m Model, msg commands.SessionsMsg) Model {
	msg.Seq = armTopologyRefresh(&m)
	updated, cmd := m.Update(msg)
	m = updated.(Model)
	feedPreviewReplies(&m, cmd, 0)
	return m
}

// applyRefreshErr feeds a refresh failure through the model as the completion
// of a refresh it started, under the same sequence rule as applySessions.
func applyRefreshErr(m Model, err error) Model {
	updated, _ := m.Update(commands.ErrMsg{Seq: armTopologyRefresh(&m), Err: err})
	return updated.(Model)
}

// armTopologyRefresh leaves m in the state a dispatched refresh leaves it in
// and returns that refresh's sequence. A model fresh from the constructor is
// already armed, so this is a no-op on the first call.
func armTopologyRefresh(m *Model) int {
	if !m.topologyRefreshInFlight {
		m.topologyRefreshNextSeq++
		m.topologyRefreshActiveSeq = m.topologyRefreshNextSeq
		m.topologyRefreshInFlight = true
	}
	return m.topologyRefreshActiveSeq
}

func pressKey(m Model, key string) Model {
	updated, _ := m.Update(keyPress(key))
	return updated.(Model)
}

// typeString types each character of s into the model in sequence.
//
// A space is the one character that cannot go through keyPress: that helper
// spells space as the *key* — String() is "space" and Text is empty, which is
// what a handler matching a binding sees — while typing one into a filter or a
// search buffer needs the rune with its Text set. The filter DSL separates its
// components with spaces, so a test that composes a two-chip query depends on
// this branch.
func typeString(m Model, s string) Model {
	for _, ch := range s {
		if ch == ' ' {
			updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
			m = updated.(Model)
			continue
		}
		m = pressKey(m, string(ch))
	}
	return m
}

// keyPress creates a KeyPressMsg whose String() returns the given key string.
func keyPress(key string) tea.KeyPressMsg {
	switch key {
	case "j":
		return tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"})
	case "k":
		return tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"})
	case "g":
		return tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"})
	case "G":
		return tea.KeyPressMsg(tea.Key{Code: 'G', Text: "G"})
	case "/":
		return tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"})
	case "f":
		return tea.KeyPressMsg(tea.Key{Code: 'f', Text: "f"})
	case "q":
		return tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"})
	case "a":
		return tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"})
	case "e":
		return tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"})
	case "o":
		return tea.KeyPressMsg(tea.Key{Code: 'o', Text: "o"})
	case "x":
		return tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"})
	case "r":
		return tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"})
	case "p":
		return tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"})
	case "y":
		return tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"})
	case "n":
		return tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"})
	case "?":
		return tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"})
	case "c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"})
	case "C":
		return tea.KeyPressMsg(tea.Key{Code: 'C', Text: "C"})
	case "H":
		return tea.KeyPressMsg(tea.Key{Code: 'H', Text: "H"})
	case " ":
		return tea.KeyPressMsg(tea.Key{Code: ' '}) // no Text: String() returns "space"
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	case "left":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft})
	case "right":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyRight})
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "pgup":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp})
	case "pgdown":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown})
	case "ctrl+d":
		return tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl})
	case "ctrl+u":
		return tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModCtrl})
	case "ctrl+f":
		return tea.KeyPressMsg(tea.Key{Code: 'f', Mod: tea.ModCtrl})
	case "ctrl+b":
		return tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl})
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
	default:
		return tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
	}
}

func makeSessions() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", Directory: "/home/user/api", State: ""},
		{TabID: 2, Base: "backend", Account: "work", Directory: "/home/user/backend", State: "bolt"},
		{TabID: 3, Base: "dotfiles", Account: "personal", Directory: "/home/user/dotfiles", State: ""},
	}
}

// modalConfirmModel mirrors testModel but opts into the legacy y/n confirm
// flow via defaults.confirm_destructive=true. Used by the confirm-mode
// tests below so they keep exercising the modal path now that toast+undo
// is the default for x/r.
func modalConfirmModel() Model {
	m := testModel()
	m.cfg.Defaults.ConfirmDestructive = true
	return m
}

func testModelWithSessions() Model {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: uitestPreviewDefaults()},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
			{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	return NewModel(cfg, client)
}
