package indicator

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
)

// TestIndicator_ReturnsNilWhenTabMissingButDaemonSucceeded locks in the
// contract that, once the daemon has recorded the event and resolved guard
// state, a missing Kitty tab is not an error from the indicator's perspective.
// Prior to this, the indicator returned an error when the tab lookup came up
// empty post-daemon-update, which Copilot CLI surfaced as
// `Hook execution failed: HookExitCodeError` on every such call — noisy for
// any event and outright harmful for permissionRequest, where a non-zero exit
// could be treated as deny. The title update is purely cosmetic at that point
// and is safe to skip.
func TestIndicator_ReturnsNilWhenTabMissingButDaemonSucceeded(t *testing.T) {
	if err := finalizeTitleUpdate(nil, 42, "bolt", nil); err != nil {
		t.Errorf("expected nil error when tab is missing, got %v", err)
	}
}

// TestDaemonDownIconState_PreservesUnacknowledgedIcons locks in the daemon-down
// fallback contract: a bare idle/clear hook must not wipe a still-relevant
// permission/stop/warning/error/elicitation icon when the daemon is
// unreachable. The regression scenario this guards against is a tab showing the
// permission key after Claude asks for approval, then the user's daemon being
// down when the next Notification (empty type → idle) fires — under the
// previous code the key icon was wiped and the user lost the visual cue that
// approval was needed. Active/compact/subagent icons still clear on idle
// because they represent an in-flight agent that has now finished.
func TestDaemonDownIconState_PreservesUnacknowledgedIcons(t *testing.T) {
	cases := []struct {
		name      string
		action    string
		current   string
		harness   string
		preserves bool
		resetFlow bool
		wantNext  string
		wantSkip  bool
	}{
		{"idle clears bolt", "idle", "bolt", "", false, false, "", false},
		{"idle clears compact for Claude", "idle", "compact", "claude", false, false, "", false},
		{"idle promotes Copilot compact to stop", "idle", "compact", "copilot", false, false, "stop", false},
		{"clear promotes Copilot compact to stop", "clear", "compact", "copilot", false, false, "stop", false},
		{"idle clears subagent", "idle", "subagent", "", false, false, "", false},
		{"idle preserves key", "idle", "key", "", false, false, "", true},
		{"idle preserves stop", "idle", "stop", "", false, false, "", true},
		{"idle preserves warning", "idle", "warning", "", false, false, "", true},
		{"idle preserves error", "idle", "error", "", false, false, "", true},
		{"idle preserves question", "idle", "question", "", false, false, "", true},
		{"clear preserves key", "clear", "key", "", false, false, "", true},
		{"clear clears bolt", "clear", "bolt", "", false, false, "", false},
		{"reset stop clears sticky icon", "stop", "stop", "", false, true, "", false},
		{"preserve-current child completion is a no-op", "active", "bolt", "", true, false, "", true},
		{"active writes bolt regardless of current", "active", "key", "", false, false, "bolt", false},
		{"permission writes key", "permission", "", "", false, false, "key", false},
		{"stop_failure writes error", "stop_failure", "bolt", "", false, false, "error", false},
		{"failure writes warning", "failure", "", "", false, false, "warning", false},
		{"idle with no current icon is a no-op", "idle", "", "", false, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, skip := daemonDownIconState(tc.action, tc.current, tc.preserves, tc.harness, tc.resetFlow)
			if next != tc.wantNext || skip != tc.wantSkip {
				t.Errorf("daemonDownIconState(%q, %q, %v) = (%q, %v), want (%q, %v)",
					tc.action, tc.current, tc.preserves, next, skip, tc.wantNext, tc.wantSkip)
			}
		})
	}
}

func TestBuildStateUpdateRequest_CarriesNormalizedDebugContext(t *testing.T) {
	req := buildStateUpdateRequest(
		42,
		"stop_failure",
		"notification",
		HookPayload{
			NotificationTypeCamel: "permission_prompt",
			StopReasonCamel:       "context_limit_reached",
			Reason:                "error",
			Source:                "clear",
			ToolNameCamel:         "Bash",
		},
		"W:api",
		"work",
		"copilot",
		"bolt",
		true,
		false,
		true,
		false,
		false,
	)

	want := daemon.Request{
		Type:             daemon.TypeStateUpdate,
		WindowID:         42,
		Action:           "stop_failure",
		Event:            "notification",
		ToolName:         "Bash",
		Source:           "clear",
		IsPrompt:         false,
		PreservesIcon:    false,
		Account:          "work",
		Harness:          "copilot",
		SessionTitle:     "W:api",
		PipeMode:         true,
		CurrentIconState: "bolt",
		NotificationType: "permission_prompt",
		StopReason:       "context_limit_reached",
		Reason:           "error",
		ContextLimit:     true,
		ResetFlow:        false,
	}
	if req != want {
		t.Fatalf("buildStateUpdateRequest() = %+v, want %+v", req, want)
	}
}

func TestBuildStateUpdateRequest_CarriesResetFlow(t *testing.T) {
	req := buildStateUpdateRequest(
		42,
		"active",
		"userPromptSubmitted",
		HookPayload{Prompt: "/clear worktree"},
		"W:api",
		"work",
		"copilot",
		"stop",
		false,
		false,
		false,
		true,
		false,
	)

	if !req.ResetFlow {
		t.Fatalf("buildStateUpdateRequest() reset_flow = false, want true: %+v", req)
	}
}

func TestShouldDelayResetSensitiveStop(t *testing.T) {
	cases := []struct {
		name        string
		event       string
		action      string
		payload     HookPayload
		contextStop bool
		resetFlow   bool
		want        bool
	}{
		{name: "claude stop waits", event: "Stop", action: "stop", want: true},
		{name: "copilot agent stop waits", event: "agentStop", action: "stop", want: true},
		{name: "session end waits", event: "sessionEnd", action: "stop", want: true},
		{name: "agent idle notification waits", event: "notification", action: "stop", payload: HookPayload{NotificationType: "agent_idle"}, want: true},
		{name: "agent idle notification type is normalized", event: "notification", action: "stop", payload: HookPayload{NotificationTypeCamel: " Agent_Idle "}, want: true},
		{name: "post compact does not wait", event: "PostCompact", action: "stop", want: false},
		{name: "failure stop does not wait", event: "Stop", action: "stop", contextStop: true, want: false},
		{name: "known reset stop does not wait", event: "Stop", action: "stop", resetFlow: true, want: false},
		{name: "non-stop action does not wait", event: "userPromptSubmitted", action: "active", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldDelayResetSensitiveStop(tc.event, tc.action, tc.payload, tc.contextStop, tc.resetFlow)
			if got != tc.want {
				t.Fatalf("shouldDelayResetSensitiveStop(%q, %q, %+v, %v, %v) = %v, want %v", tc.event, tc.action, tc.payload, tc.contextStop, tc.resetFlow, got, tc.want)
			}
		})
	}
}
