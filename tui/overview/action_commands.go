package overview

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// actionCommand is the default effect seam: what each action identity actually
// runs against the real world.
//
// It is a package function over the configuration and the client rather than a
// method, because it is bound once in defaultDeps and must not be able to read
// the model — every fact it needs is on the dispatch, which is the capture the
// operator chose rather than whatever the cursor has since moved to.
func actionCommand(cfg *config.Config, client any, d sessionactions.Dispatch) tea.Cmd {
	target := d.Target.Session
	switch d.Action {
	case commands.PeekScrollback:
		return commands.Peek(d.Target.WindowID())
	case commands.RestartSession:
		return commands.Restart(target, cfg)
	case commands.OverrideProfile:
		return restartWithProfileCmd(cfg, client, target, d.Profile)
	case commands.LaunchAnother:
		return launchWithProfileCmd(cfg, client, target, d.Profile)
	case commands.ClearStatusIcon:
		return commands.ClearStatus(target)
	case commands.MarkActiveIcon:
		return commands.MarkActive(target)
	case commands.CloseSession:
		return commands.CloseSessionRow(cfg, target)
	case commands.OpenDirectoryID:
		if cs, ok := commands.FindConfigSession(cfg, target.EffectiveProfile(), target.EffectiveProject()); ok {
			return commands.OpenDirectory(cfg, cs)
		}
		return nil
	case commands.OpenEditorID:
		if cs, ok := commands.FindConfigSession(cfg, target.EffectiveProfile(), target.EffectiveProject()); ok {
			return commands.OpenInEditor(cfg, cs)
		}
		return nil
	case commands.CopyTitleID:
		return commands.CopyTitle(dashboard.SessionDisplayTitle(target))
	case commands.ToggleLockID:
		return commands.ToggleLock(target)
	case commands.ToggleMuteID:
		return commands.ToggleMute(target)
	}
	return nil
}

// restartWithProfileCmd restarts the captured session under another profile.
//
// The registry call is recovered because it reaches a harness launcher this
// program does not own: a panic there would take the overview down over a
// session the operator can still see, and reporting it as a failed action leaves
// them on the grid with the reason.
func restartWithProfileCmd(cfg *config.Config, client any, rs session.RunningSession, profile string) tea.Cmd {
	template := commands.DashboardSessionTemplate(cfg, rs)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = commands.ActionDoneMsg{Err: fmt.Errorf("restart panicked: %v", r)}
			}
		}()
		_, err := commands.Registry.Execute(
			context.Background(),
			commands.TerminalDependencies(cfg),
			actions.Restart,
			actions.Request{
				Session:    &rs,
				Project:    &template,
				Parameters: map[string]string{"profile": profile},
				Confirmed:  true,
			},
		)
		return commands.ActionDoneMsg{Err: err}
	}
}

// launchWithProfileCmd launches a SECOND session for the captured row's project
// under the chosen profile, leaving the original running.
//
// It deliberately does not reuse the dashboard's launcher: that path drives a
// batch with progress reporting, a queue, and a launcher view, none of which
// exist here. One session, one completion, reported on the feedback row.
func launchWithProfileCmd(cfg *config.Config, client any, rs session.RunningSession, profile string) tea.Cmd {
	template := commands.SessionWithProfile(commands.DashboardSessionTemplate(cfg, rs), profile)
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = commands.ActionDoneMsg{Err: fmt.Errorf("launch panicked: %v", r)}
			}
		}()
		_, err := commands.Registry.Execute(
			context.Background(),
			commands.TerminalDependencies(cfg),
			actions.Launch,
			actions.Request{Project: &template},
		)
		return commands.ActionDoneMsg{Err: err}
	}
}
