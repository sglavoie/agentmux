package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// continueConversation performs the smart-continuation decision at the action
// boundary. It queries the daemon and Kitty immediately before focusing so a
// stale indexed row can never claim a reused or split-pane tab. When no exact
// live owner exists, the persisted transcript source is validated before a
// native resume starts in a new tab.
func continueConversation(deps Dependencies, meta transcript.SessionMeta) (Result, error) {
	if deps.Kitty == nil || deps.Config == nil {
		return Result{}, dependencyError(Continue, "Kitty client and configuration")
	}
	identity, err := conversationIdentity(meta, Continue)
	if err != nil {
		return Result{}, err
	}
	requestDaemon := deps.Daemon
	if requestDaemon == nil {
		requestDaemon = func(req daemon.Request) (daemon.Response, error) { return daemon.NewClient().Send(req) }
	}
	response, err := requestDaemon(daemon.Request{Type: daemon.TypeStateQuery})
	if err != nil {
		return Result{}, &Error{Code: "live_lookup_unavailable", Action: Continue, Message: fmt.Sprintf("checking live conversation: %v", err)}
	}
	if !response.OK {
		message := strings.TrimSpace(response.Error)
		if message == "" {
			message = "daemon rejected the live conversation query"
		}
		return Result{}, &Error{Code: "live_lookup_unavailable", Action: Continue, Message: message}
	}
	windows, err := deps.Kitty.ListWindows()
	if err != nil {
		return Result{}, &Error{Code: "live_lookup_unavailable", Action: Continue, Message: fmt.Sprintf("listing Kitty windows: %v", err)}
	}
	live, status := session.ResolveLiveConversationStatus(identity, response.States, windows)
	if status == session.LiveConversationAmbiguous {
		return Result{}, &Error{Code: "ambiguous_conversation", Action: Continue, Message: "multiple live tabs claim this conversation; refusing to focus or resume"}
	}
	if status == session.LiveConversationExact {
		if err := deps.Kitty.FocusTab(kitty.MatchTabByID(live.TabID)); err != nil {
			return Result{}, err
		}
		return Result{
			Changed: true,
			Message: "focused live conversation",
			Data:    map[string]any{"continuation": "focused", "tab_id": live.TabID, "window_id": live.WindowID},
		}, nil
	}
	project, account, err := continuityLaunchTarget(deps, meta, identity, Continue)
	if err != nil {
		return Result{}, err
	}
	if err := session.LaunchContinuity(deps.Kitty, project, account, deps.Config, harness.InvocationResume, identity.SessionID, ""); err != nil {
		return Result{}, continuityLaunchError(Continue, err)
	}
	return Result{
		Changed: true,
		Message: "resumed indexed conversation",
		Data:    map[string]any{"continuation": "resumed", "harness": identity.Harness, "session_id": identity.SessionID},
	}, nil
}

// forkConversation always creates a new native harness branch. It never
// focuses, closes, or otherwise changes a live source conversation.
func forkConversation(deps Dependencies, meta transcript.SessionMeta) (Result, error) {
	if deps.Kitty == nil || deps.Config == nil {
		return Result{}, dependencyError(Fork, "Kitty client and configuration")
	}
	identity, err := conversationIdentity(meta, Fork)
	if err != nil {
		return Result{}, err
	}
	if identity.Harness == harness.Copilot {
		return handoffConversation(context.Background(), deps, meta, meta.DisplayProfile(), false, false, "Portable fork", "")
	}
	project, account, err := continuityLaunchTarget(deps, meta, identity, Fork)
	if err != nil {
		return Result{}, err
	}
	if err := session.LaunchContinuity(deps.Kitty, project, account, deps.Config, harness.InvocationFork, identity.SessionID, ""); err != nil {
		return Result{}, continuityLaunchError(Fork, err)
	}
	return Result{
		Changed: true,
		Message: "forked indexed conversation",
		Data:    map[string]any{"continuation": "forked", "harness": identity.Harness, "session_id": identity.SessionID},
	}, nil
}

func handoffConversation(ctx context.Context, deps Dependencies, meta transcript.SessionMeta, targetProfile string, edit, noLaunch bool, label, launchDirectory string) (Result, error) {
	if deps.Config == nil {
		return Result{}, dependencyError(Handoff, "configuration")
	}
	if !noLaunch && deps.Kitty == nil {
		return Result{}, dependencyError(Handoff, "Kitty client")
	}
	identity, err := conversationIdentity(meta, Handoff)
	if err != nil {
		return Result{}, err
	}
	reader, ok := deps.Readers[identity.Harness]
	if !ok || reader == nil {
		return Result{}, &Error{Code: "source_unavailable", Action: Handoff, Message: fmt.Sprintf("no transcript reader is registered for harness %q", identity.Harness)}
	}
	if _, ok := accountByName(deps.Config, targetProfile); !ok {
		return Result{}, &Error{Code: "not_found", Action: Handoff, Message: fmt.Sprintf("no matching profile %q", targetProfile)}
	}
	runner := deps.Handoff
	if runner == nil {
		runner = handoff.NewWorkflow()
	}
	workflowResult, err := runner.Run(ctx, handoff.Request{
		Source: meta, Reader: reader, Config: deps.Config, TargetProfile: targetProfile,
		LaunchDirectory: launchDirectory, Edit: edit, NoLaunch: noLaunch, Label: label, Kitty: deps.Kitty,
	})
	result := resultFromHandoff(workflowResult)
	if strings.HasPrefix(label, "Portable") {
		result.Message = "started portable fork from finalized handoff pack"
		result.Data["continuation"] = "portable_fork"
		result.Data["fork_kind"] = "portable"
	} else if workflowResult.Launched {
		result.Message = "launched fresh handoff target"
	} else if workflowResult.Artifact.Path != "" {
		result.Message = "finalized handoff pack: " + workflowResult.Artifact.Path
	}
	if err == nil {
		return result, nil
	}
	var partial *handoff.PartialLaunchError
	if errors.As(err, &partial) {
		result.Message = "handoff pack finalized at " + workflowResult.Artifact.Path + "; target launch failed"
		return result, &PartialResultError{ActionError: &Error{Code: "partial_launch_failed", Action: Handoff, Message: err.Error()}}
	}
	return result, &Error{Code: "handoff_failed", Action: Handoff, Message: err.Error()}
}

// forkConversationInWorktree creates an explicit Git checkout before
// starting the native or portable fork. The in-place Fork action intentionally
// remains separate: it never creates Git state and keeps its existing launch
// context unchanged.
func forkConversationInWorktree(ctx context.Context, deps Dependencies, meta transcript.SessionMeta, allowDirty bool) (Result, error) {
	if deps.Kitty == nil || deps.Config == nil {
		return Result{}, dependencyError(ForkWorktree, "Kitty client and configuration")
	}
	identity, err := conversationIdentity(meta, ForkWorktree)
	if err != nil {
		return Result{}, err
	}
	project, account, err := continuityLaunchTarget(deps, meta, identity, ForkWorktree)
	if err != nil {
		return Result{}, err
	}
	runner := deps.Worktree
	if runner == nil {
		runner = worktree.NewWorkflow()
	}
	isolated, err := runner.Create(ctx, worktree.Request{
		Config: deps.Config, ProjectLabel: meta.Project, SourceCWD: meta.CWD, SessionID: identity.SessionID,
		Profile: account.Name, Harness: identity.Harness, AllowDirty: allowDirty,
	})
	if err != nil {
		return Result{}, worktreeActionError(err)
	}
	cleanup := func(cause error, result Result) (Result, error) {
		if cleanupErr := runner.RemoveUnused(context.Background(), isolated); cleanupErr != nil {
			if result.Data == nil {
				result.Data = map[string]any{}
			}
			result.Data["worktree_cleanup_error"] = cleanupErr.Error()
			return result, cause
		}
		return result, cause
	}

	if identity.Harness == harness.Copilot {
		result, err := handoffConversation(ctx, deps, meta, account.Name, false, false, "Portable isolated fork", isolated.Path)
		if err != nil || result.Data["launched"] != true {
			if err == nil {
				err = &Error{Code: "launch_failed", Action: ForkWorktree, Message: "portable isolated fork did not start a target harness"}
			}
			return cleanup(err, result)
		}
		return addIsolatedWorktreeResult(result, isolated), nil
	}
	project.Directory = isolated.Path
	if err := session.LaunchContinuity(deps.Kitty, project, account, deps.Config, harness.InvocationFork, identity.SessionID, ""); err != nil {
		return cleanup(continuityLaunchError(ForkWorktree, err), Result{})
	}
	return addIsolatedWorktreeResult(Result{
		Changed: true,
		Message: "forked indexed conversation in isolated worktree",
		Data:    map[string]any{"continuation": "forked", "harness": identity.Harness, "session_id": identity.SessionID, "fork_kind": "isolated"},
	}, isolated), nil
}

func addIsolatedWorktreeResult(result Result, isolated worktree.Result) Result {
	if result.Data == nil {
		result.Data = map[string]any{}
	}
	result.Changed = true
	result.Data["fork_kind"] = "isolated"
	result.Data["worktree"] = map[string]any{
		"project": isolated.ProjectLabel, "path": isolated.Path, "branch": isolated.Branch,
		"base_ref": isolated.BaseRef, "source_root": isolated.SourceRoot, "id": isolated.ID,
		"base_commit": isolated.BaseCommit, "source_branch": isolated.SourceBranch,
		"created_at": isolated.CreatedAt, "metadata_path": isolated.MetadataPath,
	}
	if result.Message == "started portable fork from finalized handoff pack" {
		result.Message = "started portable fork in isolated worktree"
	}
	return result
}

func worktreeActionError(err error) error {
	if errors.Is(err, worktree.ErrDirtySource) {
		return &Error{Code: "dirty_source", Action: ForkWorktree, Message: err.Error()}
	}
	if errors.Is(err, worktree.ErrNotGitCheckout) {
		return &Error{Code: "git_unavailable", Action: ForkWorktree, Message: err.Error()}
	}
	return &Error{Code: "worktree_failed", Action: ForkWorktree, Message: err.Error()}
}

func resultFromHandoff(result handoff.Result) Result {
	data := map[string]any{
		"handoff_id":     result.Artifact.ID,
		"path":           result.Artifact.Path,
		"content_hash":   result.Artifact.ContentHash,
		"status":         result.Artifact.Status,
		"created_at":     result.Artifact.CreatedAt,
		"updated_at":     result.Artifact.UpdatedAt,
		"target_profile": result.TargetProfile,
		"target_harness": result.TargetHarness,
		"launched":       result.Launched,
	}
	if result.DraftPath != "" {
		data["draft_path"] = result.DraftPath
	}
	if result.Label != "" {
		data["label"] = result.Label
	}
	return Result{Changed: result.Artifact.Path != "", Data: data}
}
