package debug

import (
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// The two seams every doctor and audit test installs: a daemon that answers
// Health and ExportEvents from canned values, and a Kitty window lister that
// answers from a fixed tree. The daemon command family keeps its own copies
// for its own export tests; these belong to this package's fixtures.
type fakeDaemonExportClient struct {
	health ipc.Health
	calls  []int
	export func(windowID int) (protocol.EventsExport, error)
}

func (f *fakeDaemonExportClient) ExportEvents(windowID int) (protocol.EventsExport, error) {
	f.calls = append(f.calls, windowID)
	if f.export == nil {
		return protocol.EventsExport{}, nil
	}
	return f.export(windowID)
}

func (f *fakeDaemonExportClient) Health() ipc.Health {
	return f.health
}

type fakeKittyWindowLister struct {
	windows []kitty.OSWindow
	err     error
}

func (f fakeKittyWindowLister) ListWindows() ([]kitty.OSWindow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.windows, nil
}
