package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// ProjectDirectoryProblem is the directory rule Launch refuses on, the same
// one `amx project list --missing` and `amx config validate` apply. Tests whose
// configurations name directories that do not exist replace it.
var ProjectDirectoryProblem = config.ProjectDirectoryProblem

func defaultDefinitions() []Definition {
	sessionRequirement := []EntityRequirement{{Kind: EntityRunningSession, Description: "live managed session"}}
	projectRequirement := []EntityRequirement{{Kind: EntityProject, Description: "configured project"}}
	conversationRequirement := []EntityRequirement{{Kind: EntityConversation, Description: "indexed conversation"}}
	worktreeRequirement := []EntityRequirement{{Kind: EntityWorktree, Description: "managed worktree"}}
	return []Definition{
		{
			ID: Focus, Label: "focus tab", Description: "focus the session's Kitty tab", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, dependencyError(Focus, "Kitty client")
				}
				if err := term.Focus(request.Session.GroupRef()); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "focused session tab"}, nil
			},
		},
		{
			ID: Launch, Label: "launch new", Description: "launch a new session from a configured project", Requires: projectRequirement,
			Parameters: []ParameterSchema{{Name: "profile", Type: "string", Description: "optional profile override"}},
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				rt, err := deps.launchRuntime(Launch)
				if err != nil {
					return Result{}, err
				}
				project := *request.Project
				if profile := strings.TrimSpace(request.Parameters["profile"]); profile != "" {
					project.Profile, project.Account = profile, profile
				}
				account, ok := accountByName(deps.Config, project.EffectiveProfile())
				if !ok {
					return Result{}, &Error{Code: "not_found", Action: Launch, Message: fmt.Sprintf("no matching profile %q", project.EffectiveProfile())}
				}
				// Kitty does not refuse a missing --cwd; it opens the tab
				// somewhere else and the harness starts in the wrong project.
				if problem := ProjectDirectoryProblem(project.Directory); problem != "" {
					issue := config.ProjectDirectoryIssue{Project: project.EffectiveProfile() + "/" + project.Title, Directory: project.Directory, Problem: problem}
					return Result{}, &Error{Code: "directory_unusable", Action: Launch, Message: issue.String()}
				}
				launched, err := session.LaunchOn(rt, project, account, deps.Config, session.LaunchPolicyAlwaysNewInstance)
				if err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "launched session", Data: deps.launchData(nil, launched)}, nil
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
					requestFn = func(req protocol.Request) (protocol.Response, error) { return ipc.NewClient().Send(req) }
				}
				// This is intentionally best effort.  The daemon can be starting while
				// the TUI issues this request, and a later refresh converges its state.
				_, _ = requestFn(operatorStateUpdate("user_clear", *request.Session))
				return Result{Changed: true, Message: "cleared session status"}, nil
			},
		},
		{
			ID: MarkActive, Label: "mark active", Description: "show the session as working until its next real event", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				requestFn := deps.Daemon
				if requestFn == nil {
					requestFn = func(req protocol.Request) (protocol.Response, error) { return ipc.NewClient().Send(req) }
				}
				// Best effort, exactly like ClearStatus: a later refresh converges.
				_, _ = requestFn(operatorStateUpdate("user_active", *request.Session))
				return Result{Changed: true, Message: "marked session active"}, nil
			},
		},
		{
			ID: ToggleLock, Label: "lock/unlock", Description: "toggle the session lock", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, dependencyError(ToggleLock, "Kitty client")
				}
				var err error
				if request.Session.Locked {
					err = session.Unlock(term, request.Session.PaneRef())
				} else {
					err = session.Lock(term, request.Session.PaneRef())
				}
				if err != nil {
					return Result{}, err
				}
				parts := request.Session.TitleParts()
				parts.Locked = !request.Session.Locked
				if err := term.SetTitle(request.Session.PaneRef(), palette.BuildTitleParts(parts)); err != nil {
					return Result{}, err
				}
				message := "locked session"
				if request.Session.Locked {
					message = "unlocked session"
				}
				return Result{Changed: true, Message: message}, nil
			},
		},
		{
			ID: ToggleMute, Label: "mute/unmute", Description: "toggle session notification mute", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, dependencyError(ToggleMute, "Kitty client")
				}
				var err error
				if request.Session.Muted {
					err = session.Unmute(term, request.Session.PaneRef())
				} else {
					err = session.Mute(term, request.Session.PaneRef())
				}
				if err != nil {
					return Result{}, err
				}
				parts := request.Session.TitleParts()
				parts.Muted = !request.Session.Muted
				if err := term.SetTitle(request.Session.PaneRef(), palette.BuildTitleParts(parts)); err != nil {
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
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, dependencyError(Close, "Kitty client")
				}
				observation, err := session.CloseObserved(term, request.Session.GroupRef())
				if err != nil && !errors.Is(err, session.ErrCloseTimeout) {
					return Result{}, err
				}
				// A timed-out poll is not a failed close: Kitty accepted the
				// request and the tab usually goes shortly after. It is reported
				// as an accepted-but-unconfirmed close rather than as an error,
				// which is what lets a caller offer close recovery only for the
				// closes it can show happened.
				if !observation.Confirmed() {
					return Result{Changed: true, Message: "requested session close; the tab was not observed to close", Data: map[string]any{DataCloseConfirmed: false}}, nil
				}
				// A confirmed close ends the runtime session the launch
				// registered; the registry is otherwise left to retirement.
				if request.Session.RuntimeSessionID != "" {
					if rt, rtErr := deps.runtimeFor(Close, *request.Session); rtErr == nil {
						_ = session.EndRuntimeSession(rt, request.Session.RuntimeSessionID)
					}
				}
				return Result{Changed: true, Message: "closed session", Data: map[string]any{DataCloseConfirmed: true}}, nil
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
				rt, err := deps.runtimeFor(Restart, *request.Session)
				if err != nil {
					return Result{}, err
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
					launched, err := session.RestartWithAccountTemplateOn(rt, *request.Session, template, account, deps.Config)
					if err != nil {
						return Result{}, err
					}
					return Result{Changed: true, Message: "restarted session", Data: deps.launchData(nil, launched)}, nil
				}
				launched, err := session.RestartOn(rt, *request.Session, deps.Config)
				if err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "restarted session", Data: deps.launchData(nil, launched)}, nil
			},
		},
		{
			ID: WorktreeList, Label: "list worktrees", Description: "list managed Git worktrees and current activity",
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return listWorktrees(ctx, deps)
			},
		},
		{
			ID: WorktreeShow, Label: "show worktree", Description: "show one managed Git worktree", Requires: worktreeRequirement,
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return showWorktree(ctx, deps, request)
			},
		},
		{
			ID: WorktreeReopen, Label: "reopen worktree", Description: "focus a live session or resume the newest conversation", Requires: worktreeRequirement,
			Parameters: []ParameterSchema{
				{Name: "session", Type: "string", Description: "live-session candidate identifier"},
				{Name: "profile", Type: "string", Description: "replacement profile for a fresh launch"},
			},
			available: func(deps Dependencies, request Request) Availability {
				return worktreeOpenAvailability(deps, request, true)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return reopenWorktree(ctx, deps, request)
			},
		},
		{
			ID: WorktreeShell, Label: "open worktree shell", Description: "open a shell in the worktree", Requires: worktreeRequirement,
			available: func(deps Dependencies, request Request) Availability {
				return worktreeOpenAvailability(deps, request, false)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return openWorktree(ctx, deps, request, false)
			},
		},
		{
			ID: WorktreeEditor, Label: "open worktree editor", Description: "open an editor in the worktree", Requires: worktreeRequirement,
			available: func(deps Dependencies, request Request) Availability {
				return worktreeOpenAvailability(deps, request, false)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return openWorktree(ctx, deps, request, true)
			},
		},
		{
			ID: WorktreeRemove, Label: "remove worktree", Description: "safely remove a clean inactive worktree", Requires: worktreeRequirement,
			Parameters:   []ParameterSchema{{Name: "delete_branch", Type: "boolean", Description: "also safely delete the merged worktree branch"}},
			Confirmation: Confirmation{Required: true, Prompt: "remove this worktree?"},
			available: func(_ Dependencies, request Request) Availability {
				return worktreeRemoveAvailability(request)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return removeWorktree(ctx, deps, request)
			},
		},
		{
			ID: WorktreeForget, Label: "forget worktree metadata", Description: "forget stale or invalid worktree metadata", Requires: worktreeRequirement,
			Confirmation: Confirmation{Required: true, Prompt: "forget this worktree metadata?"},
			available: func(_ Dependencies, request Request) Availability {
				return worktreeForgetAvailability(request)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return forgetWorktree(ctx, deps, request)
			},
		},
	}
}

func dependencyError(id ID, dependency string) error {
	return &Error{Code: "missing_dependency", Action: id, Message: fmt.Sprintf("action %q requires %s", id, dependency)}
}

// operatorStateUpdate addresses an operator's icon action to a session. A
// Kitty session is named by its window; any other by its runtime session,
// because only the daemon knows that session's state slot.
func operatorStateUpdate(action string, running session.RunningSession) protocol.Request {
	req := protocol.Request{Type: protocol.TypeStateUpdate, Action: action, SessionTitle: running.Base}
	if running.BackendKind() != runtimesession.BackendKitty {
		req.RuntimeSessionID = string(running.RuntimeSessionID)
		return req
	}
	req.WindowID = running.WindowID
	return req
}
