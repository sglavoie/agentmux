package debug

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// This file is doctor's view of the tmux backend and of the runtime sessions
// the daemon keeps for it, and `amx debug status` prints the same lines. Two
// sources answer, and they are kept apart because they fail apart:
//
//   - the backend setup (cli.BackendStatus) is read by this process, so it is
//     reported with the daemon down, which is when an operator most needs it;
//   - the daemon's own work (protocol.RuntimeHealth) comes with health: how
//     current its reconciliation is, which sessions have a terminal attached,
//     how its screen reads are going, how many tmux clients it runs.
//
// Everything printed is a count, a duration, a version, a path of the
// installation, or a word from a fixed vocabulary. Nothing is read from a
// pane, a prompt, or a launch environment.

// The finding identifiers this file raises. A backend problem keeps the code
// `amx backend status` gives it, prefixed, so the two reports name one fault
// one way.
const (
	checkBackendPrefix = "backend_"
	// checkRuntimeReconcileStale: the daemon has not finished a pass over the
	// private servers for far longer than its interval.
	checkRuntimeReconcileStale = "runtime_reconcile_stale"
	// checkRuntimeReconcileFailing: several passes in a row could not read a
	// server or the registry.
	checkRuntimeReconcileFailing = "runtime_reconcile_failing"
	// checkScreenCaptureFailing: every recent screen read failed.
	checkScreenCaptureFailing = "screen_capture_failing"
	// checkTmuxClientTimeouts: tmux clients of the daemon ran or queued past
	// their timeout since it started.
	checkTmuxClientTimeouts = "tmux_client_timeouts"
)

const (
	// runtimeReconcileStaleAfter is how old the last pass may be beyond the
	// wait the daemon says it is observing (two seconds, more while passes
	// fail). A pass that far overdue is a stalled loop, not a slow one.
	runtimeReconcileStaleAfter = 30 * time.Second
	// runtimeReconcileFailingAfter and screenCaptureFailingAfter are how many
	// consecutive failures make a finding: one failed pass is a server that
	// was busy, and one failed read is a pane that just closed.
	runtimeReconcileFailingAfter = 3
	screenCaptureFailingAfter    = 10
)

var (
	// doctorBackendStatus reads the backend setup the way `amx backend
	// status` does, including its one side effect: a bundled runtime is
	// installed if it is not yet.
	doctorBackendStatus = func() cli.BackendStatus {
		cfg, err := doctorLoadBackendConfig()
		var missing *config.MissingConfigError
		if errors.As(err, &missing) {
			cfg, err = nil, nil
		}
		if cfg != nil {
			cli.ConfigureTmux(cfg)
		}
		return cli.CollectBackendStatus(cfg, err, cli.BackendProbe{})
	}
	doctorLoadBackendConfig = cli.LoadRunnableConfig
	// doctorKittyVersion asks the kitty on PATH for its release; empty when
	// there is none or it does not answer.
	doctorKittyVersion = func() string {
		path, err := doctorLookPath("kitty")
		if err != nil {
			return ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "--version").Output() // #nosec G204 -- kitty from PATH, fixed argument.
		if err != nil {
			return ""
		}
		// "kitty 0.43.1 created by Kovid Goyal"
		fields := strings.Fields(string(out))
		if len(fields) < 2 || fields[0] != "kitty" {
			return ""
		}
		return fields[1]
	}
)

// doctorVersions is every release this installation depends on, in one place.
// A value is empty when the dependency is absent or did not answer.
type doctorVersions struct {
	AMX    string `json:"amx"`
	Daemon string `json:"daemon,omitempty"`
	Go     string `json:"go,omitempty"`
	// Tmux is the tmux a new private server would run, TmuxSource where it
	// was found, and TmuxMin the oldest release the backend supports.
	Tmux       string `json:"tmux,omitempty"`
	TmuxSource string `json:"tmux_source,omitempty"`
	TmuxMin    string `json:"tmux_min"`
	Kitty      string `json:"kitty,omitempty"`
	// Notifier is the native notification backend, with its release.
	Notifier        string `json:"notifier,omitempty"`
	NotifierVersion string `json:"notifier_version,omitempty"`
	// CompanionApp lists the releases of the connected companion apps.
	CompanionApp []string `json:"companion_app,omitempty"`
}

func collectDoctorVersions(cliBuild buildinfo.Info, health ipc.Health, backend cli.BackendStatus) doctorVersions {
	versions := doctorVersions{
		AMX:        cliBuild.Version,
		Go:         cliBuild.GoVersion,
		Tmux:       backend.Tmux.Version,
		TmuxSource: backend.Tmux.Source,
		TmuxMin:    backend.Tmux.MinVersion,
		Kitty:      doctorKittyVersion(),
	}
	if health.Build != nil {
		versions.Daemon = health.Build.Version
	}
	if n := health.Notification; n != nil && n.Available && n.Backend != "" {
		versions.Notifier, versions.NotifierVersion = n.Backend, n.Version
	}
	if health.Companion != nil {
		for _, client := range health.Companion.Clients {
			if client.ClientVersion != "" {
				versions.CompanionApp = append(versions.CompanionApp, client.ClientVersion)
			}
		}
	}
	return versions
}

func (v doctorVersions) summary() string {
	parts := []string{"amx " + orUnknown(v.AMX)}
	if v.Daemon != "" {
		parts = append(parts, "daemon "+v.Daemon)
	}
	if v.Go != "" {
		parts = append(parts, v.Go)
	}
	tmux := "tmux not found"
	if v.Tmux != "" {
		tmux = "tmux " + v.Tmux
	}
	parts = append(parts, fmt.Sprintf("%s (needs %s or newer)", tmux, v.TmuxMin))
	if v.Kitty != "" {
		parts = append(parts, "kitty "+v.Kitty)
	} else {
		parts = append(parts, "kitty not found")
	}
	if v.Notifier != "" {
		parts = append(parts, strings.TrimSpace(v.Notifier+" "+v.NotifierVersion))
	}
	for _, app := range v.CompanionApp {
		parts = append(parts, "app "+app)
	}
	return strings.Join(parts, ", ")
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

// backendSummary is the whole line after "backend: ".
func backendSummary(status cli.BackendStatus) string {
	tmux := "no usable tmux"
	if status.Tmux.Available {
		tmux = fmt.Sprintf("tmux %s (%s", status.Tmux.Version, status.Tmux.Source)
		if status.Tmux.Verified {
			tmux += ", verified"
		}
		tmux += ")"
	}
	server := "no server running"
	unreadable := false
	for _, problem := range status.Problems {
		unreadable = unreadable || problem.Code == "server_unreadable"
	}
	if status.Server.Running && unreadable {
		// Its sessions were not counted; zero would read as an empty server.
		server = "server running but not answering"
	} else if status.Server.Running {
		server = fmt.Sprintf("server running with %s in %s",
			cli.CountOf(status.Server.Agents, "agent", "agents"),
			cli.CountOf(status.Server.Sessions, "session", "sessions"))
	}
	return fmt.Sprintf("launches go to %s; %s; %s", status.LaunchBackend, tmux, server)
}

// kittyNotInUse reports whether a Kitty that does not answer is simply a
// Kitty that is not part of this installation: launches go to tmux, and this
// process is not running in a Kitty window that could be asked. Anywhere else
// a failed `kitten @ ls` stays the failure it has always been.
func kittyNotInUse(status cli.BackendStatus) bool {
	return status.LaunchBackend == config.LaunchBackendTmux && strings.TrimSpace(doctorGetenv("KITTY_LISTEN_ON")) == ""
}

// backendFindingSeverity maps a backend problem onto doctor's severities.
// A problem that stops tmux launches is a failure only where the operator
// launches on tmux or has agents there; for someone who launches in Kitty and
// has no server, a missing tmux is how the machine is set up, and is a note.
func backendFindingSeverity(status cli.BackendStatus, problem cli.BackendProblem) string {
	switch problem.Severity {
	case "error":
		return severityFailure
	case "warning":
		if problem.Code == "tmux_unavailable" && !status.Server.Running {
			return severityInfo
		}
		return severityWarning
	default:
		return severityInfo
	}
}

func writeDoctorBackend(out io.Writer, status cli.BackendStatus, addFinding func(string, string, string, ...any)) error {
	if _, err := fmt.Fprintf(out, "backend: %s\n", backendSummary(status)); err != nil {
		return err
	}
	for _, problem := range status.Problems {
		// A configuration that does not load is reported where doctor reads
		// the configuration; repeating it here would count one fault twice.
		if problem.Code == "config_invalid" {
			continue
		}
		message := problem.Message
		if problem.Hint != "" {
			message += " " + problem.Hint
		}
		addFinding(backendFindingSeverity(status, problem), checkBackendPrefix+problem.Code, "%s", message)
	}
	return nil
}

func secondsAgo(age *float64) string {
	if age == nil {
		return "never"
	}
	return (time.Duration(*age * float64(time.Second))).Round(100*time.Millisecond).String() + " ago"
}

// runtimeSessionsSummary is the whole line after "runtime sessions: ".
func runtimeSessionsSummary(runtime *protocol.RuntimeHealth) string {
	if runtime == nil {
		return "unreported (older daemon; restart required)"
	}
	if !runtime.Reconcile.Enabled {
		return "not reconciled (the daemon has no runtime-session registry)"
	}
	sessions := "registry not read yet"
	if runtime.Sessions.Known {
		sessions = fmt.Sprintf("%d live", runtime.Sessions.Live)
		if runtime.Sessions.Live > 0 {
			sessions += fmt.Sprintf(" (%d on tmux, %s), %d with a terminal attached, %d unattached",
				runtime.Sessions.Tmux, cli.CountOf(runtime.Sessions.Servers, "server", "servers"),
				runtime.Sessions.Attached, runtime.Sessions.Unattached)
		}
	}
	r := runtime.Reconcile
	reconcile := fmt.Sprintf("reconciled %s in %.0fms (%s, %d failed)",
		secondsAgo(r.AgeSeconds), r.LastDurationMs, cli.CountOf(int(min(r.Passes, 1<<31)), "pass", "passes"), r.Failures)
	if r.FailureClass != "" {
		reconcile += "; last pass failed: " + r.FailureClass
	}
	return sessions + "; " + reconcile
}

// screenCapturesSummary is the whole line after "screen captures: ".
func screenCapturesSummary(runtime *protocol.RuntimeHealth) string {
	c, t := runtime.Capture, runtime.TmuxClient
	reads := "no screen read yet"
	if c.Reads > 0 {
		reads = fmt.Sprintf("%d read, %d failed, last good one %s (slowest %.0fms)", c.Reads, c.Failures, secondsAgo(c.AgeSeconds), c.MaxDurationMs)
	}
	return fmt.Sprintf("%s; tmux clients: %d run, at most %d at once (limit %d), %d timed out",
		reads, t.Commands, t.MaxInFlight, t.Limit, t.Timeouts)
}

func writeDoctorRuntime(out io.Writer, runtime *protocol.RuntimeHealth, addFinding func(string, string, string, ...any)) error {
	if _, err := fmt.Fprintf(out, "runtime sessions: %s\n", runtimeSessionsSummary(runtime)); err != nil {
		return err
	}
	if runtime == nil {
		return nil
	}
	if _, err := fmt.Fprintf(out, "screen captures: %s\n", screenCapturesSummary(runtime)); err != nil {
		return err
	}
	r := runtime.Reconcile
	switch {
	case !r.Enabled:
	case r.ConsecutiveFailures >= runtimeReconcileFailingAfter:
		addFinding(severityWarning, checkRuntimeReconcileFailing,
			"the last %d reconciliation passes failed (%s): agents that exit or lose their terminal are not noticed until one succeeds. `amx backend status` reads the server directly",
			r.ConsecutiveFailures, orUnknown(r.FailureClass))
	case r.AgeSeconds != nil && *r.AgeSeconds > r.IntervalSeconds+runtimeReconcileStaleAfter.Seconds():
		addFinding(severityWarning, checkRuntimeReconcileStale,
			"the daemon last reconciled runtime sessions %s (it is due every %.0fs): its maintenance loop is stalled",
			secondsAgo(r.AgeSeconds), r.IntervalSeconds)
	}
	if c := runtime.Capture; c.ConsecutiveFailures >= screenCaptureFailingAfter {
		addFinding(severityWarning, checkScreenCaptureFailing,
			"the last %d screen reads failed (last good one %s): stuck-indicator recovery has no evidence to work from",
			c.ConsecutiveFailures, secondsAgo(c.AgeSeconds))
	}
	if t := runtime.TmuxClient; t.Timeouts > 0 {
		addFinding(severityInfo, checkTmuxClientTimeouts,
			"%d of the daemon's %d tmux client commands timed out since it started (longest wait for a slot %.0fms, slowest command %.0fms)",
			t.Timeouts, t.Commands, t.MaxQueueMs, t.MaxDurationMs)
	}
	return nil
}
