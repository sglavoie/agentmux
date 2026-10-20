package kitty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// runTimeout bounds how long a single kitten subprocess may run before it is
// killed. Hook scripts and the indicator invoke kitten synchronously on every
// Claude/Copilot event, so a wedged or stalled kitten remote control call
// must not be allowed to hang the hook indefinitely. Exposed as a var so
// tests can shrink it without sleeping for production duration.
var runTimeout = 10 * time.Second

// ErrNoKittyTerminal reports a kitten remote-control call that names no socket
// from a terminal that is not a Kitty window, so there is no Kitty it could
// reach.
var ErrNoKittyTerminal = errors.New("no Kitty socket is named and this terminal is not a Kitty window")

// UnreachableMessage is how a command words ErrNoKittyTerminal for a person,
// instead of the kitten command line and its error.
const UnreachableMessage = "Kitty is not reachable from this terminal (it is not a Kitty window and KITTY_LISTEN_ON is not set)"

// CommandRunner abstracts exec.Command for testability.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, []byte, error)
}

// RealRunner executes commands via os/exec.
type RealRunner struct{}

// NewRealRunner returns a RealRunner.
func NewRealRunner() *RealRunner { return &RealRunner{} }

func (r *RealRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	if speaksThroughTerminal(args) && !terminalIsKitty(os.Getenv) {
		return nil, nil, ErrNoKittyTerminal
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		// exec reports a deadline kill as a plain "signal: killed" ExitError,
		// which is indistinguishable from any other fatal signal. Joining the
		// context error keeps the ten-second budget exactly as it was while
		// letting callers classify the failure with errors.Is.
		err = fmt.Errorf("%w: %w", ctx.Err(), err)
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// speaksThroughTerminal reports a `kitten @` call with no --to. Kitten then
// speaks to Kitty through the controlling terminal: it puts the terminal in
// raw mode, writes the command as an escape sequence, and waits for the reply.
// A terminal that is not Kitty never replies, so the call ends at runTimeout
// with a kill, and a killed kitten leaves the terminal raw for whatever prints
// next.
func speaksThroughTerminal(args []string) bool {
	if len(args) == 0 || args[0] != "@" {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "--to" {
			return false
		}
	}
	return true
}

// terminalIsKitty reports whether the controlling terminal can be a Kitty
// window. Inside tmux it is tmux's pane whatever Kitty variables were
// inherited.
func terminalIsKitty(getenv func(string) string) bool {
	return getenv(WindowIDEnv) != "" && getenv("TMUX") == ""
}
