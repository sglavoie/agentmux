package daemon

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestWindowStateUserVarRoundTrip(t *testing.T) {
	activity := time.Unix(1710000001, 0)
	guardExpiry := time.Unix(1710000301, 0)
	state := WindowState{
		Activity:                   activity,
		LastAction:                 "active",
		LastAccount:                "work",
		LastHarness:                "codex",
		LastActiveEvent:            "PreToolUse",
		CopilotToolPermissionState: copilotToolPermissionAutoApproved,
		SubagentCount:              2,
		PendingReset:               resetFlowClearing,
		NotifiedStale:              true,
		ContextLimitNotified:       true,
		LastPipeMode:               true,
		EventLog: []protocol.SessionEvent{
			{At: activity.Unix(), Event: "PreToolUse", Action: "active"},
		},
		Looping:         true,
		DesiredIcon:     "bolt",
		Dirty:           true,
		Seq:             7,
		RenderFailures:  3,
		LastRenderError: "kitty unavailable",
		GuardAction:     "active",
		GuardExpiresAt:  guardExpiry,
	}

	encoded, err := state.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	if strings.Contains(encoded, "PreToolUse") && strings.Contains(encoded, "result_icon_state") {
		t.Fatalf("MarshalUserVar() persisted event log: %s", encoded)
	}

	var got WindowState
	if err := got.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}

	if got.LastAction != state.LastAction {
		t.Fatalf("LastAction = %q, want %q", got.LastAction, state.LastAction)
	}
	if got.LastAccount != state.LastAccount {
		t.Fatalf("LastAccount = %q, want %q", got.LastAccount, state.LastAccount)
	}
	if got.LastHarness != state.LastHarness {
		t.Fatalf("LastHarness = %q, want %q", got.LastHarness, state.LastHarness)
	}
	if got.LastActiveEvent != state.LastActiveEvent {
		t.Fatalf("LastActiveEvent = %q, want %q", got.LastActiveEvent, state.LastActiveEvent)
	}
	if got.CopilotToolPermissionState != state.CopilotToolPermissionState {
		t.Fatalf("CopilotToolPermissionState = %d, want %d", got.CopilotToolPermissionState, state.CopilotToolPermissionState)
	}
	if got.SubagentCount != state.SubagentCount {
		t.Fatalf("SubagentCount = %d, want %d", got.SubagentCount, state.SubagentCount)
	}
	if got.PendingReset != state.PendingReset {
		t.Fatalf("PendingReset = %d, want %d", got.PendingReset, state.PendingReset)
	}
	if got.NotifiedStale != state.NotifiedStale {
		t.Fatalf("NotifiedStale = %v, want %v", got.NotifiedStale, state.NotifiedStale)
	}
	if got.ContextLimitNotified != state.ContextLimitNotified {
		t.Fatalf("ContextLimitNotified = %v, want %v", got.ContextLimitNotified, state.ContextLimitNotified)
	}
	if got.LastPipeMode != state.LastPipeMode {
		t.Fatalf("LastPipeMode = %v, want %v", got.LastPipeMode, state.LastPipeMode)
	}
	if got.Activity.Unix() != activity.Unix() {
		t.Fatalf("Activity = %v, want %v", got.Activity, activity)
	}
	if got.GuardAction != state.GuardAction {
		t.Fatalf("GuardAction = %q, want %q", got.GuardAction, state.GuardAction)
	}
	if got.GuardExpiresAt.Unix() != guardExpiry.Unix() {
		t.Fatalf("GuardExpiresAt = %v, want %v", got.GuardExpiresAt, guardExpiry)
	}
	if len(got.EventLog) != 0 {
		t.Fatalf("EventLog length = %d, want 0", len(got.EventLog))
	}
}

func TestWindowStateUserVarEmptyClearsState(t *testing.T) {
	state := WindowState{LastAction: "active", LastAccount: "work"}
	if err := state.UnmarshalUserVar(" \n\t "); err != nil {
		t.Fatalf("UnmarshalUserVar(empty) error: %v", err)
	}
	if !reflect.DeepEqual(state, WindowState{}) {
		t.Fatalf("UnmarshalUserVar(empty) = %+v, want zero state", state)
	}
}
