package kitty

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// runTimeout bounds how long a single kitten subprocess may run before it is
// killed. Hook scripts and the indicator invoke kitten synchronously on every
// Claude/Copilot event, so a wedged or stalled kitten remote control call
// must not be allowed to hang the hook indefinitely. Exposed as a var so
// tests can shrink it without sleeping for production duration.
var runTimeout = 10 * time.Second

// CommandRunner abstracts exec.Command for testability.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, []byte, error)
}

// RealRunner executes commands via os/exec.
type RealRunner struct{}

// NewRealRunner returns a RealRunner.
func NewRealRunner() *RealRunner { return &RealRunner{} }

func (r *RealRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
