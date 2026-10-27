package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// A session has one interactive owner: the one terminal attached to it. That
// terminal's keyboard reaches the agent and its size is the pane's size.
// Reading a pane (Capture, Observe, Survey) attaches nothing, so it can
// neither type nor resize; and when the owner goes away, however it goes, the
// pane keeps the size it last had, because the server resizes a window only
// for a client that is attached to it.

// Client is one terminal attached to the private server.
type Client struct {
	// Name is the client's terminal device, which is how tmux addresses it.
	Name string
	// Group is the session the client views.
	Group backend.Ref
	PID   int
	// Created and Activity are Unix seconds.
	Created  int64
	Activity int64
	Cols     int
	Rows     int
}

// Size is a pane's size in cells.
type Size struct {
	Cols int
	Rows int
}

// Survey is the private server at one moment: its generation, every pane and
// its size, and every attached terminal, read by one client invocation so the
// three agree with each other.
type Survey struct {
	// Instance carries no generation when the server is not running.
	Instance runtimesession.BackendInstance
	Running  bool
	Panes    []backend.Pane
	// Sizes is keyed by pane ID.
	Sizes   map[string]Size
	Clients []Client
}

var clientFields = []string{
	"#{client_name}",
	"#{session_id}",
	"#{client_pid}",
	"#{client_created}",
	"#{client_activity}",
	"#{client_width}",
	"#{client_height}",
}

// The line tags of a survey's output.
const (
	surveyServer = "S"
	surveyPane   = "P"
	surveyClient = "C"
)

// ConfirmedSurvey is Survey for a caller that ends sessions on what it finds,
// and does not take one refused connection for a server that is gone. A
// survey that finds no server ends every session on it, so "not running" is
// believed at once only when the socket is gone too. Over a socket that is
// still there it may be a server too busy to take the connection, so the
// survey is asked once more; a second refusal is believed only when nothing
// listens on the socket, which is what a server that crashed and left its
// socket behind looks like. One that still listens is reported as unreadable,
// and missing evidence ends nothing.
func (b *Backend) ConfirmedSurvey() (Survey, error) {
	survey, err := b.Survey()
	if err != nil || survey.Running {
		return survey, err
	}
	socket := b.server.socket
	if !socketPresent(socket) {
		return survey, nil
	}
	if survey, err = b.Survey(); err != nil || survey.Running {
		return survey, err
	}
	if Listening(socket) {
		return Survey{}, fmt.Errorf("tmux: the server at %s refused a survey twice but still accepts connections", socket)
	}
	return survey, nil
}

// socketPresent reports whether anything is at the socket path. One that
// cannot even be looked at counts as gone, as it would for tmux.
func socketPresent(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Survey reads the server. A server that is not running is not an error: it
// is reported as such, with no panes and no clients. Any other failure is an
// error, so an unreachable server is never mistaken for an absent one.
func (b *Backend) Survey() (Survey, error) {
	survey := Survey{Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: b.server.socket}}
	// The size comes first so the pane's free-text fields stay last.
	paneFormat := strings.Join(append([]string{surveyPane, "#{pane_width}", "#{pane_height}"}, discoverFields...), fieldSep)
	out, err := b.server.runChain([][]string{
		{"display-message", "-p", strings.Join([]string{surveyServer, "#{pid}", "#{start_time}"}, fieldSep)},
		{"list-panes", "-a", "-F", paneFormat},
		{"list-clients", "-F", strings.Join(append([]string{surveyClient}, clientFields...), fieldSep)},
	})
	if errors.Is(err, ErrServerNotRunning) {
		return survey, nil
	}
	if err != nil {
		return Survey{}, err
	}
	survey.Sizes = map[string]Size{}
	// A pane or client line that is not what its tag says is skipped, so one
	// odd pane or terminal never hides the rest of the server.
	starts := func(line string) bool {
		tag, rest, _ := strings.Cut(line, fieldSep)
		switch tag {
		case surveyServer:
			return true
		case surveyPane:
			f := strings.SplitN(rest, fieldSep, 3)
			return len(f) == 3 && isPaneLine(f[2])
		case surveyClient:
			return len(strings.Split(rest, fieldSep)) == len(clientFields)
		}
		return false
	}
	continues := func(line string) bool { return strings.HasPrefix(line, surveyPane+fieldSep) }
	for _, line := range records(out, starts, continues) {
		tag, rest, _ := strings.Cut(line, fieldSep)
		switch tag {
		case surveyServer:
			f := strings.Split(rest, fieldSep)
			if len(f) != 2 || f[0] == "" || f[1] == "" {
				return Survey{}, fmt.Errorf("tmux: unexpected server identity %q", line)
			}
			survey.Instance = b.server.instance(f[0], f[1])
			survey.Running = true
		case surveyPane:
			f := strings.SplitN(rest, fieldSep, 3)
			pane, ok := parsePane(f[2])
			if !ok {
				continue
			}
			cols, _ := strconv.Atoi(f[0])
			rows, _ := strconv.Atoi(f[1])
			survey.Panes = append(survey.Panes, pane)
			survey.Sizes[pane.Ref.ID] = Size{Cols: cols, Rows: rows}
		case surveyClient:
			f := strings.Split(rest, fieldSep)
			client := Client{Name: f[0], Group: backend.GroupID(f[1])}
			client.PID, _ = strconv.Atoi(f[2])
			client.Created, _ = strconv.ParseInt(f[3], 10, 64)
			client.Activity, _ = strconv.ParseInt(f[4], 10, 64)
			client.Cols, _ = strconv.Atoi(f[5])
			client.Rows, _ = strconv.Atoi(f[6])
			survey.Clients = append(survey.Clients, client)
		}
	}
	if !survey.Running {
		return Survey{}, fmt.Errorf("tmux: survey reported no server identity: %q", out)
	}
	return survey, nil
}

// ClientsOf returns the terminals attached to the session holding pane.
func (s Survey) ClientsOf(pane backend.Ref) []Client {
	found, ok := backend.FindPane(s.Panes, pane)
	if !ok {
		return nil
	}
	var clients []Client
	for _, client := range s.Clients {
		if client.Group == found.Group {
			clients = append(clients, client)
		}
	}
	return clients
}

// Observation is what the survey says about a registered tmux session's
// target, in the form the registry validates. A pane that is retained after
// its program ended is reported as exited, with how it ended; a pane or a
// server that is not there is simply absent.
//
// fingerprint reads a process's start token. Without one, a live pane holding
// the registered PID is taken to hold the registered process: the server
// holds each pane's program as its own child for as long as the pane is not
// dead, so within one server generation that PID cannot have been reused. A
// fingerprint that fails to read is treated as no fingerprint, never as no
// process: a slow or failing ps is no evidence that the agent has gone, and a
// session the registry marks stale never comes back.
func (s Survey) Observation(record runtimesession.Record, fingerprint func(pid int) (runtimesession.ProcessFingerprint, error)) runtimesession.Observation {
	observation := runtimesession.Observation{Instance: s.Instance}
	if record.Target == nil || !s.Running {
		return observation
	}
	pane, ok := backend.FindPane(s.Panes, backend.PaneID(record.Target.Ref))
	if !ok {
		return observation
	}
	observation.Present = true
	if pane.Exit != nil {
		observation.Exited = true
		observation.ExitDetail = "status " + strconv.Itoa(pane.Exit.Status)
		if pane.Exit.Signal != "" {
			observation.ExitDetail = "signal " + pane.Exit.Signal
		}
		return observation
	}
	if len(pane.Foreground) == 0 {
		return observation
	}
	pid := pane.Foreground[0].PID
	if fingerprint != nil {
		if process, err := fingerprint(pid); err == nil {
			observation.Process = &process
			return observation
		}
	}
	switch {
	case record.Process != nil && record.Process.PID == pid:
		process := *record.Process
		observation.Process = &process
	default:
		observation.Process = &runtimesession.ProcessFingerprint{PID: pid}
	}
	return observation
}

// DetachClient detaches one terminal from the server. The agent it was
// viewing is untouched. A client that is already gone is not an error.
func (b *Backend) DetachClient(client Client) error {
	if err := clientName(client.Name); err != nil {
		return err
	}
	_, err := b.server.run([]string{"detach-client", "-t", client.Name})
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && strings.Contains(cmdErr.Stderr, "can't find client") {
		return nil
	}
	if errors.Is(err, ErrServerNotRunning) {
		return nil
	}
	return err
}

// clientName accepts a client as tmux names one: its terminal device, with
// nothing in it that a tmux command line or a format would read as syntax.
func clientName(name string) error {
	if !strings.HasPrefix(name, "/dev/") || strings.ContainsAny(name, " \t\n;'\"#{}$\\") {
		return fmt.Errorf("tmux: %q is not a client terminal", name)
	}
	return nil
}

// Client returns the attached terminal called name.
func (s Survey) Client(name string) (Client, bool) {
	for _, client := range s.Clients {
		if client.Name == name {
			return client, true
		}
	}
	return Client{}, false
}

// LastActive returns the terminal the operator used last: the one with the
// latest activity, and among equals the one that attached last.
func (s Survey) LastActive() (Client, bool) {
	var last Client
	found := false
	for _, client := range s.Clients {
		if !found || client.Activity > last.Activity ||
			(client.Activity == last.Activity && (client.Created > last.Created ||
				(client.Created == last.Created && client.Name > last.Name))) {
			last, found = client, true
		}
	}
	return last, found
}

// Competing returns, for every session with more than one terminal attached,
// all of them but the one that attached last: the terminals that would
// otherwise share the keyboard and fight over the size with the owner.
// `amx attach` never leaves any (it detaches the others as it attaches); a
// client started some other way can.
func (s Survey) Competing() []Client {
	byGroup := map[backend.Ref][]Client{}
	for _, client := range s.Clients {
		byGroup[client.Group] = append(byGroup[client.Group], client)
	}
	var competing []Client
	for _, clients := range byGroup {
		if len(clients) < 2 {
			continue
		}
		sort.Slice(clients, func(i, j int) bool { return attachedAfter(clients[i], clients[j]) })
		competing = append(competing, clients[1:]...)
	}
	sort.Slice(competing, func(i, j int) bool { return competing[i].Name < competing[j].Name })
	return competing
}

// attachedAfter orders the terminals of one session by which attached last.
// tmux records attachment to the second, so equals fall to the one used last
// and then to the name. Competing keeps the first and Switcher.Viewer moves
// it; one order keeps them from picking different terminals.
func attachedAfter(a, b Client) bool {
	if a.Created != b.Created {
		return a.Created > b.Created
	}
	if a.Activity != b.Activity {
		return a.Activity > b.Activity
	}
	return a.Name > b.Name
}

// OpenExisting returns the private server whose socket is at address, for
// reading a server a registry record names. Unlike NewServer it prepares
// nothing for a server that is not there: a missing socket is reported as
// ErrServerNotRunning.
func OpenExisting(address string) (*Server, error) {
	if _, err := os.Stat(address); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: no socket at %s", ErrServerNotRunning, address)
		}
		return nil, fmt.Errorf("tmux: %w", err)
	}
	server, err := NewServer(Options{Dir: filepath.Dir(address)})
	if err != nil {
		return nil, err
	}
	if server.Socket() != address {
		return nil, fmt.Errorf("tmux: %s is not a private tmux socket", address)
	}
	return server, nil
}

func recordPane(record runtimesession.Record) backend.Ref {
	return backend.PaneID(record.Target.Ref)
}
