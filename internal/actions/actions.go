// Package actions is the typed action registry shared by the CLI and the TUI,
// so a command-line entry point and an interactive one run the same action
// definition. It deliberately contains no Bubble Tea types: callers supply the
// current entity and the dependencies needed to perform an action, then decide
// how to present results, confirmations, and errors.
package actions

import (
	"context"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/kitty"
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
	WorktreeReopen ID = "worktree.reopen"
	WorktreeShell  ID = "worktree.shell"
	WorktreeEditor ID = "worktree.editor"
	WorktreeRemove ID = "worktree.remove"
	WorktreeForget ID = "worktree.forget"
)

// EntityKind identifies a typed target accepted by an action.
type EntityKind string

const (
	EntityRunningSession EntityKind = "running_session"
	EntityProject        EntityKind = "project"
	EntityConversation   EntityKind = "conversation"
	EntityWorktree       EntityKind = "worktree"
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
type Dependencies struct {
	Kitty           kitty.KittyClient
	Terminal        backend.Backend
	Config          *config.Config
	Clipboard       func(string) error
	Daemon          func(protocol.Request) (protocol.Response, error)
	Readers         map[string]transcript.Reader
	Handoff         handoff.Runner
	Worktree        worktree.Runner
	WorktreeManager WorktreeLifecycle
	LiveSessions    func(*config.Config) ([]session.RunningSession, error)
	ActiveLoops     func() ([]agentloop.Candidate, error)
	TranscriptIndex func() (*transcript.Index, error)
}

// WorktreeLifecycle is the Git-authoritative inventory and cleanup boundary
// used by shared lifecycle actions. The production implementation is
// worktree.Manager; tests and alternate frontends may inject a narrow fake.
type WorktreeLifecycle interface {
	List(context.Context) ([]worktree.Entry, error)
	Resolve(context.Context, string) (worktree.Entry, error)
	Remove(context.Context, string, worktree.RemoveOptions) (worktree.MutationResult, error)
	Forget(context.Context, string) (worktree.MutationResult, error)
}

// Request holds the typed targets and plain parameters supplied to one action.
// Project is a configured launch template, while Session is an already-live
// Kitty-backed session.
type Request struct {
	Session      *session.RunningSession
	Project      *config.Session
	Conversation *transcript.SessionMeta
	Worktree     *worktree.Entry
	Selector     string
	Parameters   map[string]string
	Confirmed    bool
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
