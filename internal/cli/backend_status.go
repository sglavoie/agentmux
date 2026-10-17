package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// BackendStatus is what `amx backend status` reports: where launches go, which
// tmux the private server runs and why, whether a server is running and with
// how many agents, and everything that needs the operator's attention. The
// desktop app shows it in Preferences and the uninstaller reads it to say what
// would be left running.
type BackendStatus struct {
	// LaunchBackend is where a launch goes without --backend.
	LaunchBackendSource string           `json:"launch_backend_source"`
	LaunchBackend       string           `json:"launch_backend"`
	Tmux                TmuxStatus       `json:"tmux"`
	Server              TmuxServerStatus `json:"server"`
	// Problems is empty when launching on tmux would work as configured.
	Problems []BackendProblem `json:"problems"`
}

// TmuxStatus describes the tmux a new private server would run.
type TmuxStatus struct {
	Available  bool             `json:"available"`
	Path       string           `json:"path,omitempty"`
	Version    string           `json:"version,omitempty"`
	Source     string           `json:"source,omitempty"`
	Verified   bool             `json:"verified"`
	MinVersion string           `json:"min_version"`
	Candidates []tmux.Candidate `json:"candidates"`
}

// TmuxServerStatus describes the private server as it is now.
type TmuxServerStatus struct {
	Directory string `json:"directory"`
	Socket    string `json:"socket"`
	Running   bool   `json:"running"`
	// Binary is the executable the running server was started with.
	Binary string `json:"binary,omitempty"`
	// Generation identifies this run of the server (its PID and start time):
	// it changes if and only if the server was replaced, which is what an
	// installer compares before and after an update.
	Generation string `json:"generation,omitempty"`
	// Sessions counts every session the server holds; Agents counts those
	// whose program is still running.
	Sessions int `json:"sessions"`
	Agents   int `json:"agents"`
}

// BackendProblem is one thing wrong with the backend setup, with what to do
// about it. Severity is "error" when launching on tmux cannot work, "warning"
// when it works but not as configured, and "info" for a state the operator
// may want to know about.
type BackendProblem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// BackendProbe is how CollectBackendStatus reads the machine. Zero fields
// read the real one.
type BackendProbe struct {
	Resolve   func() (tmux.Runtime, []tmux.Candidate, error)
	ServerDir func() string
	Listening func(socket string) bool
	Pinned    func(dir string) string
	// Survey reads the running server: its panes and its generation.
	Survey func() ([]backend.Pane, string, error)
}

func (p BackendProbe) withDefaults() BackendProbe {
	if p.Resolve == nil {
		p.Resolve = func() (tmux.Runtime, []tmux.Candidate, error) {
			return tmux.ResolveRuntime(tmux.RuntimeOptions{})
		}
	}
	if p.ServerDir == nil {
		p.ServerDir = tmux.DefaultDir
	}
	if p.Listening == nil {
		p.Listening = tmux.Listening
	}
	if p.Pinned == nil {
		p.Pinned = tmux.PinnedBinary
	}
	if p.Survey == nil {
		p.Survey = func() ([]backend.Pane, string, error) {
			server, err := tmux.NewServer(tmux.Options{})
			if err != nil {
				return nil, "", err
			}
			instance, err := server.Instance()
			if err != nil {
				return nil, "", err
			}
			panes, err := tmux.NewBackend(server).Discover()
			return panes, instance.Generation, err
		}
	}
	return p
}

// CollectBackendStatus reads the backend setup. cfgErr is the error loading
// the configuration, reported as a problem rather than a failure: the report
// is what an operator reads to find out what is wrong. It starts no server.
func CollectBackendStatus(cfg *config.Config, cfgErr error, probe BackendProbe) BackendStatus {
	probe = probe.withDefaults()
	status := BackendStatus{
		LaunchBackend: cfg.LaunchBackendOrDefault(),
		Tmux:          TmuxStatus{MinVersion: tmux.MinVersion, Candidates: []tmux.Candidate{}},
		Problems:      []BackendProblem{},
	}
	status.LaunchBackendSource = "compatibility default"
	if cfg != nil && cfg.Defaults.LaunchBackend != "" {
		status.LaunchBackendSource = "config.toml"
	}
	if cfgErr != nil {
		status.Problems = append(status.Problems, BackendProblem{
			Code:     "config_invalid",
			Severity: "error",
			Message:  "The configuration could not be loaded: " + cfgErr.Error(),
			Hint:     "Fix " + config.DefaultConfigPath() + " (`amx config validate` lists every error), or create one with `amx config init`.",
		})
	}

	// Without tmux only a tmux launch fails, so it is an error for an
	// operator who launches there and a warning for one who launches in Kitty.
	unusable := "warning"
	if status.LaunchBackend == config.LaunchBackendTmux {
		unusable = "error"
	}

	dir := probe.ServerDir()
	status.Server.Directory = dir
	status.Server.Socket = filepath.Join(dir, "tmux.sock")
	status.Server.Running = probe.Listening(status.Server.Socket)
	if status.Server.Running {
		status.Server.Binary = probe.Pinned(dir)
	}

	runtime, candidates, err := probe.Resolve()
	if candidates != nil {
		status.Tmux.Candidates = candidates
	}
	if err != nil {
		message := err.Error()
		var none *tmux.NoRuntimeError
		if errors.As(err, &none) {
			message = strings.TrimSuffix(message, "\n"+tmux.RuntimeHint)
		}
		problem := BackendProblem{
			Code:     "tmux_unavailable",
			Severity: unusable,
			Message:  "The tmux backend cannot start agents: " + message,
			Hint:     tmux.RuntimeHint,
		}
		if status.Server.Binary != "" {
			// The running server has its own tmux, so launches still reach
			// it; what is missing is a tmux for the server after this one.
			problem.Severity = "warning"
			problem.Message = "Agents can be launched while the running server lasts, but no tmux was found to start another one: " + message
		}
		status.Problems = append(status.Problems, problem)
	} else {
		status.Tmux.Available = true
		status.Tmux.Path = runtime.Binary
		status.Tmux.Version = runtime.Version
		status.Tmux.Source = string(runtime.Source)
		status.Tmux.Verified = runtime.Verified
	}

	if !status.Server.Running {
		return status
	}
	switch {
	case status.Server.Binary == "":
		status.Problems = append(status.Problems, BackendProblem{
			Code:     "server_binary_unknown",
			Severity: "warning",
			Message:  "A private tmux server is running, but the tmux it was started with is not recorded or no longer exists.",
			Hint:     "Its agents keep running. They are reached with the tmux found now; if that fails, end them with `amx backend stop --yes` and launch again.",
		})
	case status.Tmux.Available && status.Server.Binary != status.Tmux.Path:
		status.Problems = append(status.Problems, BackendProblem{
			Code:     "server_keeps_previous_tmux",
			Severity: "info",
			Message:  fmt.Sprintf("The running server keeps the tmux it was started with (%s). %s is used once its last agent has ended.", status.Server.Binary, status.Tmux.Path),
		})
	}
	panes, generation, err := probe.Survey()
	if err != nil {
		status.Problems = append(status.Problems, BackendProblem{
			Code:     "server_unreadable",
			Severity: unusable,
			Message:  "The private tmux server is running but did not answer: " + err.Error(),
			Hint:     "Run `amx backend status` again; if it keeps failing, `amx backend stop --yes` ends the server and every agent in it.",
		})
		return status
	}
	status.Server.Generation = generation
	groups := map[string]bool{}
	for _, pane := range panes {
		groups[pane.Group.ID] = true
		if pane.Exit == nil {
			status.Server.Agents++
		}
	}
	status.Server.Sessions = len(groups)
	return status
}

// Usable reports whether launching on tmux would work now: no problem is an
// error, and there is a tmux to launch with, be it the one a new server would
// run or the one the running server was started with.
func (s BackendStatus) Usable() bool {
	for _, problem := range s.Problems {
		if problem.Severity == "error" {
			return false
		}
	}
	return s.Tmux.Available || (s.Server.Running && s.Server.Binary != "")
}
