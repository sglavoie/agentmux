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

// DaemonRequest is a narrow seam around the IPC client so command behavior can
// be tested without connecting to an operator's daemon. It carries the 250ms
// acknowledgement budget every ordinary request shares.
var DaemonRequest = func(req protocol.Request) (protocol.Response, error) {
	return ipc.NewClient().Send(req)
}

// StateQuery is the dashboard refresh's own seam. It is separate from
// DaemonRequest because a state query has its own one-second budget: it carries
// the cached Kitty window tree as well as per-window state, which is more than
// the acknowledgement budget was ever sized for. The client turns a refusal
// into an error, so a rejected query can never be read as a daemon with no
// windows.
var StateQuery = func() (protocol.Response, error) {
	return ipc.NewClient().StateQuery()
}

// SpawnDaemonDetached is the seam the health probe uses to start a daemon it
// found unreachable.
var SpawnDaemonDetached = process.SpawnDetached

// CopyToClipboard is the seam every clipboard-writing command goes through.
var CopyToClipboard = clipboard.Copy
