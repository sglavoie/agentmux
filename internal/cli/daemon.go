package cli

import (
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// DaemonRestartAdvice is the one next step every command names for a daemon
// that is not running. `amx daemon restart` starts one detached whether or not
// one runs; `amx daemon start` would hold the terminal in the foreground.
const DaemonRestartAdvice = "run `amx daemon restart` to start it in the background"

// DaemonNotRunningMessage is how every command says the daemon is not running,
// with DaemonRestartAdvice, instead of the socket error that told it so.
const DaemonNotRunningMessage = "the daemon is not running; " + DaemonRestartAdvice

// DaemonExportClient is the daemon connection the investigation surfaces share:
// `amx daemon export-events`, `amx doctor`, and `amx daemon audit` all want the
// same two answers — the durable event snapshot and the daemon's health — and
// all three swap the client for a fixture in their tests. Command families that
// need a different slice of the daemon declare their own narrower contract
// rather than widening this one.
type DaemonExportClient interface {
	ExportEvents(windowID int) (protocol.EventsExport, error)
	Health() ipc.Health
}

// NewDaemonExportClient dials the running daemon. The client applies the
// transport's own connect and auto-start policy; nothing about that policy is
// re-decided per command family, which is why the construction lives here.
func NewDaemonExportClient() DaemonExportClient {
	return ipc.NewClient()
}
