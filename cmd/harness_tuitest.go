//go:build tuitest

package cmd

import (
	"github.com/sglavoie/agentmux/cmd/tuitest"
	"github.com/spf13/cobra"
)

// scenarioHarnessCommand is the hidden `tui-test` tree. Only a build with the
// tuitest tag links the harness; harness_stub.go is the release build's answer.
func scenarioHarnessCommand() *cobra.Command { return tuitest.Command() }
