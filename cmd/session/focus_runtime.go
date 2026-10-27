package session

// The focus commands outside Kitty. A session on the tmux backend is not a
// tab: "the next one" is its neighbour in the order every surface lists
// sessions in (session.Order), and going there moves a terminal. Hosted by
// the desktop workspace that is a workspace_view request, which the workspace
// answers by showing the session itself; in any other terminal it is a tmux
// client switched to the session, under the rules attaching holds to.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
)

const (
	focusRuntimeSessionFlag = "runtime-session"
	focusClientFlag         = "client"
)

// The seams the tests replace: the session this command runs in, whether the
// desktop workspace hosts it, the configuration the order is read from, and
// the daemon's workspace.
var (
	// currentRuntimeSession is the runtime session this command runs in, for
	// a session with no Kitty window. In a Kitty window the window is what a
	// command with nothing named means, as it always was: a runtime session
	// on the Kitty backend is not found on tmux.
	currentRuntimeSession = func() string {
		if cli.WindowIDFromEnv() != 0 {
			return ""
		}
		return strings.TrimSpace(os.Getenv(runtimesession.EnvVar))
	}
	// focusWorkspaceHosted is whether this command itself was started by the
	// workspace. A command typed in an agent's pane never is, whoever shows
	// the pane: there the session's recorded owner answers
	// (tmux.Switcher.Hosted).
	focusWorkspaceHosted = attachHosted
	focusLoadConfig      = config.LoadDefault
	focusWorkspaceView   = func(view protocol.WorkspaceViewRequest) error {
		_, err := ipc.NewClient().WorkspaceView(view)
		return err
	}
)

func addFocusRuntimeFlags(cmd *cobra.Command) {
	cmd.Flags().String(focusRuntimeSessionFlag, "",
		"runtime session ID of the current tmux session (defaults to $"+runtimesession.EnvVar+")")
	cmd.Flags().String(focusClientFlag, "",
		"tmux client (terminal device) to switch (defaults to the terminal attached to the current session)")
}

// focusRuntimeSession reports the runtime session a focus command starts
// from, when it has one. A session named by flag wins; a Kitty window, named
// by flag or the one this command runs in, is Kitty's; otherwise the session
// this command runs in decides.
func focusRuntimeSession(cmd *cobra.Command, windowID int) (string, bool) {
	if named, _ := cmd.Flags().GetString(focusRuntimeSessionFlag); strings.TrimSpace(named) != "" {
		return strings.TrimSpace(named), true
	}
	if windowID != 0 {
		return "", false
	}
	if client, _ := cmd.Flags().GetString(focusClientFlag); strings.TrimSpace(client) != "" {
		// A terminal was named, so this is tmux even with no session to
		// start from; that is reported rather than handed to Kitty.
		return currentRuntimeSession(), true
	}
	current := currentRuntimeSession()
	return current, current != ""
}

// focusChoice picks where to go from the session at index current of the
// sessions in list order. live reports which ones still run an agent, the
// only ones it may choose; attention reports which ones wait on the operator.
type focusChoice func(ordered []session.RunningSession, current int, live, attention func(session.RunningSession) bool) (session.RunningSession, bool)

// neighborSession is the nearest running session in direction, wrapping
// around. The current session is never chosen.
func neighborSession(direction int) focusChoice {
	return func(ordered []session.RunningSession, current int, live, _ func(session.RunningSession) bool) (session.RunningSession, bool) {
		n := len(ordered)
		for step := 1; step < n; step++ {
			if candidate := ordered[((current+direction*step)%n+n)%n]; live(candidate) {
				return candidate, true
			}
		}
		return session.RunningSession{}, false
	}
}

// nextAttentionSession is the first running session after the current one,
// wrapping around, that waits on the operator. The current session is never
// chosen.
func nextAttentionSession(ordered []session.RunningSession, current int, live, attention func(session.RunningSession) bool) (session.RunningSession, bool) {
	n := len(ordered)
	for step := 1; step < n; step++ {
		if candidate := ordered[(current+step)%n]; live(candidate) && attention(candidate) {
			return candidate, true
		}
	}
	return session.RunningSession{}, false
}

// runtimeAttention reads which tmux sessions wait on the operator from the
// daemon, by the predicate the companion snapshot uses: an icon state in
// protocol.AttentionStates. A daemon that cannot be asked marks none, so
// focus-next goes nowhere rather than somewhere arbitrary; it is never
// started for this.
func runtimeAttention() func(session.RunningSession) bool {
	waiting := map[string]bool{}
	if response, err := sessionCommandsDaemon(protocol.Request{Type: protocol.TypeStateQuery}); err == nil && response.OK {
		for slot, bound := range response.RuntimeSlots {
			if state, ok := response.States[slot]; ok && protocol.AttentionStates[state.IconState] {
				waiting[bound.RuntimeSessionID] = true
			}
		}
	}
	return func(rs session.RunningSession) bool { return waiting[string(rs.RuntimeSessionID)] }
}

func runFocusRuntime(cmd *cobra.Command, named string, choose focusChoice) error {
	if named == "" {
		return cli.MissingParameter(cmd, false, "--"+focusRuntimeSessionFlag, "the runtime session ID of the tmux session to start from")
	}
	id, err := cli.ParseRuntimeSessionID(named)
	if err != nil {
		return cli.CommandFailure(cmd, false, "invalid_arguments", err)
	}
	client, _ := cmd.Flags().GetString(focusClientFlag)
	client = strings.TrimSpace(client)
	opened, err := attachNewTmux()
	if err != nil {
		return cli.CommandFailure(cmd, false, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", err))
	}
	server, ok := opened.(*tmux.Backend)
	if !ok {
		return cli.CommandFailure(cmd, false, "backend_unavailable", errors.New("tmux backend cannot switch terminals"))
	}
	panes, err := server.Discover()
	if err != nil {
		return cli.CommandFailure(cmd, false, "backend_unavailable", fmt.Errorf("list tmux sessions: %w", err))
	}
	// The order needs only the sort mode and the profile names, so a
	// configuration that cannot be read costs the mode, not the command.
	cfg, _ := focusLoadConfig()
	mode := ""
	if cfg != nil {
		mode = cfg.Defaults.Sort
	}
	// The session to start from may be one whose agent exited, its pane kept
	// and still on screen, so its place comes from an order of every pane
	// the server holds; the session to go to is chosen among the live ones.
	running := map[runtimesession.ID]bool{}
	for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg) {
		running[rs.RuntimeSessionID] = true
	}
	held := make([]backend.Pane, len(panes))
	for i, pane := range panes {
		pane.Exit = nil
		held[i] = pane
	}
	ordered := session.SortRunning(session.RunningSessionsFromPanes(runtimesession.BackendTmux, held, cfg), mode)
	live := func(rs session.RunningSession) bool { return rs.RuntimeSessionID != "" && running[rs.RuntimeSessionID] }
	current := -1
	for i, rs := range ordered {
		if rs.RuntimeSessionID == id {
			current = i
		}
	}
	if current < 0 {
		return cli.CommandFailure(cmd, false, "not_found", noRuntimeSession(id))
	}
	target, ok := choose(ordered, current, live, runtimeAttention())
	if !ok {
		return nil
	}
	store, err := runtimesession.NewStore(attachRegistryPath())
	if err != nil {
		return cli.CommandFailure(cmd, false, "registry_unavailable", err)
	}
	switcher := tmux.Switcher{Store: store, Backend: server, Fingerprint: attachFingerprint, Now: attachNow}
	// With no terminal named, the one to move is whichever shows the current
	// session, and a terminal of the workspace's is the workspace's to move.
	hosted := client == "" && focusWorkspaceHosted()
	if client == "" && !hosted {
		if hosted, err = switcher.Hosted(id); err != nil {
			return cli.CommandFailure(cmd, false, "registry_unavailable", err)
		}
	}
	if hosted {
		view := protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewSession, RuntimeSessionID: string(target.RuntimeSessionID)}
		if err := focusWorkspaceView(view); err != nil {
			return cli.CommandFailure(cmd, false, "workspace_view_failed", fmt.Errorf("asking the workspace to show %s: %w", target.RuntimeSessionID, err))
		}
		return nil
	}
	if client == "" {
		viewer, err := switcher.Viewer(id)
		if err != nil {
			return cli.CommandFailure(cmd, false, "no_client", fmt.Errorf("%w; name the terminal to switch with --%s", err, focusClientFlag))
		}
		client = viewer.Name
	}
	return switchFailure(cmd, switcher.Switch(client, target.RuntimeSessionID))
}

// switchFailure reports a refused switch under the code attach uses for the
// same refusal.
func switchFailure(cmd *cobra.Command, err error) error {
	var notLive *tmux.NotLiveError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &notLive):
		return cli.CommandFailure(cmd, false, notLiveCode(notLive.Record), notLiveError(notLive.Record))
	case errors.Is(err, tmux.ErrNoClient):
		return cli.CommandFailure(cmd, false, "no_client", err)
	case errors.Is(err, tmux.ErrPaneExited):
		return cli.CommandFailure(cmd, false, "runtime_session_exited", err)
	case errors.Is(err, tmux.ErrPaneGone):
		return cli.CommandFailure(cmd, false, "runtime_session_lost", err)
	}
	return cli.CommandFailure(cmd, false, "switch_failed", err)
}
