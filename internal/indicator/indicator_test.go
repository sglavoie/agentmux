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
// permission/stop/error/elicitation icon when the daemon is unreachable. The
// regression scenario this guards against is a tab showing the permission key
// after Claude asks for approval, then the user's daemon being down when the
// next Notification (empty type → idle) fires — under the previous code the
// key icon was wiped and the user lost the visual cue that approval was
// needed. Active/compact/subagent icons still clear on idle because they
// represent an in-flight agent that has now finished.
func TestDaemonDownIconState_PreservesUnacknowledgedIcons(t *testing.T) {
	cases := []struct {
		name      string
		action    string
		current   string
		harness   string
		preserves bool
		wantNext  string
		wantSkip  bool
	}{
		{"idle clears bolt", "idle", "bolt", "", false, "", false},
		{"idle clears compact for Claude", "idle", "compact", "claude", false, "", false},
		{"idle promotes Copilot compact to stop", "idle", "compact", "copilot", false, "stop", false},
		{"clear promotes Copilot compact to stop", "clear", "compact", "copilot", false, "stop", false},
		{"idle clears subagent", "idle", "subagent", "", false, "", false},
		{"idle preserves key", "idle", "key", "", false, "", true},
		{"idle preserves stop", "idle", "stop", "", false, "", true},
		{"idle preserves error", "idle", "error", "", false, "", true},
		{"idle preserves question", "idle", "question", "", false, "", true},
		{"clear preserves key", "clear", "key", "", false, "", true},
		{"clear clears bolt", "clear", "bolt", "", false, "", false},
		{"preserve-current child completion is a no-op", "active", "bolt", "", true, "", true},
		{"active writes bolt regardless of current", "active", "key", "", false, "bolt", false},
		{"permission writes key", "permission", "", "", false, "key", false},
		{"stop_failure writes error", "stop_failure", "bolt", "", false, "error", false},
		{"failure writes error", "failure", "", "", false, "error", false},
		{"idle with no current icon is a no-op", "idle", "", "", false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, skip := daemonDownIconState(tc.action, tc.current, tc.preserves, tc.harness)
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
			ToolName:              "Bash",
		},
		"W:api",
		"work",
		"copilot",
		"bolt",
		true,
		false,
		true,
	)

	want := daemon.Request{
		Type:             daemon.TypeStateUpdate,
		WindowID:         42,
		Action:           "stop_failure",
		Event:            "notification",
		ToolName:         "Bash",
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
	}
	if req != want {
		t.Fatalf("buildStateUpdateRequest() = %+v, want %+v", req, want)
	}
}
