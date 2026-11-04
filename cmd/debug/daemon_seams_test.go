package debug

import (
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// The daemon seam doctor and audit tests install answers Health and ExportEvents
// from canned values. The daemon command family keeps its own export fixtures.
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
