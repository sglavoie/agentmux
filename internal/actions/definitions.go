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
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
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
	planRequirement := []EntityRequirement{{Kind: EntityPlan, Description: "split plan directory"}}
	return []Definition{
		{
			ID: Focus, Label: "focus tab", Description: "focus the session's Kitty tab", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, missingTerminal(Focus, *request.Session)
				}
				if err := term.Focus(request.Session.GroupRef()); err != nil {
					return Result{}, err
				}
				return Result{Changed: true, Message: "focused session tab"}, nil
			},
		},
		{
			ID: Launch, Label: "launch new", Description: "launch a new session from a configured project", Requires: projectRequirement,
			Parameters: []ParameterSchema{
				{Name: "profile", Type: "string", Description: "optional profile override"},
				{Name: "directory", Type: "string", Description: "optional working directory override"},
			},
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				rt, err := deps.launchRuntime(Launch)
				if err != nil {
					return Result{}, err
				}
				project := *request.Project
				if profile := strings.TrimSpace(request.Parameters["profile"]); profile != "" {
					project.Profile, project.Account = profile, profile
				}
				// The override replaces the configured directory before the
				// check below, so an unusable override is refused the same way.
				if directory := strings.TrimSpace(request.Parameters["directory"]); directory != "" {
					project.Directory = directory
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
				// With no Kitty there is no tab to open: the directory is
				// shown where the surface's host shows a shell, or left to
				// the caller's own terminal.
				if deps.Kitty == nil {
					return deps.openUtility(OpenDirectory, false, request.Project.Directory)
				}
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
				if deps.Kitty == nil {
					return deps.openUtility(OpenEditor, true, request.Project.Directory)
				}
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
				stateRequest, err := operatorStateUpdate("user_clear", *request.Session)
				if err != nil {
					return Result{}, err
				}
				_, _ = requestFn(stateRequest)
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
				stateRequest, err := operatorStateUpdate("user_active", *request.Session)
				if err != nil {
					return Result{}, err
				}
				_, _ = requestFn(stateRequest)
				return Result{Changed: true, Message: "marked session active"}, nil
			},
		},
		{
			ID: ToggleLock, Label: "lock/unlock", Description: "toggle the session lock", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, missingTerminal(ToggleLock, *request.Session)
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
					return Result{}, missingTerminal(ToggleMute, *request.Session)
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
			ID: TogglePin, Label: "pin/unpin", Description: "pin the session above every other, or unpin it", Requires: sessionRequirement,
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, missingTerminal(TogglePin, *request.Session)
				}
				// The pin is the title's star, so every surface that reads
				// the title (the dashboard, the workspace, the session order)
				// sees the same pins.
				parts := request.Session.TitleParts()
				parts.Starred = !request.Session.Starred
				if err := term.SetTitle(request.Session.PaneRef(), palette.BuildTitleParts(parts)); err != nil {
					return Result{}, err
				}
				message := "pinned session"
				if request.Session.Starred {
					message = "unpinned session"
				}
				return Result{Changed: true, Message: message}, nil
			},
		},
		{
			ID: Close, Label: "End Session", Description: "end the live agent; preserve its transcript", Requires: sessionRequirement,
			Confirmation: Confirmation{Required: true, Prompt: "end the session?"},
			available: func(_ Dependencies, request Request) Availability {
				if request.Session.Locked {
					return Availability{Reason: "session is locked"}
				}
				return Availability{Available: true}
			},
			execute: func(_ context.Context, deps Dependencies, request Request) (Result, error) {
				term := deps.sessionTerminal(*request.Session)
				if term == nil {
					return Result{}, missingTerminal(Close, *request.Session)
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
					return Result{Changed: true, Message: "requested session end; the terminal was not observed to exit", Data: map[string]any{DataCloseConfirmed: false}}, nil
				}
				// A confirmed close ends the runtime session the launch
				// registered; the registry is otherwise left to retirement.
				result := Result{Changed: true, Message: "ended session", Data: map[string]any{DataCloseConfirmed: true}}
				if request.Session.RuntimeSessionID != "" {
					if rt, rtErr := deps.runtimeFor(Close, *request.Session); rtErr == nil {
						// The session is gone either way, but a record left
						// live is later found stale, and attach and history
						// then call an agent the operator ended lost. That is
						// worth saying now rather than leaving to reconcile.
						if err := session.EndRuntimeSession(rt, request.Session.RuntimeSessionID); err != nil {
							return result, &PartialResultError{ActionError: &Error{
								Code: "registry_update_failed", Action: Close, Cause: err,
								Message: fmt.Sprintf("ended session, but runtime session %s could not be recorded as ended: %v", request.Session.RuntimeSessionID, err),
							}}
						}
					}
				}
				return result, nil
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
			ID: WorktreeDiff, Label: "review worktree changes", Description: "show what a worktree changed since its source branch and how it would land", Requires: worktreeRequirement,
			Parameters: []ParameterSchema{
				{Name: "committed", Type: "boolean", Description: "leave uncommitted changes out"},
				{Name: "patch", Type: "boolean", Description: "include the unified diff"},
				{Name: "color", Type: "boolean", Description: "color the patch for a terminal"},
			},
			available: func(_ Dependencies, request Request) Availability {
				return worktreeDiffAvailability(request)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return diffWorktree(ctx, deps, request)
			},
		},
		{
			ID: WorktreeLand, Label: "land worktree", Description: "fast-forward the source branch to a clean inactive worktree's branch", Requires: worktreeRequirement,
			Parameters:   []ParameterSchema{{Name: "remove", Type: "boolean", Description: "then remove the worktree and delete its landed branch"}},
			Confirmation: Confirmation{Required: true, Prompt: "land this worktree on its source branch?"},
			available: func(_ Dependencies, request Request) Availability {
				return worktreeLandAvailability(request)
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return landWorktree(ctx, deps, request)
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
		{
			ID: PlanRun, Label: "run plan", Description: "start an agent-session-loop run of a ready plan under a supervising agent", Requires: planRequirement,
			Parameters: []ParameterSchema{
				{Name: "project", Type: "string", Description: "configured project the supervising session launches from, when the plan's directory does not name exactly one"},
				{Name: "profile", Type: "string", Description: "optional profile the supervising session launches under"},
				{Name: "harness", Type: "string", Description: "harness that implements the plan's sessions; ASL's default when omitted", Enum: harness.Names()},
				{Name: "review_harness", Type: "string", Description: "harness for a final review; none when omitted", Enum: harness.Names()},
				{Name: "commit_mode", Type: "string", Description: "when the run commits; ASL's default when omitted", Enum: planCommitModes()},
				{Name: "check", Type: "boolean", Description: "report readiness and start nothing"},
			},
			execute: func(ctx context.Context, deps Dependencies, request Request) (Result, error) {
				return runPlan(ctx, deps, request)
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
func operatorStateUpdate(action string, running session.RunningSession) (protocol.Request, error) {
	req := protocol.Request{Type: protocol.TypeStateUpdate, Action: action, SessionTitle: running.Base}
	if running.RuntimeSessionID != "" {
		req.RuntimeSessionID = string(running.RuntimeSessionID)
		return req, nil
	}
	if running.BackendKind() != runtimesession.BackendKitty {
		return protocol.Request{}, fmt.Errorf("runtime session ID unavailable for %s session", running.BackendKind())
	}
	req.KittyListenOn = kitty.NormalizedListenAddressFromEnv()
	if req.KittyListenOn == "" || running.WindowID <= 0 {
		return protocol.Request{}, fmt.Errorf("kitty socket and window ID are required to update this session's status")
	}
	req.WindowID = running.WindowID
	return req, nil
}
