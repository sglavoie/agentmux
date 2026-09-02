package commands

import (
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Registry is intentionally package-level and stateless. It gives direct
// keyboard paths the same handlers as the actions overlay and other callers
// without coupling the registry to Bubble Tea.
var Registry = actions.DefaultRegistry()

// DaemonRequest is a narrow seam around the IPC client so fetch and clear
// behavior can be tested without connecting to an operator's daemon.
var DaemonRequest = func(req protocol.Request) (protocol.Response, error) {
	return ipc.NewClient().Send(req)
}

// SpawnDaemonDetached is the seam the health probe uses to start a daemon it
// found unreachable.
var SpawnDaemonDetached = process.SpawnDetached

// CopyToClipboard is the seam every clipboard-writing command goes through.
var CopyToClipboard = clipboard.Copy
