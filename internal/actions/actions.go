// Package actions defines the application actions shared by interactive and
// command-line entry points.  It deliberately contains no Bubble Tea types:
// callers supply the current entity and the dependencies needed to perform an
// action, then decide how to present results, confirmations, and errors.
package actions

import (
	"context"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
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
