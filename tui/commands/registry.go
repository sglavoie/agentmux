package commands

import (
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/clipboard"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
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

// LocalKittySocket reports the reusable remote-control endpoint of the Kitty
// instance this dashboard is running inside. It is the identity a daemon
// snapshot's own socket is checked against, so the dashboard never renders — or
// writes back into — a window tree belonging to a different Kitty.
//
// It is the NORMALIZED address rather than the raw KITTY_LISTEN_ON: a pane
// launched with --allow-remote-control inherits a process-local "fd:N"
// descriptor, which names the same instance the daemon dials as
// "unix:/tmp/kitty-<pid>" but shares no characters with it. Comparing the raw
// values would report a mismatch for the ordinary single-Kitty case.
//
// It is empty outside Kitty and for any address that cannot be normalized,
// which is "unknown", not "matches everything"; the comparison below is what
// decides what to do with that.
var LocalKittySocket = kitty.NormalizedListenAddressFromEnv

// SpawnDaemonDetached is the seam the health probe uses to start a daemon it
// found unreachable.
var SpawnDaemonDetached = process.SpawnDetached

// CopyToClipboard is the seam every clipboard-writing command goes through.
var CopyToClipboard = clipboard.Copy
