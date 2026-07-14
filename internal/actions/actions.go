// Package actions defines the application actions shared by interactive and
// command-line entry points.  It deliberately contains no Bubble Tea types:
// callers supply the current entity and the dependencies needed to perform an
// action, then decide how to present results, confirmations, and errors.
package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// ID is a stable action identifier shared by the TUI and CLI rather than a
// presentation label.
type ID string

const (
	Focus         ID = "session.focus"
	Launch        ID = "session.launch"
	Continue      ID = "session.continue"
	Fork          ID = "session.fork"
	ForkWorktree  ID = "session.fork_worktree"
	Handoff       ID = "session.handoff"
	OpenDirectory ID = "project.open_directory"
	OpenEditor    ID = "project.open_editor"
	CopyTitle     ID = "session.copy_title"
	ClearStatus   ID = "session.clear_status"
	ToggleLock    ID = "session.toggle_lock"
	ToggleMute    ID = "session.toggle_mute"
	Close         ID = "session.close"
	Restart       ID = "session.restart"
)

// EntityKind identifies a typed target accepted by an action.
type EntityKind string

const (
	EntityRunningSession EntityKind = "running_session"
	EntityProject        EntityKind = "project"
	EntityConversation   EntityKind = "conversation"
)

// EntityRequirement declares the target an action needs before it can run.
type EntityRequirement struct {
	Kind        EntityKind `json:"kind"`
	Description string     `json:"description,omitempty"`
}

// ParameterSchema describes one action parameter.  The registry preserves
// these schemas even when the current TUI does not expose a parameter yet, so
// a non-interactive caller can receive structured guidance instead of a prompt.
type ParameterSchema struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`
}

// Confirmation describes an intentional side effect.  The registry never
// prompts; callers render this metadata in interactive UIs or send an explicit
// confirmation flag in non-interactive flows.
type Confirmation struct {
	Required bool   `json:"required"`
	Prompt   string `json:"prompt,omitempty"`
}

// Availability explains whether an action can run for the supplied entities.
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Definition is one entry in the typed action registry.
type Definition struct {
	ID           ID                  `json:"id"`
	Label        string              `json:"label"`
	Description  string              `json:"description"`
	Requires     []EntityRequirement `json:"requires,omitempty"`
	Parameters   []ParameterSchema   `json:"parameters,omitempty"`
	Confirmation Confirmation        `json:"confirmation"`

	available func(Dependencies, Request) Availability
	execute   func(context.Context, Dependencies, Request) (Result, error)
}

// Dependencies are the injected seams an action can use.  Actions do not
// construct Kitty clients, read configuration files, or reach into Bubble Tea.
type Dependencies struct {
	Kitty     kitty.KittyClient
	Config    *config.Config
	Clipboard func(string) error
	Daemon    func(daemon.Request) (daemon.Response, error)
	Readers   map[string]transcript.Reader
	Handoff   handoff.Runner
	Worktree  worktree.Runner
}

// Request holds the typed targets and plain parameters supplied to one action.
// Project is a configured launch template, while Session is an already-live
// Kitty-backed session.
type Request struct {
	Session      *session.RunningSession
	Project      *config.Session
	Conversation *transcript.SessionMeta
	Parameters   map[string]string
	Confirmed    bool
}

// Result is presentation-neutral execution output.
type Result struct {
	Action  ID             `json:"action"`
	Changed bool           `json:"changed"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// Error is a stable action error suitable for a JSON error envelope.
type Error struct {
	Code              string            `json:"code"`
	Action            ID                `json:"action,omitempty"`
	Message           string            `json:"message"`
	MissingParameters []ParameterSchema `json:"missing_parameters,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// PartialResultError reports a requested side effect that completed its
// durable portion but failed later. Callers must present the accompanying
// Result (which includes the retained artifact) instead of discarding it as a
// normal all-or-nothing action failure.
type PartialResultError struct{ ActionError *Error }

func (e *PartialResultError) Error() string {
	if e == nil || e.ActionError == nil {
		return "action completed partially"
	}
	return e.ActionError.Error()
}

func (e *PartialResultError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.ActionError
}

// Registry provides deterministic lookup and execution of action definitions.
type Registry struct {
	byID map[ID]Definition
	ids  []ID
}

// New constructs a registry and rejects invalid or duplicate definitions.
func New(definitions ...Definition) (*Registry, error) {
	r := &Registry{byID: make(map[ID]Definition, len(definitions))}
	for _, definition := range definitions {
		if definition.ID == "" {
			return nil, errors.New("actions: action ID is required")
		}
		if definition.execute == nil {
			return nil, fmt.Errorf("actions: action %q has no handler", definition.ID)
		}
		if _, exists := r.byID[definition.ID]; exists {
			return nil, fmt.Errorf("actions: duplicate action ID %q", definition.ID)
		}
		definition.Requires = append([]EntityRequirement(nil), definition.Requires...)
		definition.Parameters = append([]ParameterSchema(nil), definition.Parameters...)
		r.byID[definition.ID] = definition
		r.ids = append(r.ids, definition.ID)
	}
	sort.Slice(r.ids, func(i, j int) bool { return r.ids[i] < r.ids[j] })
	return r, nil
}

// DefaultRegistry returns the standard actions.  Construction is pure and
// inexpensive; callers may create one per command or model without state
// leakage between runs.
func DefaultRegistry() *Registry {
	r, err := New(defaultDefinitions()...)
	if err != nil {
		panic(err)
	}
	return r
}

// Definitions returns definitions in stable ID order.
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	out := make([]Definition, 0, len(r.ids))
	for _, id := range r.ids {
		out = append(out, r.byID[id])
	}
	return out
}

// Get returns one definition by stable ID.
func (r *Registry) Get(id ID) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.byID[id]
	return definition, ok
}

// Availability evaluates a definition's entity and state requirements.
func (r *Registry) Availability(deps Dependencies, id ID, request Request) Availability {
	definition, ok := r.Get(id)
	if !ok {
		return Availability{Reason: fmt.Sprintf("unknown action %q", id)}
	}
	if err := validateEntities(definition, request); err != nil {
		return Availability{Reason: err.Error()}
	}
	if definition.available == nil {
		return Availability{Available: true}
	}
	return definition.available(deps, request)
}

// Execute validates a request, enforces non-interactive confirmation, and
// invokes the action handler.  It never asks a question or writes output.
func (r *Registry) Execute(ctx context.Context, deps Dependencies, id ID, request Request) (Result, error) {
	definition, ok := r.Get(id)
	if !ok {
		return Result{}, &Error{Code: "unknown_action", Action: id, Message: fmt.Sprintf("unknown action %q", id)}
	}
	if err := validateEntities(definition, request); err != nil {
		return Result{}, err
	}
	if err := validateParameters(definition, request); err != nil {
		return Result{}, err
	}
	availability := r.Availability(deps, id, request)
	if !availability.Available {
		message := availability.Reason
		if message == "" {
			message = fmt.Sprintf("action %q is unavailable", id)
		}
		return Result{}, &Error{Code: "unavailable", Action: id, Message: message}
	}
	if definition.Confirmation.Required && !request.Confirmed {
		message := definition.Confirmation.Prompt
		if message == "" {
			message = fmt.Sprintf("action %q requires confirmation", id)
		}
		return Result{}, &Error{Code: "confirmation_required", Action: id, Message: message}
	}
	result, err := definition.execute(ctx, deps, request)
	result.Action = id
	if err != nil {
		return result, err
	}
	return result, nil
}

func validateEntities(definition Definition, request Request) error {
	for _, requirement := range definition.Requires {
		switch requirement.Kind {
		case EntityRunningSession:
			if request.Session == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "a running session is required"}
			}
		case EntityProject:
			if request.Project == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "a configured project is required"}
			}
		case EntityConversation:
			if request.Conversation == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "an indexed conversation is required"}
			}
		default:
			return &Error{Code: "invalid_action", Action: definition.ID, Message: fmt.Sprintf("unknown entity requirement %q", requirement.Kind)}
		}
	}
	return nil
}

func validateParameters(definition Definition, request Request) error {
	missing := make([]ParameterSchema, 0)
	for _, parameter := range definition.Parameters {
		if parameter.Required && strings.TrimSpace(request.Parameters[parameter.Name]) == "" {
			missing = append(missing, parameter)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &Error{
		Code:              "missing_parameters",
		Action:            definition.ID,
		Message:           fmt.Sprintf("action %q requires additional parameters", definition.ID),
		MissingParameters: missing,
	}
}

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

func accountByName(cfg *config.Config, profile string) (config.Account, bool) {
	if cfg == nil {
		return config.Account{}, false
	}
	accounts := cfg.Accounts
	if len(accounts) == 0 {
		accounts = cfg.Profiles
	}
	for _, account := range accounts {
		if strings.EqualFold(account.Name, profile) {
			return account, true
		}
	}
	return config.Account{}, false
}

func requireProjectAccount(id ID, deps Dependencies, project config.Session) (config.Account, error) {
	if deps.Kitty == nil || deps.Config == nil {
		return config.Account{}, dependencyError(id, "Kitty client and configuration")
	}
	account, ok := accountByName(deps.Config, project.EffectiveProfile())
	if !ok {
		return config.Account{}, &Error{Code: "not_found", Action: id, Message: fmt.Sprintf("no matching profile %q", project.EffectiveProfile())}
	}
	return account, nil
}

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
		Config: deps.Config, ProjectLabel: meta.Project, SourceCWD: meta.CWD, SessionID: identity.SessionID, AllowDirty: allowDirty,
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
		"base_ref": isolated.BaseRef, "source_root": isolated.SourceRoot,
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

func parameterBool(parameters map[string]string, name string) bool {
	value := strings.TrimSpace(parameters[name])
	return strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
}

func conversationIdentity(meta transcript.SessionMeta, action ID) (conversation.Identity, error) {
	identity, ok := conversation.New(meta.Harness, meta.ID)
	if !ok {
		return conversation.Identity{}, &Error{Code: "invalid_conversation", Action: action, Message: "indexed conversation must include a harness and session ID"}
	}
	return identity, nil
}

// continuityLaunchTarget validates the immutable source fields Agent Mux needs
// to resume safely, then derives a configured launch template without changing
// the source CWD/profile/harness. A matching configured project carries its
// explicit args and environment forward only when it is uniquely identified.
func continuityLaunchTarget(deps Dependencies, meta transcript.SessionMeta, identity conversation.Identity, action ID) (config.Session, config.Account, error) {
	if err := validateIndexedSource(meta, identity, deps.Readers); err != nil {
		return config.Session{}, config.Account{}, &Error{Code: "source_unavailable", Action: action, Message: err.Error()}
	}
	profile := strings.TrimSpace(meta.DisplayProfile())
	if profile == "" {
		return config.Session{}, config.Account{}, &Error{Code: "invalid_conversation", Action: action, Message: "indexed conversation has no originating profile"}
	}
	account, ok := accountByName(deps.Config, profile)
	if !ok {
		return config.Session{}, config.Account{}, &Error{Code: "not_found", Action: action, Message: fmt.Sprintf("no matching profile %q", profile)}
	}
	if !strings.EqualFold(account.EffectiveHarness(), identity.Harness) {
		return config.Session{}, config.Account{}, &Error{
			Code:    actionCodeProfileHarnessMismatch,
			Action:  action,
			Message: fmt.Sprintf("profile %q now uses %q, but the indexed conversation belongs to %q", account.Name, account.EffectiveHarness(), identity.Harness),
		}
	}
	return continuityProject(deps.Config, meta, account), account, nil
}

const actionCodeProfileHarnessMismatch = "profile_harness_mismatch"

func validateIndexedSource(meta transcript.SessionMeta, identity conversation.Identity, readers map[string]transcript.Reader) error {
	if strings.TrimSpace(meta.Path) == "" {
		return errors.New("indexed conversation has no transcript source path")
	}
	if strings.TrimSpace(meta.CWD) == "" {
		return errors.New("indexed conversation has no source working directory")
	}
	reader, ok := readers[identity.Harness]
	if !ok || reader == nil {
		return fmt.Errorf("no transcript reader is registered for harness %q", identity.Harness)
	}
	if !strings.EqualFold(reader.Harness(), identity.Harness) {
		return fmt.Errorf("transcript reader %q cannot validate harness %q", reader.Harness(), identity.Harness)
	}
	info, err := os.Stat(meta.Path)
	if err != nil {
		return fmt.Errorf("reading indexed transcript source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("indexed transcript source %q is not a regular file", meta.Path)
	}
	if _, err := reader.ReadMessages(meta.Path); err != nil {
		return fmt.Errorf("validating indexed transcript source: %w", err)
	}
	return nil
}

func continuityProject(cfg *config.Config, meta transcript.SessionMeta, account config.Account) config.Session {
	var candidates []config.Session
	for _, project := range cfg.Projects {
		if !strings.EqualFold(project.EffectiveProfile(), account.Name) || !sameSourceDirectory(project.Directory, meta.CWD) {
			continue
		}
		if meta.Project != "" && strings.EqualFold(project.Title, meta.Project) {
			project.Directory = meta.CWD
			project.Profile, project.Account = account.Name, account.Name
			return project
		}
		candidates = append(candidates, project)
	}
	if len(candidates) == 1 {
		project := candidates[0]
		project.Directory = meta.CWD
		project.Profile, project.Account = account.Name, account.Name
		return project
	}
	title := strings.TrimSpace(meta.Project)
	if title == "" {
		title = meta.ID
	}
	return config.Session{Title: title, Directory: meta.CWD, Profile: account.Name, Account: account.Name}
}

func sameSourceDirectory(left, right string) bool {
	left = filepath.Clean(config.ExpandTilde(left))
	right = filepath.Clean(config.ExpandTilde(right))
	return left != "." && left == right
}

func continuityLaunchError(action ID, err error) error {
	if errors.Is(err, harness.ErrUnsupportedInvocation) {
		return &Error{Code: "capability_unavailable", Action: action, Message: err.Error()}
	}
	return &Error{Code: "launch_failed", Action: action, Message: err.Error()}
}
