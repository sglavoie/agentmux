package tui

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

func activeLoopTitleSyncCmd(client kitty.KittyClient, sessions []session.RunningSession, candidates []agentloop.Candidate) tea.Cmd {
	if client == nil || len(sessions) == 0 {
		return nil
	}
	sessionSnapshot := append([]session.RunningSession(nil), sessions...)
	candidateSnapshot := append([]agentloop.Candidate(nil), candidates...)
	return func() tea.Msg {
		return activeLoopTitleSyncMsg{
			err: syncActiveLoopTabTitles(client, sessionSnapshot, candidateSnapshot),
		}
	}
}

func syncActiveLoopTabTitles(client kitty.KittyClient, sessions []session.RunningSession, candidates []agentloop.Candidate) error {
	osWindows, err := client.ListWindows()
	if err != nil {
		return fmt.Errorf("list kitty windows: %w", err)
	}

	openTargets := activeLoopWorkspaceTargets(sessions)
	var errs []error
	for _, s := range sessions {
		if s.WindowID == 0 {
			continue
		}
		tab, _, ok := kitty.FindTabByWindowID(osWindows, s.WindowID)
		if !ok {
			continue
		}
		looping, err := agentloop.HasMatchingWorkspace(
			candidates,
			agentloop.WorkspaceTarget{CWD: s.Directory, Harness: s.Harness, WindowID: s.WindowID},
			openTargets,
		)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sessionDiagnosticName(s), err))
			continue
		}

		parts := palette.ParseTitleParts(tab.Title)
		parts.Looping = looping
		newTitle := palette.BuildTitleParts(parts)
		if newTitle == tab.Title {
			continue
		}
		if err := client.SetTabTitle(kitty.MatchTabByID(tab.ID), newTitle); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sessionDiagnosticName(s), err))
		}
	}
	return errors.Join(errs...)
}

func sessionDiagnosticName(s session.RunningSession) string {
	if s.Base != "" {
		return s.Base
	}
	if s.Title != "" {
		return s.Title
	}
	return fmt.Sprintf("window %d", s.WindowID)
}
