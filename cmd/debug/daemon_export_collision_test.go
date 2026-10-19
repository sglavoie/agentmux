package debug

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestExportLiveMetadataJoinsOnlyTheNamedKittyInstance(t *testing.T) {
	const alias = 1 << 32
	windows := []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{{ID: 2, Title: "W:second", Windows: []kitty.Window{{ID: 7}}}}}}
	snapshot := protocol.EventsExport{
		WindowMetadata: map[int]protocol.WindowDebugMetadata{7: {}, alias: {}},
		KittySlots: map[int]protocol.KittySlot{
			7:     {Socket: "unix:/kitty-a", WindowID: 7},
			alias: {Socket: "unix:/kitty-b", WindowID: 7},
		},
	}
	live := ExportLiveMetadata(windows, snapshot, 0, "unix:/kitty-b")
	if live[7].Found || !live[alias].Found || live[alias].TabTitle != "W:second" {
		t.Fatalf("live metadata crossed Kitty instances: %+v", live)
	}
}
