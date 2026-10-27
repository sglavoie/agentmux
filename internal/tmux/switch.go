package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// In a plain terminal, going to another session is something done to the
// terminal that is attached: its client is switched to the other session. The
// server still has no view of its own, so this is not Focus; it names the
// client it moves. Switching follows the rule attaching does: the terminal
// that arrives is the session's one owner, and it never arrives at an agent
// that is not running.

// The reasons a client is not switched.
var (
	// ErrNoClient reports that no attached terminal has the name given.
	ErrNoClient = errors.New("tmux: no such client")
	// ErrPaneGone reports a target pane the server does not hold.
	ErrPaneGone = errors.New("tmux: pane is gone")
	// ErrPaneExited reports a target pane whose program ended.
	ErrPaneExited = errors.New("tmux: the program in the pane exited")
)

// SwitchClient moves the terminal called client to the session holding
// target, and detaches every other terminal of that session in the same
// invocation, so the session never has two owners. The session it leaves
// keeps running with nobody attached. A terminal already on the session stays
// there and only loses its competitors.
//
// A target that is gone or whose program exited is refused, and so is one
// that stops being there between the check and the move: the move itself is
// guarded by the pane being alive, and where the client ended up is read back
// in the same invocation.
//
// The session's status line is turned on, as `amx attach` does for a plain
// terminal: only a plain terminal is ever switched.
func (b *Backend) SwitchClient(client string, target backend.Ref) error {
	if err := paneOnly(target); err != nil {
		return err
	}
	if err := clientName(client); err != nil {
		return err
	}
	survey, err := b.Survey()
	if err != nil {
		return err
	}
	if !survey.Running {
		return fmt.Errorf("%w: %s", ErrPaneGone, target.ID)
	}
	return b.switchSurveyed(survey, client, target)
}

func (b *Backend) switchSurveyed(survey Survey, client string, target backend.Ref) error {
	pane, ok := backend.FindPane(survey.Panes, target)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPaneGone, target.ID)
	}
	if pane.Exit != nil {
		return fmt.Errorf("%w: %s", ErrPaneExited, target.ID)
	}
	moving, ok := survey.Client(client)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoClient, client)
	}
	if groupID(pane.Group) != nil {
		return fmt.Errorf("tmux: pane %s is in no session", target.ID)
	}
	if moving.Group == pane.Group {
		for _, other := range survey.ClientsOf(target) {
			if other.Name == client {
				continue
			}
			if err := b.DetachClient(other); err != nil {
				return err
			}
		}
		return nil
	}
	alive := "#{&&:#{==:#{pane_id}," + target.ID + "},#{==:#{pane_dead},0}}"
	move := strings.Join([]string{
		"detach-client -s " + quoteArg(pane.Group.ID),
		"switch-client -c " + quoteArg(client) + " -t " + quoteArg(target.ID),
		"set-option -t " + quoteArg(target.ID) + " status on",
	}, " ; ")
	// The read back names the client with -t: -c only says which client a
	// message would go to, and the format is then resolved in the session the
	// server used last, which may be the target even when the move was skipped.
	out, err := b.server.runChain([][]string{
		{"if-shell", "-F", "-t", target.ID, alive, move},
		{"display-message", "-p", "-t", client, "#{pane_id}"},
	})
	if err != nil {
		var cmdErr *CommandError
		if errors.As(err, &cmdErr) && strings.Contains(cmdErr.Stderr, "can't find client") {
			return fmt.Errorf("%w: %s", ErrNoClient, client)
		}
		// The target went away after the survey. tmux 3.8 resolves the
		// guard's missing pane to another, which the guard then refuses, but
		// a tmux that reports it instead says the same thing.
		if errors.As(err, &cmdErr) && strings.Contains(cmdErr.Stderr, "can't find pane") {
			return fmt.Errorf("%w: %s", ErrPaneGone, target.ID)
		}
		return err
	}
	if at := strings.TrimSpace(out); at != target.ID {
		return fmt.Errorf("%w: %s (the client is on %q)", ErrPaneGone, target.ID, at)
	}
	return nil
}

// PopupCommand is the client that opens a popup over what the terminal called
// client shows and runs argv in it, with env added to the session's
// environment. The popup closes when the program ends. The command runs for
// as long as the popup is open, so the caller starts it and need not wait; a
// terminal that already shows a popup keeps it, and the command ends at once.
//
// A popup is a view of the terminal it is on. It is not a session: it has no
// pane, the server lists it nowhere, and nothing counts it.
func (b *Backend) PopupCommand(client string, env map[string]string, argv []string) (*exec.Cmd, error) {
	if err := clientName(client); err != nil {
		return nil, err
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("tmux: a popup needs a program")
	}
	args := []string{"-S", b.server.socket, "-f", b.server.config, "display-popup", "-c", client, "-E", "-w", "100%", "-h", "100%"}
	for key, value := range env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("tmux: invalid environment variable %q", key)
		}
		args = append(args, "-e", key+"="+value)
	}
	args = append(args, "--")
	for _, a := range paneArgv(argv) {
		args = append(args, escapeArg(a))
	}
	// #nosec G204 -- the binary was resolved at NewServer and the client name is validated.
	c := exec.Command(b.server.binary, args...)
	c.Env = clientEnv(os.Environ())
	return c, nil
}

// NotLiveError reports a switch refused because the session's agent is not
// running. Record is the session as the registry holds it afterwards: stale,
// with why.
type NotLiveError struct {
	Record runtimesession.Record
}

func (e *NotLiveError) Error() string {
	return fmt.Sprintf("runtime session %s is not running (%s)", e.Record.ID, e.Record.Outcome())
}

// Switcher moves attached terminals between the registered sessions of one
// server, with the validation and the ownership record `amx attach` keeps.
type Switcher struct {
	Store   *runtimesession.Store
	Backend *Backend
	// Fingerprint reads a process's start token. Nil trusts the server's own
	// PID for the pane, as Survey.Observation explains.
	Fingerprint func(pid int) (runtimesession.ProcessFingerprint, error)
	// Now is the clock. Nil is time.Now.
	Now func() time.Time
}

// Viewer returns the terminal attached to the session of runtime session id:
// its one owner, or when a competitor has not been detached yet, the one that
// attached last.
func (s Switcher) Viewer(id runtimesession.ID) (Client, error) {
	registry, err := s.Store.Load()
	if err != nil {
		return Client{}, err
	}
	record, err := registry.Get(id)
	if err != nil || record.Target == nil {
		return Client{}, fmt.Errorf("no runtime session %s", id)
	}
	survey, err := s.Backend.Survey()
	if err != nil {
		return Client{}, err
	}
	viewer, ok := Survey{Clients: survey.ClientsOf(recordPane(record))}.owner()
	if !ok {
		return Client{}, fmt.Errorf("%w: no terminal is attached to %s", ErrNoClient, id)
	}
	return viewer, nil
}

// The owner the registry records for a session is named for what attached it.
// An `amx attach` the desktop workspace runs in one of its own terminals
// records a hosted one, and whatever takes the session next (an `amx attach`
// in a plain terminal, a client switched to it) replaces that record. So the
// record, not anything in the agent's environment, says whether the terminal
// showing a session right now is the workspace's.
const (
	attachmentPrefix       = "amx-attach-"
	hostedAttachmentPrefix = "amx-workspace-"
)

// AttachmentID names the ownership an `amx attach` records, from a token of
// its own. hosted is whether the desktop workspace runs that attach
// (WorkspaceHostEnv).
func AttachmentID(hosted bool, token string) string {
	if hosted {
		return hostedAttachmentPrefix + token
	}
	return attachmentPrefix + token
}

// Hosted reports whether the terminal that owns runtime session id is one the
// desktop workspace hosts. The workspace keeps its own account of which
// session each of its terminals shows, so such a terminal is never switched:
// the workspace is asked to show the other session instead. A session nobody
// owns, or one a plain terminal took, is not hosted.
func (s Switcher) Hosted(id runtimesession.ID) (bool, error) {
	registry, err := s.Store.Load()
	if err != nil {
		return false, err
	}
	record, err := registry.Get(id)
	if err != nil || record.State != runtimesession.StateLive || record.Attachment == nil {
		return false, nil
	}
	return strings.HasPrefix(record.Attachment.ID, hostedAttachmentPrefix), nil
}

// LastActivePlain returns the terminal the operator used last on survey's
// server among those the desktop workspace does not host. A terminal shows a
// hosted session when the registry records a hosted owner for the live
// session on its pane, and such a terminal is the workspace's to move, as
// Hosted explains; switching it would also record a plain owner over the
// workspace's. With no other terminal attached it returns ErrNoClient.
func (s Switcher) LastActivePlain(survey Survey) (Client, error) {
	registry, err := s.Store.Load()
	if err != nil {
		return Client{}, err
	}
	hosted := map[string]bool{}
	for _, record := range registry.List() {
		if record.State != runtimesession.StateLive || record.Target == nil || record.Attachment == nil ||
			record.Target.Instance.Kind != runtimesession.BackendTmux || record.Target.Instance.Address != s.Backend.server.socket ||
			!strings.HasPrefix(record.Attachment.ID, hostedAttachmentPrefix) {
			continue
		}
		for _, client := range survey.ClientsOf(recordPane(record)) {
			hosted[client.Name] = true
		}
	}
	var plain Survey
	for _, client := range survey.Clients {
		if !hosted[client.Name] {
			plain.Clients = append(plain.Clients, client)
		}
	}
	client, ok := plain.LastActive()
	if !ok {
		return Client{}, ErrNoClient
	}
	return client, nil
}

// owner is the client that attached last, by the same order Competing keeps
// its owner by.
func (s Survey) owner() (Client, bool) {
	var last Client
	found := false
	for _, client := range s.Clients {
		if !found || attachedAfter(client, last) {
			last, found = client, true
		}
	}
	return last, found
}

// Switch moves the terminal called client to the session of runtime session
// id. The registered pane must still hold the registered process on the same
// server generation; when it does not, the session is marked stale and the
// switch is refused with a NotLiveError, so a terminal never lands on an
// exited or lost agent or on a pane ID a new server reused.
//
// The registry follows the terminal: the session it arrives at records a new
// owner, and the one it left gives up the owner it had, since that owner's
// terminal is now elsewhere. An owner recorded here has no process waiting to
// release it; Reconcile does that once no terminal is attached.
func (s Switcher) Switch(client string, id runtimesession.ID) error {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	registry, err := s.Store.Load()
	if err != nil {
		return err
	}
	record, err := registry.Get(id)
	if err != nil {
		return fmt.Errorf("no runtime session %s", id)
	}
	if record.State != runtimesession.StateLive || record.Target == nil {
		return &NotLiveError{Record: record}
	}
	if record.Target.Instance.Kind != runtimesession.BackendTmux || record.Target.Instance.Address != s.Backend.server.socket {
		return fmt.Errorf("runtime session %s does not run on the tmux server at %s", id, s.Backend.server.socket)
	}
	// The survey decides whether the session is still there, so a server
	// that turned one connection away is not taken for one that is gone.
	survey, err := s.Backend.ConfirmedSurvey()
	if err != nil {
		return err
	}
	observation := survey.Observation(record, s.Fingerprint)
	current := record
	if err := s.Store.Update(func(registry *runtimesession.Registry) error {
		validated, err := registry.Validate(id, observation, now())
		if err == nil || errors.Is(err, runtimesession.ErrStaleTarget) || errors.Is(err, runtimesession.ErrInvalidState) {
			current = validated
			return nil
		}
		return err
	}); err != nil {
		return err
	}
	if current.State != runtimesession.StateLive {
		return &NotLiveError{Record: current}
	}
	moving, ok := survey.Client(client)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoClient, client)
	}
	target := recordPane(record)
	// The session the terminal leaves is whichever live record names the pane
	// it was on.
	var left runtimesession.ID
	if from, ok := paneOfGroup(survey.Panes, moving.Group); ok && from.Ref != target {
		for _, other := range registry.List() {
			if other.State == runtimesession.StateLive && other.Target != nil &&
				other.Target.Instance == record.Target.Instance && other.Target.Ref == from.Ref.ID {
				left = other.ID
			}
		}
	}
	if err := s.Backend.switchSurveyed(survey, client, target); err != nil {
		return err
	}
	if from, ok := paneOfGroup(survey.Panes, moving.Group); ok && from.Ref == target {
		// The terminal was already there: whoever recorded it as the owner
		// still is.
		return nil
	}
	owner, err := switchAttachment(client)
	if err != nil {
		return err
	}
	return s.Store.Update(func(registry *runtimesession.Registry) error {
		if left != "" {
			if previous, err := registry.Get(left); err == nil && previous.Attachment != nil {
				_, _ = registry.Detach(left, previous.Attachment.ID, now())
			}
		}
		if _, err := registry.Attach(id, owner, now()); err != nil && !errors.Is(err, runtimesession.ErrInvalidState) && !errors.Is(err, runtimesession.ErrNotFound) {
			return err
		}
		return nil
	})
}

func paneOfGroup(panes []backend.Pane, group backend.Ref) (backend.Pane, bool) {
	for _, pane := range panes {
		if pane.Group == group {
			return pane, true
		}
	}
	return backend.Pane{}, false
}

func switchAttachment(client string) (runtimesession.Attachment, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return runtimesession.Attachment{}, fmt.Errorf("naming attachment: %w", err)
	}
	return runtimesession.Attachment{ID: "amx-switch-" + hex.EncodeToString(b[:]), Client: "tmux client " + client}, nil
}
