// Package actions is the typed action registry shared by the CLI and the TUI,
// so a command-line entry point and an interactive one run the same action
// definition. It deliberately contains no Bubble Tea types: callers supply the
// current entity and the dependencies needed to perform an action, then decide
// how to present results, confirmations, and errors.
package actions

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// ID is a stable action identifier shared by the TUI and CLI rather than a
// presentation label.
type ID string

const (
	Focus          ID = "session.focus"
	Launch         ID = "session.launch"
	Continue       ID = "session.continue"
	Fork           ID = "session.fork"
	ForkWorktree   ID = "session.fork_worktree"
	Handoff        ID = "session.handoff"
	OpenDirectory  ID = "project.open_directory"
	OpenEditor     ID = "project.open_editor"
	CopyTitle      ID = "session.copy_title"
	ClearStatus    ID = "session.clear_status"
	MarkActive     ID = "session.mark_active"
	ToggleLock     ID = "session.toggle_lock"
	ToggleMute     ID = "session.toggle_mute"
	Close          ID = "session.close"
	Restart        ID = "session.restart"
	WorktreeList   ID = "worktree.list"
	WorktreeShow   ID = "worktree.show"
	WorktreeDiff   ID = "worktree.diff"
	WorktreeLand   ID = "worktree.land"
	WorktreeReopen ID = "worktree.reopen"
	WorktreeShell  ID = "worktree.shell"
	WorktreeEditor ID = "worktree.editor"
	WorktreeRemove ID = "worktree.remove"
	WorktreeForget ID = "worktree.forget"
	PlanRun        ID = "plan.run"
)

// EntityKind identifies a typed target accepted by an action.
type EntityKind string

const (
	EntityRunningSession EntityKind = "running_session"
	EntityProject        EntityKind = "project"
	EntityConversation   EntityKind = "conversation"
	EntityWorktree       EntityKind = "worktree"
	EntityPlan           EntityKind = "plan"
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
	// Positional marks a parameter given as a command argument rather than a
	// flag, so text-mode help does not tell the caller to pass a --flag that
	// does not exist.
	Positional bool `json:"positional,omitempty"`
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
//
// Terminal operations on a live session (focus, title, metadata, close,
// launch) go through Terminal. Kitty remains the topology source for the
// session-package helpers that still read Kitty's window tree, and when
// Terminal is nil the Kitty adapter over it serves as the terminal.
//
// New agents launch on the backend Backend selects: Kitty by default, or the
// private tmux server in Tmux. Actions on an existing session always use the
// backend that session records, whatever Backend says.
type Dependencies struct {
	Kitty    kitty.KittyClient
	Terminal backend.Backend
	// Backend selects where launches go. Empty means Kitty.
	Backend runtimesession.BackendKind
	// Tmux is the private tmux server, required to launch on tmux or to act
	// on a tmux-hosted session.
	Tmux backend.TargetLauncher
	// Runtimes is the runtime-session registry; nil means the default file.
	Runtimes        *runtimesession.Store
	Config          *config.Config
	Clipboard       func(string) error
	Daemon          func(protocol.Request) (protocol.Response, error)
	Readers         map[string]transcript.Reader
	Handoff         handoff.Runner
	Worktree        worktree.Runner
	WorktreeManager WorktreeLifecycle
	LiveSessions    func(*config.Config) ([]session.RunningSession, error)
	// Launched, when set, is told about every launch or dedup that ended on a
	// registered runtime session, so a surface can show the agent where its
	// host shows agents. It is a notification: it cannot fail the action.
	Launched func(session.LaunchResult)
	// Utility, when set, shows a shell or an editor where the surface's host
	// shows such views: the desktop workspace. It is asked only when there is
	// no Kitty to open a tab in, and an error means the host did not take the
	// view, which is then left to the caller (DataContinuationHere).
	Utility         func(UtilityView) error
	ActiveLoops     func() ([]agentloop.Candidate, error)
	TranscriptIndex func() (*transcript.Index, error)
	// ASL answers whether a plan may be started and how a supervising agent
	// starts it; nil means the configured agent-session-loop command.
	ASL planrun.ASL
}

// WorktreeLifecycle is the Git-authoritative inventory and cleanup boundary
// used by shared lifecycle actions. The production implementation is
// worktree.Manager; tests and alternate frontends may inject a narrow fake.
type WorktreeLifecycle interface {
	List(context.Context) ([]worktree.Entry, error)
	Resolve(context.Context, string) (worktree.Entry, error)
	Remove(context.Context, string, worktree.RemoveOptions) (worktree.MutationResult, error)
	Forget(context.Context, string) (worktree.MutationResult, error)
	Review(context.Context, worktree.Entry, worktree.ReviewOptions) (worktree.Review, error)
	Land(context.Context, string) (worktree.LandResult, error)
}

// Request holds the typed targets and plain parameters supplied to one action.
// Project is a configured launch template, while Session is an already-live
// Kitty-backed session. Plan is a split plan directory.
type Request struct {
	Session      *session.RunningSession
	Project      *config.Session
	Conversation *transcript.SessionMeta
	Worktree     *worktree.Entry
	Plan         *plans.PlanMeta
	Selector     string
	Parameters   map[string]string
	Confirmed    bool
}

// UtilityView is a shell in a directory or an editor on a path, opened beside
// the agents. It is never a session, and it names a place, never a command
// line: whoever shows it decides what runs.
type UtilityView struct {
	// Editor asks for the configured editor on Path instead of a shell in it.
	Editor bool
	// Path is absolute.
	Path string
}

// The continuations an open reports when there was no Kitty tab to open.
const (
	// DataContinuationWorkspace: the desktop workspace took the view.
	DataContinuationWorkspace = "workspace"
	// DataContinuationHere: nothing was opened. The caller runs the shell or
	// the editor in its own terminal, since an action starts no program
	// there itself.
	DataContinuationHere = "here"
)

// UtilityHere reads off an open's result the view it left to the caller's own
// terminal.
func UtilityHere(result Result) (UtilityView, bool) {
	if continuation, _ := result.Data["continuation"].(string); continuation != DataContinuationHere {
		return UtilityView{}, false
	}
	path, _ := result.Data["path"].(string)
	editor, _ := result.Data["editor"].(bool)
	return UtilityView{Editor: editor, Path: path}, path != ""
}

// openUtility opens a shell or an editor with no Kitty tab to put it in: in
// the host's own view when there is one, and otherwise by handing the view
// back to the caller.
func (d Dependencies) openUtility(id ID, editor bool, path string) (Result, error) {
	abs, err := filepath.Abs(config.ExpandTilde(path))
	if err != nil {
		return Result{}, &Error{Code: "launch_failed", Action: id, Message: err.Error()}
	}
	view := UtilityView{Editor: editor, Path: abs}
	noun := "shell"
	if editor {
		noun = "editor"
	}
	data := map[string]any{"path": abs, "editor": editor}
	if d.Utility != nil && d.Utility(view) == nil {
		data["continuation"] = DataContinuationWorkspace
		return Result{Changed: true, Message: "opened " + noun + " in the workspace", Data: data}, nil
	}
	data["continuation"] = DataContinuationHere
	return Result{Message: noun + " for " + abs + " runs in this terminal", Data: data}, nil
}

// DataCloseConfirmed is the Close result's Data key: whether the tab was
// observed to disappear, as opposed to the close merely having been accepted.
// It is carried as data rather than folded into Changed because both endings
// changed something — the request was sent either way — and a caller that offers
// the operator a recovery on the strength of the close needs the narrower fact.
const DataCloseConfirmed = "close_confirmed"

// CloseConfirmed reads DataCloseConfirmed off a Close result. A result from any
// other action, or one predating the key, reads as unconfirmed, which is the
// fail-closed direction: a caller learns nothing it can advertise.
func CloseConfirmed(result Result) bool {
	confirmed, _ := result.Data[DataCloseConfirmed].(bool)
	return confirmed
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
	Candidates        []Candidate       `json:"candidates,omitempty"`
	// Cause is the domain error the action mapped, when there is one, so a
	// presenter can recognize it with errors.As instead of parsing Message.
	Cause error `json:"-"`
}

// Candidate is a presentation-neutral disambiguation choice. CLI callers map
// it to the common schema-v1 Candidate shape without parsing error strings.
type Candidate struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	CanonicalPath string `json:"canonical_path,omitempty"`
	// Problem says why choosing this candidate would still fail, such as a
	// project whose directory is missing. It is offered anyway when it is the
	// likely meaning of what was typed, so the caller learns what to fix
	// rather than seeing the project vanish from the list.
	Problem string `json:"problem,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Cause }

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

// terminal returns the backend for terminal operations, or nil when neither
// Terminal nor Kitty is injected.
func (d Dependencies) terminal() backend.Backend {
	if d.Terminal != nil {
		return d.Terminal
	}
	if d.Kitty == nil {
		return nil
	}
	return kitty.NewBackend(d.Kitty)
}

// launchRuntime is the backend new agents launch on.
func (d Dependencies) launchRuntime(id ID) (session.Runtime, error) {
	switch d.Backend {
	case "", runtimesession.BackendKitty:
		if d.Kitty == nil || d.Config == nil {
			return session.Runtime{}, dependencyError(id, "Kitty client and configuration")
		}
		return session.Runtime{Kitty: d.Kitty, Registry: d.Runtimes}, nil
	case runtimesession.BackendTmux:
		if d.Tmux == nil || d.Config == nil {
			return session.Runtime{}, dependencyError(id, "tmux server and configuration")
		}
		return session.Runtime{Kitty: d.Kitty, Tmux: d.Tmux, Registry: d.Runtimes}, nil
	default:
		return session.Runtime{}, &Error{Code: "invalid_backend", Action: id, Message: fmt.Sprintf("unknown session backend %q (kitty or tmux)", d.Backend)}
	}
}

// runtimeFor is the backend hosting running.
func (d Dependencies) runtimeFor(id ID, running session.RunningSession) (session.Runtime, error) {
	if running.BackendKind() == runtimesession.BackendTmux {
		if d.Tmux == nil || d.Config == nil {
			return session.Runtime{}, dependencyError(id, "tmux server and configuration")
		}
		return session.Runtime{Kitty: d.Kitty, Tmux: d.Tmux, Registry: d.Runtimes}, nil
	}
	if d.Kitty == nil || d.Config == nil {
		return session.Runtime{}, dependencyError(id, "Kitty client and configuration")
	}
	return session.Runtime{Kitty: d.Kitty, Registry: d.Runtimes}, nil
}

// missingTerminal names the terminal an action on running needs and lacks: the
// backend that session runs on, not Kitty whatever it is.
func missingTerminal(id ID, running session.RunningSession) error {
	if running.BackendKind() == runtimesession.BackendTmux {
		return dependencyError(id, "tmux server")
	}
	return dependencyError(id, "Kitty client")
}

// sessionTerminal is the terminal of the backend hosting running, or nil.
func (d Dependencies) sessionTerminal(running session.RunningSession) backend.Backend {
	if running.BackendKind() == runtimesession.BackendTmux {
		if d.Tmux == nil {
			return nil
		}
		return d.Tmux
	}
	return d.terminal()
}

// launchData records where a launch went. Backend is always reported so a
// caller reads the same shape from either backend; the runtime session is
// reported when the launch registered one, and Launched is told about it.
func (d Dependencies) launchData(data map[string]any, launched session.LaunchResult) map[string]any {
	if d.Launched != nil && launched.RuntimeSessionID != "" {
		d.Launched(launched)
	}
	if data == nil {
		data = map[string]any{}
	}
	if launched.Backend != "" {
		data["backend"] = string(launched.Backend)
	}
	if launched.RuntimeSessionID != "" {
		data["runtime_session_id"] = string(launched.RuntimeSessionID)
	}
	return data
}
