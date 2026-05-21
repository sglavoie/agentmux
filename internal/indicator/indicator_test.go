package indicator

import "testing"

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
		name        string
		action      string
		current     string
		wantNext    string
		wantSkip    bool
	}{
		{"idle clears bolt", "idle", "bolt", "", false},
		{"idle clears compact", "idle", "compact", "", false},
		{"idle clears subagent", "idle", "subagent", "", false},
		{"idle preserves key", "idle", "key", "", true},
		{"idle preserves stop", "idle", "stop", "", true},
		{"idle preserves error", "idle", "error", "", true},
		{"idle preserves question", "idle", "question", "", true},
		{"clear preserves key", "clear", "key", "", true},
		{"clear clears bolt", "clear", "bolt", "", false},
		{"active writes bolt regardless of current", "active", "key", "bolt", false},
		{"permission writes key", "permission", "", "key", false},
		{"stop_failure writes error", "stop_failure", "bolt", "error", false},
		{"failure writes error", "failure", "", "error", false},
		{"idle with no current icon is a no-op", "idle", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, skip := daemonDownIconState(tc.action, tc.current)
			if next != tc.wantNext || skip != tc.wantSkip {
				t.Errorf("daemonDownIconState(%q, %q) = (%q, %v), want (%q, %v)",
					tc.action, tc.current, next, skip, tc.wantNext, tc.wantSkip)
			}
		})
	}
}
