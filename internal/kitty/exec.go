package kitty

import (
	"bytes"
	"os/exec"
)

// CommandRunner abstracts exec.Command for testability.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, []byte, error)
}

// RealRunner executes commands via os/exec.
type RealRunner struct{}

// NewRealRunner returns a RealRunner.
func NewRealRunner() *RealRunner { return &RealRunner{} }

func (r *RealRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
