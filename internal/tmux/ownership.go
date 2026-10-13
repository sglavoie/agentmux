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

// Survey reads the server. A server that is not running is not an error: it
// is reported as such, with no panes and no clients. Any other failure is an
// error, so an unreachable server is never mistaken for an absent one.
func (b *Backend) Survey() (Survey, error) {
	survey := Survey{Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: b.server.socket}}
	paneFormat := strings.Join(append(append([]string{surveyPane}, discoverFields...), "#{pane_width}", "#{pane_height}"), fieldSep)
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
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		tag, rest, _ := strings.Cut(line, fieldSep)
		f := strings.Split(rest, fieldSep)
		switch tag {
		case surveyServer:
			if len(f) != 2 || f[0] == "" || f[1] == "" {
				return Survey{}, fmt.Errorf("tmux: unexpected server identity %q", line)
			}
			survey.Instance = b.server.instance(f[0], f[1])
			survey.Running = true
		case surveyPane:
			if len(f) != len(discoverFields)+2 {
				return Survey{}, fmt.Errorf("tmux: malformed pane line %q", line)
			}
			pane, err := parsePane(strings.Join(f[:len(discoverFields)], fieldSep))
			if err != nil {
				return Survey{}, err
			}
			cols, _ := strconv.Atoi(f[len(f)-2])
			rows, _ := strconv.Atoi(f[len(f)-1])
			survey.Panes = append(survey.Panes, pane)
			survey.Sizes[pane.Ref.ID] = Size{Cols: cols, Rows: rows}
		case surveyClient:
			if len(f) != len(clientFields) {
				return Survey{}, fmt.Errorf("tmux: malformed client line %q", line)
			}
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
// dead, so within one server generation that PID cannot have been reused.
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
	switch {
	case fingerprint != nil:
		if process, err := fingerprint(pid); err == nil {
			observation.Process = &process
		}
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
		sort.Slice(clients, func(i, j int) bool {
			if clients[i].Created != clients[j].Created {
				return clients[i].Created > clients[j].Created
			}
			if clients[i].Activity != clients[j].Activity {
				return clients[i].Activity > clients[j].Activity
			}
			return clients[i].Name > clients[j].Name
		})
		competing = append(competing, clients[1:]...)
	}
	sort.Slice(competing, func(i, j int) bool { return competing[i].Name < competing[j].Name })
	return competing
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
