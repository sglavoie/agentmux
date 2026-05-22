package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// action describes one entry in the per-row actions overlay. keyDisplay is
// what the user sees in the overlay (e.g. "⏎"); keyMatch is the literal
// msg.String() value the handler compares against (e.g. "enter").
type action struct {
	keyDisplay  string
	keyMatch    string
	label       string
	description string
	available   func(m Model) bool
	run         func(m Model) (Model, tea.Cmd)
}

// actionsFor returns the ordered list of actions exposed in the overlay for
// the current target row (m.targetSession). The availability functions take
// the model so they can read live state (lastActivity, idle threshold, etc.).
func actionsFor(_ Model) []action {
	return []action{
		{
			keyDisplay:  "⏎",
			keyMatch:    keyEnter,
			label:       "focus tab",
			description: "jump to the session's Kitty tab",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				cmd := focusCmd(m.client, m.targetSession.TabID)
				m.mode = ModeList
				return m, cmd
			},
		},
		{
			keyDisplay:  "e",
			keyMatch:    keyE,
			label:       "peek scrollback",
			description: "open the session's scrollback in an overlay pager",
			available: func(m Model) bool {
				return m.targetSession.State == "bolt"
			},
			run: func(m Model) (Model, tea.Cmd) {
				cmd := peekCmd(m.client, m.targetSession.WindowID)
				m.mode = ModeList
				return m, cmd
			},
		},
		{
			keyDisplay:  "r",
			keyMatch:    keyR,
			label:       "restart",
			description: "close and relaunch the session",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "restart"
					m.confirmConsequence = restartConsequence
					m.mode = ModeConfirm
					return m, nil
				}
				m.mode = ModeList
				cmd := m.restartWithToast(m.targetSession)
				return m, cmd
			},
		},
		{
			keyDisplay:  "x",
			keyMatch:    keyX,
			label:       "close",
			description: "exit the Claude process; transcript persists",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.actionErr = nil
				if m.cfg.Defaults.ConfirmDestructive {
					m.confirmAction = "close"
					m.confirmConsequence = closeConsequence
					m.mode = ModeConfirm
					return m, nil
				}
				m.mode = ModeList
				cmd := m.closeWithToast(m.targetSession)
				return m, cmd
			},
		},
		{
			keyDisplay:  "c",
			keyMatch:    keyC,
			label:       "override profile",
			description: "restart under a different harness profile",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.configPickerAccounts = m.cfg.Accounts
				m.configPickerCurrent = m.targetSession.EffectiveProfile()
				m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
				m.configPickerReturnMode = ModeList
				m.configPickerProjectIdx = -1
				m.actionErr = nil
				m.mode = ModeConfigPicker
				return m, nil
			},
		},
		{
			keyDisplay:  "C",
			keyMatch:    keyClearStatus,
			label:       "clear icon",
			description: "drop the attention/key/stop badge on the tab title",
			available: func(m Model) bool {
				return stateGroup(m.targetSession, m.lastActivity, m.idleThreshold) == 0
			},
			run: func(m Model) (Model, tea.Cmd) {
				cmd := clearStatusCmd(m.client, m.targetSession)
				m.actionErr = nil
				m.mode = ModeList
				return m, cmd
			},
		},
		{
			keyDisplay:  "o",
			keyMatch:    keyO,
			label:       "open dir in shell",
			description: "spawn a plain shell tab at the session's directory",
			available: func(m Model) bool {
				_, ok := findConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				return ok
			},
			run: func(m Model) (Model, tea.Cmd) {
				cs, ok := findConfigSession(m.cfg, m.targetSession.EffectiveProfile(), m.targetSession.EffectiveProject())
				m.mode = ModeList
				if !ok {
					return m, nil
				}
				return m, openDirActionCmd(m, cs)
			},
		},
		{
			keyDisplay:  "y",
			keyMatch:    keyY,
			label:       "copy title",
			description: "copy the session title to the system clipboard",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				title := runningSessionDisplayTitle(m.targetSession)
				m.mode = ModeList
				return m, copyTitleCmd(title)
			},
		},
	}
}

// renderActions renders the actions overlay for m.targetSession. Available
// actions render in default text; unavailable ones render dim.
func renderActions(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Actions for "+runningSessionDisplayTitle(m.targetSession)) + "\n\n")
	for _, a := range actionsFor(m) {
		avail := a.available(m)
		// Display column: <2-cell key>  <label, padded>
		keyCol := fmt.Sprintf("%-2s  %-18s", a.keyDisplay, a.label)
		desc := a.description
		line := "  " + keyCol + "  " + styleDim.Render(desc)
		if !avail {
			line = "  " + styleFooterUnavailable.Render(keyCol) + "  " + styleFooterUnavailable.Render(desc)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteByte('\n')
	sb.WriteString(styleDim.Render("  press a key to run · esc to back out") + "\n")
	return sb.String()
}

// findConfigSession resolves a running session back to its configured project
// entry using the logical project title + profile identity rather than the
// rendered tab title, which may collide across harnesses.
func findConfigSession(cfg *config.Config, profileName, projectTitle string) (config.Session, bool) {
	if cfg == nil {
		return config.Session{}, false
	}
	for _, cs := range cfg.Sessions {
		if cs.Title == projectTitle && cs.EffectiveProfile() == profileName {
			return cs, true
		}
	}
	return config.Session{}, false
}

func findAccountByName(accounts []config.Account, name string) (config.Account, bool) {
	// Case-insensitive compare: SessionKey lowercases profile names at
	// runtime and config validation rejects case-only duplicates, so a
	// user var like "Work" (from a launch before a config rename) must
	// still resolve to the "work" profile. Matches the contract enforced
	// by session.findAccount and session.RestartWithAccount.
	for _, a := range accounts {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return config.Account{}, false
}

func sessionWithProfile(sess config.Session, profileName string) config.Session {
	out := sess
	out.Profile = profileName
	out.Account = profileName
	return out
}

// openDirActionCmd opens a config session's directory in a plain shell tab,
// returning pickerOpenDoneMsg so the dashboard refreshes afterwards.
func openDirActionCmd(m Model, cs config.Session) tea.Cmd {
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		acc, ok := findAccountByName(accounts, cs.EffectiveProfile())
		if !ok {
			return pickerOpenDoneMsg{err: nil}
		}
		return pickerOpenDoneMsg{err: session.OpenDirectory(client, cs, acc, cfg)}
	}
}

// copyTitleCmd writes the given title to the system clipboard. Errors are
// reported via actionDoneMsg so the dashboard surfaces them in the header.
func copyTitleCmd(title string) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: clipboard.Copy(title)}
	}
}
