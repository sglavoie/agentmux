package actions

import (
	"context"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

func defaultDefinitions() []Definition {
	sessionRequirement := []EntityRequirement{{Kind: EntityRunningSession, Description: "live managed session"}}
	projectRequirement := []EntityRequirement{{Kind: EntityProject, Description: "configured project"}}
	conversationRequirement := []EntityRequirement{{Kind: EntityConversation, Description: "indexed conversation"}}
	return []Definition{
		{
			ID: Focus, Label: "focus tab", Description: "focus the session's Kitty tab", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				if deps.Kitty == nil {
					return Result{}, dependencyError(Focus, "Kitty client")
				}
				if err := deps.Kitty.FocusTab(kitty.MatchTabByID(request.Session.TabID)); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "focused session tab"}, nil
			},
		},
		{
			ID: Launch, Label: "launch new", Description: "launch a new session from a configured project", Requires: projectRequirement,
			Parameters: []ParameterSchema{{Name: "profile", Type: "string", Description: "optional profile override"}},
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				if deps.Kitty == nil || deps.Config == nil {
					return Result{}, dependencyError(Launch, "Kitty client and configuration")
				}
				project := *request.Project
				if profile := strings.TrimSpace(request.Parameters["profile"]); profile != "" {
					project.Profile, project.Account = profile, profile
				}
				account, ok := accountByName(deps.Config, project.EffectiveProfile())
				if !ok {
					return Result{}, &Error{Code: "not_found", Action: Launch, Message: fmt.Sprintf("no matching profile %q", project.EffectiveProfile())}
				}
				if err := session.Launch(deps.Kitty, project, account, deps.Config, session.LaunchPolicyAlwaysNewInstance); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "launched session"}, nil
			},
		},
		{
			ID: Continue, Label: "continue", Description: "focus an exact live conversation or resume its indexed source", Requires: conversationRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				return continueConversation(deps, *request.Conversation)
			},
		},
		{
			ID: Fork, Label: "fork", Description: "start a native branch from an indexed conversation", Requires: conversationRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				return forkConversation(deps, *request.Conversation)
			},
		},
		{
			ID: ForkWorktree, Label: "fork in new worktree", Description: "start an isolated branch in a Git worktree", Requires: conversationRequirement,
			Parameters:   []ParameterSchema{{Name: "allow_dirty", Type: "boolean", Description: "allow a dirty source; uncommitted changes are not copied"}},
			Confirmation: Confirmation{Required: true, Prompt: "create an isolated worktree fork? If --allow-dirty is used, uncommitted source changes will not be copied."},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return forkConversationInWorktree(ctx, deps, *request.Conversation, parameterBool(request.Parameters, "allow_dirty"))
			},
		},
		{
			ID: Handoff, Label: "handoff", Description: "start a fresh target profile from a portable context pack", Requires: conversationRequirement,
			Parameters: []ParameterSchema{{Name: "profile", Type: "string", Description: "target configured profile", Required: true}, {Name: "edit", Type: "boolean", Description: "edit the generated pack before finalizing"}, {Name: "no_launch", Type: "boolean", Description: "finalize the pack without launching a target"}},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return handoffConversation(ctx, deps, *request.Conversation, strings.TrimSpace(request.Parameters["profile"]), parameterBool(request.Parameters, "edit"), parameterBool(request.Parameters, "no_launch"), "", "")
			},
		},
		{
			ID: OpenDirectory, Label: "open directory", Description: "open the project directory in a shell", Requires: projectRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				account, err := requireProjectAccount(OpenDirectory, deps, *request.Project)
				if err != nil {
					return Result{}, err
				}
				if err := session.OpenDirectory(deps.Kitty, *request.Project, account, deps.Config); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "opened project directory"}, nil
			},
		},
		{
			ID: OpenEditor, Label: "open in editor", Description: "open the project directory in an editor", Requires: projectRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				account, err := requireProjectAccount(OpenEditor, deps, *request.Project)
				if err != nil {
					return Result{}, err
				}
				if err := session.OpenInEditor(deps.Kitty, *request.Project, account, deps.Config); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "opened project in editor"}, nil
			},
		},
		{
			ID: CopyTitle, Label: "copy title", Description: "copy the session title to the system clipboard", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				copyFn := deps.Clipboard
				if copyFn == nil {
					copyFn = clipboard.Copy
				}
				title := request.Session.EffectiveProject()
				if title == "" {
					title = request.Session.Base
				}
				if err := copyFn(title); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "copied session title", Data: map[string]any{"title": title}}, nil
			},
		},
		{
			ID: ClearStatus, Label: "clear icon", Description: "clear the session's attention status", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				requestFn := deps.Daemon
				if requestFn == nil {
					requestFn = func(req daemon.Request) (daemon.Response, error) { return daemon.NewClient().Send(req) }
				}
				// This is intentionally best effort.  The daemon can be starting while
				// the TUI issues this request, and a later refresh converges its state.
				_, _ = requestFn(daemon.Request{Type: daemon.TypeStateUpdate, Action: "user_clear", WindowID: request.Session.WindowID, SessionTitle: request.Session.Base})
				return Result{Changed: true, Message: "cleared session status"}, nil
			},
		},
		{
			ID: ToggleLock, Label: "lock/unlock", Description: "toggle the session lock", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				if deps.Kitty == nil {
					return Result{}, dependencyError(ToggleLock, "Kitty client")
				}
				var err error
				if request.Session.Locked {
					err = session.Unlock(deps.Kitty, request.Session.WindowID)
				} else {
					err = session.Lock(deps.Kitty, request.Session.WindowID)
				}
				if err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "toggled session lock"}, nil
			},
		},
		{
			ID: ToggleMute, Label: "mute/unmute", Description: "toggle session notification mute", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				if deps.Kitty == nil {
					return Result{}, dependencyError(ToggleMute, "Kitty client")
				}
				var err error
				if request.Session.Muted {
					err = session.Unmute(deps.Kitty, request.Session.WindowID)
				} else {
					err = session.Mute(deps.Kitty, request.Session.WindowID)
				}
				if err != nil {
					return Result{}, err
				}
				parts := request.Session.TitleParts()
				parts.Muted = !request.Session.Muted
				if err := deps.Kitty.SetTabTitle(kitty.MatchTabByWindowID(request.Session.WindowID), palette.BuildTitleParts(parts)); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "toggled session mute"}, nil
			},
		},
		{
			ID: Close, Label: "close", Description: "close the live session", Requires: sessionRequirement,
			Confirmation: Confirmation{Required: true, Prompt: "close the session?"},
			available: func(_ Dependencies, request Request) Availability {
				if request.Session.Locked {
					return Availability{Reason: "session is locked"}
				}
				return Availability{Available: true}
			},
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				if deps.Kitty == nil {
					return Result{}, dependencyError(Close, "Kitty client")
				}
				if err := session.Close(deps.Kitty, request.Session.TabID); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "closed session"}, nil
			},
		},
		{
			ID: Restart, Label: "restart", Description: "relaunch the live session", Requires: sessionRequirement,
			Parameters:   []ParameterSchema{{Name: "profile", Type: "string", Description: "optional target profile"}},
			Confirmation: Confirmation{Required: true, Prompt: "restart the session?"},
			available: func(_ Dependencies, request Request) Availability {
				if request.Session.Locked {
					return Availability{Reason: "session is locked"}
				}
				return Availability{Available: true}
			},
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				if deps.Kitty == nil || deps.Config == nil {
					return Result{}, dependencyError(Restart, "Kitty client and configuration")
				}
				if profile := strings.TrimSpace(request.Parameters["profile"]); profile != "" {
					account, ok := accountByName(deps.Config, profile)
					if !ok {
						return Result{}, &Error{Code: "not_found", Action: Restart, Message: fmt.Sprintf("no matching profile %q", profile)}
					}
					template := session.SessionFromRunning(*request.Session)
					if request.Project != nil {
						template = *request.Project
					}
					if err := session.RestartWithAccountTemplate(deps.Kitty, *request.Session, template, account, deps.Config); err != nil {
						return Result{}, err
					}
					return Result{Changed: true, Message: "restarted session"}, nil
				}
				if err := session.Restart(deps.Kitty, *request.Session, deps.Config); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "restarted session"}, nil
			},
		},
	}
}

func dependencyError(id ID, dependency string) error {
	return &Error{Code: "missing_dependency", Action: id, Message: fmt.Sprintf("action %q requires %s", id, dependency)}
}
