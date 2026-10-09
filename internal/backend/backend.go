// Package backend defines the narrow terminal-backend seams AgentMux domain
// services use: discovery, launch, termination, capture, metadata, and
// presentation. A backend (Kitty today, a private tmux server later) owns its
// own addressing grammar and topology; domain code only ever holds an opaque
// Ref and asks for an operation by intent.
//
// Every operation is synchronous and reports exactly what the backend did.
// Operations a backend cannot perform return an *UnsupportedError (matched by
// errors.Is(err, ErrUnsupported)) and are declared up front in Capabilities,
// so callers can hide an action instead of discovering the gap by failing.
package backend

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Scope says which level of a backend's topology a Ref addresses.
type Scope string

const (
	// ScopeGroup is the presentation unit that holds one agent: a Kitty tab,
	// a tmux session. Titles, colors, ordering, focus, and termination act on
	// groups.
	ScopeGroup Scope = "group"
	// ScopePane is the terminal surface an agent process runs in: a Kitty
	// window, a tmux pane. Metadata and capture act on panes.
	ScopePane Scope = "pane"
)

// Ref addresses one group or pane inside a backend. ID is the backend's own
// identifier kept as an opaque string; only the adapter that produced it may
// interpret it.
type Ref struct {
	Scope Scope  `json:"scope"`
	ID    string `json:"id"`
}

// GroupID returns a group reference.
func GroupID(id string) Ref { return Ref{Scope: ScopeGroup, ID: id} }

// PaneID returns a pane reference.
func PaneID(id string) Ref { return Ref{Scope: ScopePane, ID: id} }

// IsZero reports whether r addresses nothing.
func (r Ref) IsZero() bool { return r.Scope == "" && r.ID == "" }

func (r Ref) String() string { return string(r.Scope) + ":" + r.ID }

// Validate rejects a reference with an unknown scope or an empty ID. Adapters
// additionally reject IDs outside their own grammar.
func (r Ref) Validate() error {
	if r.Scope != ScopeGroup && r.Scope != ScopePane {
		return &InvalidRefError{Ref: r, Reason: "unknown scope"}
	}
	if strings.TrimSpace(r.ID) == "" {
		return &InvalidRefError{Ref: r, Reason: "empty id"}
	}
	return nil
}

// Process is one process observed in the foreground of a pane.
type Process struct {
	PID  int
	Argv []string
	CWD  string
}

// Pane is one discovered terminal surface. Group-level facts (title, focus)
// are repeated on every pane of the group so a caller never has to rebuild
// the backend's topology to answer a question about one agent.
type Pane struct {
	Ref   Ref
	Group Ref
	// GroupTitle is the presentation title of the containing group.
	GroupTitle string
	// Title is the pane's own title, as set by the program running in it.
	Title string
	CWD   string
	// Vars is the pane metadata AgentMux wrote with SetMetadata or at launch.
	// An empty value and an absent key mean the same thing: backends that
	// cannot unset a key clear it by writing the empty string. A backend may
	// report only the keys AgentMux defines.
	Vars map[string]string
	// Active is true for the pane that receives input inside its group.
	Active bool
	// GroupActive is true for the selected group of its window or client.
	GroupActive bool
	Foreground  []Process
	// Exit is set once the pane's program has ended and the backend retains
	// the pane (and its screen) until the group is closed. Backends that
	// remove a pane when its program ends never set it.
	Exit *Exit
}

// Exit is how a pane's program ended.
type Exit struct {
	// Status is the exit status of a normal exit, or -1 when the program was
	// killed by a signal or the backend has not collected the status yet.
	Status int
	// Signal names the signal that killed the program, if one did.
	Signal string
}

// Placement says where a launched group goes relative to the current one.
type Placement string

const (
	PlacementDefault Placement = ""
	PlacementFirst   Placement = "first"
	PlacementBefore  Placement = "before"
	PlacementAfter   Placement = "after"
)

// LaunchSpec describes one agent launch: a new group holding one pane.
type LaunchSpec struct {
	Title     string
	Directory string
	Placement Placement
	Env       map[string]string
	// Vars is the initial pane metadata, readable through Discover.
	Vars map[string]string
	Argv []string
}

// ColorReset clears a color back to the backend's configured default. An
// empty color field leaves that color unchanged.
const ColorReset = "reset"

// Colors is a group's presentation color set.
type Colors struct {
	ActiveBg   string
	InactiveBg string
	ActiveFg   string
	InactiveFg string
}

// IsZero reports whether no color would change.
func (c Colors) IsZero() bool {
	return c.ActiveBg == "" && c.InactiveBg == "" && c.ActiveFg == "" && c.InactiveFg == ""
}

// Extent selects how much terminal text Capture returns.
type Extent string

const (
	// ExtentScreen is the visible screen.
	ExtentScreen Extent = "screen"
	// ExtentAll is the visible screen plus scrollback.
	ExtentAll Extent = "all"
)

// Operation names one backend operation for capability reporting.
type Operation string

const (
	OpDiscover Operation = "discover"
	OpLaunch   Operation = "launch"
	OpClose    Operation = "close"
	OpCapture  Operation = "capture"
	OpMetadata Operation = "metadata"
	OpFocus    Operation = "focus"
	OpTitle    Operation = "title"
	OpColors   Operation = "colors"
	OpReorder  Operation = "reorder"
)

// Operations lists every operation in a stable order.
var Operations = []Operation{
	OpDiscover, OpLaunch, OpClose, OpCapture, OpMetadata,
	OpFocus, OpTitle, OpColors, OpReorder,
}

// Capabilities is the set of operations a backend performs. An operation not
// in the set returns ErrUnsupported without side effects.
type Capabilities map[Operation]bool

// Supports reports whether op is available.
func (c Capabilities) Supports(op Operation) bool { return c[op] }

// Discoverer lists the panes a backend currently hosts.
type Discoverer interface {
	Discover() ([]Pane, error)
}

// Launcher starts an agent in a new group.
type Launcher interface {
	Launch(spec LaunchSpec) error
}

// Launched is the exact target one launch created, so a runtime session can
// be committed without searching for it.
type Launched struct {
	Group    Ref
	Pane     Ref
	PID      int
	Instance runtimesession.BackendInstance
}

// TargetLauncher is a backend that reports the target each launch created.
// Backends that can only report success (Kitty) implement Launcher alone.
type TargetLauncher interface {
	Backend
	LaunchTarget(spec LaunchSpec) (Launched, error)
}

// Terminator ends the group addressed by ref. A pane ref ends the group that
// contains it, since AgentMux runs one agent per group.
type Terminator interface {
	Close(ref Ref) error
}

// Capturer reads terminal text from a pane.
type Capturer interface {
	Capture(ref Ref, extent Extent) (string, error)
}

// MetadataWriter writes pane metadata. An empty map is a no-op; an empty value
// clears that key.
type MetadataWriter interface {
	SetMetadata(ref Ref, vars map[string]string) error
}

// Presenter performs the presentation operations a backend supports. Focus
// accepts group and pane refs; the others act on the group a ref names or
// contains.
type Presenter interface {
	Focus(ref Ref) error
	SetTitle(ref Ref, title string) error
	SetColors(ref Ref, colors Colors) error
	// Move slides a group delta positions in its window's ordering; positive
	// moves toward the end. Zero is a no-op.
	Move(ref Ref, delta int) error
}

// Backend is the full seam one terminal backend implements.
type Backend interface {
	Kind() runtimesession.BackendKind
	Capabilities() Capabilities
	Discoverer
	Launcher
	Terminator
	Capturer
	MetadataWriter
	Presenter
}

// ErrUnsupported matches every *UnsupportedError.
var ErrUnsupported = errors.New("backend: operation not supported")

// ErrInvalidRef matches every *InvalidRefError.
var ErrInvalidRef = errors.New("backend: invalid reference")

// UnsupportedError reports an operation the backend does not perform.
type UnsupportedError struct {
	Kind      runtimesession.BackendKind
	Operation Operation
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("backend %s: %s not supported", e.Kind, e.Operation)
}

func (e *UnsupportedError) Is(target error) bool { return target == ErrUnsupported }

// InvalidRefError reports a reference the backend refuses before acting.
type InvalidRefError struct {
	Ref    Ref
	Reason string
}

func (e *InvalidRefError) Error() string {
	return fmt.Sprintf("backend: invalid reference %q: %s", e.Ref.String(), e.Reason)
}

func (e *InvalidRefError) Is(target error) bool { return target == ErrInvalidRef }

// FindPane returns the discovered pane with ref, or false.
func FindPane(panes []Pane, ref Ref) (Pane, bool) {
	for _, p := range panes {
		if p.Ref == ref {
			return p, true
		}
	}
	return Pane{}, false
}

// GroupPresent reports whether any discovered pane belongs to group.
func GroupPresent(panes []Pane, group Ref) bool {
	for _, p := range panes {
		if p.Group == group {
			return true
		}
	}
	return false
}
