package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// findConfigSession resolves a running session back to its configured project
// entry using the logical project title + profile identity rather than the
// rendered tab title, which may collide across harnesses.
func findConfigSession(cfg *config.Config, profileName, projectTitle string) (config.Session, bool) {
	if cfg == nil {
		return config.Session{}, false
	}
	for _, cs := range cfg.Sessions {
		if cs.Title == projectTitle && strings.EqualFold(cs.EffectiveProfile(), profileName) {
			return cs, true
		}
	}
	return config.Session{}, false
}

func findConfigSessionForRunning(cfg *config.Config, rs session.RunningSession) (config.Session, bool) {
	if cfg == nil {
		return config.Session{}, false
	}
	projectTitle := rs.EffectiveProject()
	if cs, ok := findConfigSession(cfg, rs.EffectiveProfile(), projectTitle); ok {
		return cs, true
	}

	var titleMatches []config.Session
	for _, cs := range cfg.Sessions {
		if cs.Title == projectTitle {
			titleMatches = append(titleMatches, cs)
		}
	}
	if len(titleMatches) == 0 {
		return config.Session{}, false
	}

	runningDir := config.ExpandTilde(rs.Directory)
	if runningDir != "" {
		var dirMatches []config.Session
		for _, cs := range titleMatches {
			if config.ExpandTilde(cs.Directory) == runningDir {
				dirMatches = append(dirMatches, cs)
			}
		}
		if len(dirMatches) == 1 {
			return dirMatches[0], true
		}
	}
	if len(titleMatches) == 1 {
		return titleMatches[0], true
	}
	return config.Session{}, false
}

func dashboardSessionTemplate(cfg *config.Config, rs session.RunningSession) config.Session {
	if cs, ok := findConfigSessionForRunning(cfg, rs); ok {
		return cs
	}
	return session.SessionFromRunning(rs)
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
	return func() tea.Msg {
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.OpenDirectory, actions.Request{Project: &cs})
		return pickerOpenDoneMsg{err: err}
	}
}

// openInEditorActionCmd opens a config session's directory in a new Kitty tab
// running the configured editor (defaults.editor → $VISUAL → $EDITOR → vi).
// Emits pickerOpenDoneMsg so the model can quit on success or surface the
// error in the dashboard otherwise.
func openInEditorActionCmd(m Model, cs config.Session) tea.Cmd {
	return func() tea.Msg {
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: m.client, Config: m.cfg}, actions.OpenEditor, actions.Request{Project: &cs})
		return pickerOpenDoneMsg{err: err}
	}
}

// copyTitleCmd writes the given title to the system clipboard. Errors are
// reported via actionDoneMsg so the dashboard surfaces them in the header.
func copyTitleCmd(title string) tea.Cmd {
	return func() tea.Msg {
		target := session.RunningSession{Project: title, Base: title}
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Clipboard: copyToClipboard}, actions.CopyTitle, actions.Request{Session: &target})
		return actionDoneMsg{err: err}
	}
}

// copyTextCmd writes arbitrary text to the system clipboard. It reports via
// copyDoneMsg so full-layout viewers do not trigger a live-session refresh.
func copyTextCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return copyDoneMsg{err: copyToClipboard(text)}
	}
}

// toggleLockCmd flips the lock state on the session's anchor window. Locked
// sessions reject close/restart/override from the dashboard.
func toggleLockCmd(client kitty.KittyClient, rs session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: client}, actions.ToggleLock, actions.Request{Session: &rs})
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
		_, err := sharedActionRegistry.Execute(context.Background(), actions.Dependencies{Kitty: client}, actions.ToggleMute, actions.Request{Session: &rs})
		return actionDoneMsg{err: err}
	}
}
