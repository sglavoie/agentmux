package commands

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// FindConfigSession resolves a running session back to its configured project
// entry using the logical project title + profile identity rather than the
// rendered tab title, which may collide across harnesses.
func FindConfigSession(cfg *config.Config, profileName, projectTitle string) (config.Session, bool) {
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

// findConfigSessionForRunning resolves a running session to a configured
// project, falling back to a title match disambiguated by directory when the
// profile identity alone does not resolve.
func findConfigSessionForRunning(cfg *config.Config, rs session.RunningSession) (config.Session, bool) {
	if cfg == nil {
		return config.Session{}, false
	}
	projectTitle := rs.EffectiveProject()
	if cs, ok := FindConfigSession(cfg, rs.EffectiveProfile(), projectTitle); ok {
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

// DashboardSessionTemplate returns the configured project a running session
// should relaunch from, synthesizing one from the live session when the
// dashboard row has no configured counterpart.
func DashboardSessionTemplate(cfg *config.Config, rs session.RunningSession) config.Session {
	if cs, ok := findConfigSessionForRunning(cfg, rs); ok {
		return cs
	}
	return session.SessionFromRunning(rs)
}

// FindAccountByName resolves a profile name to its configured account.
func FindAccountByName(accounts []config.Account, name string) (config.Account, bool) {
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

// SessionWithProfile rebinds a configured project to a different profile.
func SessionWithProfile(sess config.Session, profileName string) config.Session {
	out := sess
	out.Profile = profileName
	out.Account = profileName
	return out
}

// OpenDirectory opens a config session's directory in a plain shell tab.
// Emits PickerOpenDoneMsg so the model can quit on success or surface the
// error in the dashboard otherwise.
func OpenDirectory(client kitty.KittyClient, cfg *config.Config, cs config.Session) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Kitty: client, Config: cfg}, actions.OpenDirectory, actions.Request{Project: &cs})
		return PickerOpenDoneMsg{Err: err}
	}
}

// OpenInEditor opens a config session's directory in a new Kitty tab running
// the configured editor (defaults.editor → $VISUAL → $EDITOR → vi). Emits
// PickerOpenDoneMsg so the model can quit on success or surface the error in
// the dashboard otherwise.
func OpenInEditor(client kitty.KittyClient, cfg *config.Config, cs config.Session) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), actions.Dependencies{Kitty: client, Config: cfg}, actions.OpenEditor, actions.Request{Project: &cs})
		return PickerOpenDoneMsg{Err: err}
	}
}
