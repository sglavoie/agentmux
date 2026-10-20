// Package testenv keeps a test binary from inheriting the operator's agent
// session.
//
// `go test` is often typed inside an Agent Mux session: a tmux pane on the
// private server, a Kitty window, or a terminal of the desktop workspace.
// Such a shell carries variables that name the session and terminal it runs
// in (AMX_RUNTIME_SESSION_ID, TMUX, KITTY_WINDOW_ID, ...), and production code
// reads them to decide which session a command or hook is about. A test that
// inherits them routes its hook to the operator's real session, believes it
// runs in Kitty, or refuses to attach because it already sits inside tmux.
//
// ClearSessionEnv, called first thing in a package's TestMain, removes them,
// so a test sees the environment of a plain terminal unless it sets a
// variable itself with t.Setenv. It touches only session and terminal
// identity: configuration overrides (AMX_CONFIG, AMX_TMUX, XDG_*, HOME) are
// left for each package's TestMain to isolate as it already does.
//
// The package is imported only by tests and must stay free of other
// in-repository dependencies, so any package's TestMain can import it.
package testenv

import "os"

// SessionVars lists the variables ClearSessionEnv removes. Each one describes
// the session or terminal the process was started in, never a choice a test
// makes on purpose.
var SessionVars = []string{
	// Agent Mux runtime session and the terminals that host it.
	"AMX_RUNTIME_SESSION_ID", // runtimesession.EnvVar: the managed session a launched agent belongs to
	"AMX_WORKSPACE_HOSTED",   // tmux.WorkspaceHostEnv: set by the desktop workspace's amx attach
	"AMX_BIN",                // kittykeys.TmuxAmxEnv: the amx a tmux server's key bindings run
	"AMX_TMUX_RUNTIME",       // tmux.RuntimeEnv: the bundled runtime the app's sessions inherit
	"AMX_DAEMON_DETACHED",    // process.DetachedEnv: set only in a daemon the CLI spawned
	"AMX_DAEMON_LAUNCH_ID",   // process.LaunchIDEnv: likewise
	"AMX_DAEMON_BOOTSTRAP_LOG",

	// An agent-session-loop run the shell belongs to.
	"AMX_ASL_RUN_ID",
	"ASL_RUN_ID",
	"AGENT_SESSION_LOOP_RUN_ID",

	// tmux: the server and pane the shell runs in.
	"TMUX",
	"TMUX_PANE",

	// Kitty: the window and instance the shell runs in.
	"KITTY_WINDOW_ID",
	"KITTY_LISTEN_ON",
	"KITTY_PID",
	"KITTY_PUBLIC_KEY",
	"WINDOWID",
}

// ClearSessionEnv unsets every variable in SessionVars. Call it at the start
// of TestMain, before m.Run; tests that need one of them set it again with
// t.Setenv, which restores the cleared state afterwards.
func ClearSessionEnv() {
	for _, key := range SessionVars {
		_ = os.Unsetenv(key)
	}
}
