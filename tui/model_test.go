package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// testModel returns a Model with a minimal config and mock client.
func testModel() Model {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
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

// loadSessions feeds a set of RunningSession values into the model
// as if they came from a Kitty query.
func loadSessions(m Model, sessions []session.RunningSession) Model {
	updated, _ := m.Update(sessionsMsg{sessions: sessions})
	return updated.(Model)
}

func pressKey(m Model, key string) Model {
	updated, _ := m.Update(keyPress(key))
	return updated.(Model)
}

// typeString types each character of s into the model in sequence.
func typeString(m Model, s string) Model {
	for _, ch := range s {
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
	case " ":
		return tea.KeyPressMsg(tea.Key{Code: ' '}) // no Text: String() returns "space"
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
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
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
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
