package daemon

import (
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// The ASL block was added to an already-shipped health response, so the two
// mixed pairs an operator can actually end up with are a new CLI against an old
// daemon and an old CLI against a new one. Both must keep working without a
// protocol bump, which is only true while the block stays additive and
// omittable. These tests pin that at the wire, below any rendering.

// preASLHealthResponse mirrors the health fields a client built before the
// integration knew about. It carries no `asl` key at all, which is exactly what
// an older `amx` decoding a newer daemon's reply sees.
type preASLHealthResponse struct {
	OK             bool                      `json:"ok"`
	PID            int                       `json:"pid,omitempty"`
	UptimeSeconds  int64                     `json:"uptime_seconds,omitempty"`
	Build          *buildinfo.Info           `json:"build,omitempty"`
	Metrics        *protocol.MetricsSnapshot `json:"metrics,omitempty"`
	Ready          bool                      `json:"ready"`
	DegradedReason string                    `json:"degraded_reason,omitempty"`
}

func TestOlderClientStillReadsAHealthResponseCarryingASL(t *testing.T) {
	age := int64(7)
	encoded, err := json.Marshal(protocol.Response{
		OK: true, PID: 4242, UptimeSeconds: 90, Ready: true,
		ASLCapability: &aslbridge.Status{
			Mode: string(aslbridge.ModeAuto), Command: "asl", Available: true,
			MutationsEnabled: true, Protocol: 1, Version: "1.2.3",
			SnapshotAgeSeconds: &age,
			Metrics: &aslbridge.Metrics{OperationsByStatus: map[string]uint64{
				aslbridge.OperationAccepted: 1,
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal health response: %v", err)
	}

	var old preASLHealthResponse
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatalf("an older client failed to decode a newer health response: %v\n%s", err, encoded)
	}
	if !old.OK || !old.Ready || old.PID != 4242 || old.UptimeSeconds != 90 {
		t.Errorf("pre-ASL fields did not survive: %+v", old)
	}
}

func TestNewerClientReadsAnOlderDaemonHealthResponse(t *testing.T) {
	// Captured shape of a daemon built before the integration existed.
	const older = `{"ok":true,"pid":91,"uptime_seconds":120,"ready":true}`

	var resp protocol.Response
	if err := json.Unmarshal([]byte(older), &resp); err != nil {
		t.Fatalf("decoding an older daemon's health response: %v", err)
	}
	if resp.ASLCapability != nil {
		t.Errorf("an older daemon produced an asl block: %+v", resp.ASLCapability)
	}
	if !resp.OK || !resp.Ready || resp.PID != 91 {
		t.Errorf("older health response decoded wrong: %+v", resp)
	}
}

// Within the block, the counters grew after the block itself shipped. A daemon
// running an older bridge build reports metrics without the operation tally,
// and that absence must read as "nothing counted yet" rather than as a decode
// failure that would take the whole capability block down with it.
func TestASLStatusDecodesMetricsWithoutTheOperationTally(t *testing.T) {
	const older = `{"mode":"auto","available":true,"mutations_enabled":true,"restart_count":0,
		"metrics":{"frames_read":4,"frames_written":3,"bytes_read":900,"bytes_written":300,
		"restarts":0,"refusals":0,"requests_by_method":{"runs.snapshot":3}}}`

	var status aslbridge.Status
	if err := json.Unmarshal([]byte(older), &status); err != nil {
		t.Fatalf("decoding an older status block: %v", err)
	}
	if status.Metrics == nil {
		t.Fatal("metrics went missing")
	}
	if status.Metrics.OperationsByStatus != nil {
		t.Errorf("operations_by_status = %v, want it absent", status.Metrics.OperationsByStatus)
	}
	if got := status.Metrics.RequestsByMethod[aslbridge.MethodRunsSnapshot]; got != 3 {
		t.Errorf("requests_by_method[%s] = %d, want 3", aslbridge.MethodRunsSnapshot, got)
	}
}
