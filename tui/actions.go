package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
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
			available:   func(m Model) bool { return !m.targetSession.Locked },
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
			available:   func(m Model) bool { return !m.targetSession.Locked },
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
			available:   func(m Model) bool { return !m.targetSession.Locked },
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
			keyDisplay:  "O",
			keyMatch:    keyOpenEditor,
			label:       "open in editor",
			description: "open the project directory in $VISUAL/$EDITOR (or defaults.editor)",
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
				return m, openInEditorActionCmd(m, cs)
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
		{
			keyDisplay:  "L",
			keyMatch:    keyLock,
			label:       "lock/unlock",
			description: "toggle session lock (prevents close/restart/override)",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.mode = ModeList
				return m, toggleLockCmd(m.client, m.targetSession)
			},
		},
		{
			keyDisplay:  "m",
			keyMatch:    keyM,
			label:       "mute/unmute",
			description: "toggle session mute (suppresses macOS notifications)",
			available:   func(Model) bool { return true },
			run: func(m Model) (Model, tea.Cmd) {
				m.mode = ModeList
				return m, toggleMuteCmd(m.client, m.targetSession)
			},
		},
	}
}

// actionDescIndent is the column at which wrapped description text must
// resume so continuation lines align under the first description column.
// It matches the 22 visible columns produced by `%-2s  %-18s` for the
// key+label prefix plus the 2-space separator before the description.
const actionDescIndent = 24

// renderActions renders the actions overlay for m.targetSession. Available
// actions render in default text; unavailable ones render dim. Descriptions
// are word-wrapped to descWidth so continuation lines indent under the
// description column instead of dangling at the left edge.
func renderActions(m Model, descWidth int) string {
	if descWidth < 1 {
		descWidth = 1
	}
	indent := strings.Repeat(" ", actionDescIndent)

	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Actions for "+runningSessionDisplayTitle(m.targetSession)) + "\n\n")
	for _, a := range actionsFor(m) {
		avail := a.available(m)
		keyCol := fmt.Sprintf("%-2s  %-18s", a.keyDisplay, a.label)
		descLines := wrapText(a.description, descWidth)
		if len(descLines) == 0 {
			descLines = []string{""}
		}
		style := styleDim
		keyRendered := keyCol
		if !avail {
			style = styleFooterUnavailable
			keyRendered = styleFooterUnavailable.Render(keyCol)
		}
		sb.WriteString(keyRendered + "  " + style.Render(descLines[0]) + "\n")
		for _, cont := range descLines[1:] {
			sb.WriteString(indent + style.Render(cont) + "\n")
		}
	}
	sb.WriteByte('\n')
	sb.WriteString(styleDim.Render("press a key to run · esc/q to back out") + "\n")
	return sb.String()
}

// wrapText greedy-wraps s into lines no wider than width. Tokens longer
// than width are hard-broken so the caller can guarantee every emitted
// line fits the available column budget.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return nil
	}
	var lines []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		lines = append(lines, cur.String())
		cur.Reset()
	}
	for _, word := range strings.Fields(s) {
		for len(word) > width {
			flush()
			lines = append(lines, word[:width])
			word = word[width:]
		}
		if cur.Len() == 0 {
			cur.WriteString(word)
			continue
		}
		if cur.Len()+1+len(word) > width {
			flush()
			cur.WriteString(word)
			continue
		}
		cur.WriteByte(' ')
		cur.WriteString(word)
	}
	flush()
	return lines
}

// renderActionsBox wraps renderActions in styleOverlayBox so the actions
// overlay can be composited as a centered modal on top of the dimmed
// dashboard. styleOverlayBox.Width sets the outer box width (including
// the 1-col border and 1-col padding on each side), so the usable text
// column is innerWidth-4 and the description column budget is
// innerWidth-4-actionDescIndent. The outer width is capped at 70 and
// clamped to m.width-4 on narrow terminals to leave a 2-col margin.
func renderActionsBox(m Model) string {
	innerWidth := 70
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	descWidth := innerWidth - 4 - actionDescIndent
	if descWidth < 1 {
		descWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderActions(m, descWidth), "\n"))
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

// openDirActionCmd opens a config session's directory in a plain shell tab.
// Emits pickerOpenDoneMsg so the model can quit on success or surface the
// error in the dashboard otherwise.
func openDirActionCmd(m Model, cs config.Session) tea.Cmd {
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		acc, ok := findAccountByName(accounts, cs.EffectiveProfile())
		if !ok {
			return pickerOpenDoneMsg{err: fmt.Errorf("no matching account for profile %q", cs.EffectiveProfile())}
		}
		return pickerOpenDoneMsg{err: session.OpenDirectory(client, cs, acc, cfg)}
	}
}

// openInEditorActionCmd opens a config session's directory in a new Kitty tab
// running the configured editor (defaults.editor → $VISUAL → $EDITOR → vi).
// Emits pickerOpenDoneMsg so the model can quit on success or surface the
// error in the dashboard otherwise.
func openInEditorActionCmd(m Model, cs config.Session) tea.Cmd {
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		acc, ok := findAccountByName(accounts, cs.EffectiveProfile())
		if !ok {
			return pickerOpenDoneMsg{err: fmt.Errorf("no matching account for profile %q", cs.EffectiveProfile())}
		}
		return pickerOpenDoneMsg{err: session.OpenInEditor(client, cs, acc, cfg)}
	}
}

// copyTitleCmd writes the given title to the system clipboard. Errors are
// reported via actionDoneMsg so the dashboard surfaces them in the header.
func copyTitleCmd(title string) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: clipboard.Copy(title)}
	}
}

// toggleLockCmd flips the lock state on the session's anchor window. Locked
// sessions reject close/restart/override from the dashboard.
func toggleLockCmd(client kitty.KittyClient, rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		var err error
		if rs.Locked {
			err = session.Unlock(client, rs.WindowID)
		} else {
			err = session.Lock(client, rs.WindowID)
		}
		return actionDoneMsg{err: err}
	}
}

// toggleMuteCmd flips the mute state on the session's anchor window. Muted
// sessions suppress macOS notifications across every notify path in the daemon.
// After flipping the user var, the tab title is rebuilt with the new muted flag
// so the 🔇 glyph appears immediately on the Kitty tab — without this push every
// downstream title-rewrite path only preserves a 🔇 already present and never
// seeds it.
func toggleMuteCmd(client kitty.KittyClient, rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		var err error
		if rs.Muted {
			err = session.Unmute(client, rs.WindowID)
		} else {
			err = session.Mute(client, rs.WindowID)
		}
		if err == nil {
			newMuted := !rs.Muted
			newTitle := palette.BuildTitleFull(rs.Base, rs.State, rs.Starred, newMuted)
			err = client.SetTabTitle(kitty.MatchTabByWindowID(rs.WindowID), newTitle)
		}
		return actionDoneMsg{err: err}
	}
}
