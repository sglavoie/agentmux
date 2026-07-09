package indicator

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestRunWithDeps_UnmanagedSiblingNoOpsWhenTabHasManagedOwner(t *testing.T) {
	cases := []struct {
		name        string
		event       string
		payload     HookPayload
		harnessName string
	}{
		{
			name:        "claude notification",
			event:       "Notification",
			payload:     HookPayload{NotificationType: "agent_idle"},
			harnessName: harness.Claude,
		},
		{
			name:        "copilot stop",
			event:       "agentStop",
			payload:     HookPayload{StopReasonCamel: "end_turn"},
			harnessName: harness.Copilot,
		},
		{
			name:        "codex stop",
			event:       "Stop",
			payload:     HookPayload{StopReason: "end_turn"},
			harnessName: harness.Codex,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kittyClient := &fakeIndicatorKittyClient{
				windows: ownershipTestWindows("api", []kitty.Window{
					{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile: "work",
							Harness: tc.harnessName,
						},
					},
					{ID: 43, IsActive: true},
				}),
				screenText: "Context limit reached\n/compact or /clear to continue\n",
			}
			daemonClient := &fakeIndicatorDaemonClient{
				resp: daemon.Response{OK: true, Allowed: true, IconState: "stop"},
			}

			if err := runWithDeps(tc.event, 43, "/tmp/amx", tc.payload, false, kittyClient, daemonClient); err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}

			assertOwnershipNoOp(t, kittyClient, daemonClient)
		})
	}
}

func TestRunWithDeps_ManagedNonOwnerNoOps(t *testing.T) {
	cases := []struct {
		name        string
		event       string
		harnessName string
	}{
		{name: "claude", event: "PreToolUse", harnessName: harness.Claude},
		{name: "copilot", event: "preToolUse", harnessName: harness.Copilot},
		{name: "codex", event: "PostToolUse", harnessName: harness.Codex},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kittyClient := &fakeIndicatorKittyClient{
				windows: ownershipTestWindows("api", []kitty.Window{
					{
						ID:       42,
						IsActive: true,
						UserVars: kitty.UserVars{
							Profile: "work",
							Harness: tc.harnessName,
						},
					},
					{
						ID: 43,
						UserVars: kitty.UserVars{
							Profile: "scratch",
							Harness: tc.harnessName,
						},
					},
				}),
			}
			daemonClient := &fakeIndicatorDaemonClient{
				resp: daemon.Response{OK: true, Allowed: true, IconState: "bolt"},
			}

			if err := runWithDeps(tc.event, 43, "/tmp/amx", HookPayload{}, false, kittyClient, daemonClient); err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}

			assertOwnershipNoOp(t, kittyClient, daemonClient)
		})
	}
}

func TestRunWithDeps_TabTitleOwnerStillUpdates(t *testing.T) {
	cases := []struct {
		name        string
		event       string
		harnessName string
	}{
		{name: "claude", event: "PreToolUse", harnessName: harness.Claude},
		{name: "copilot", event: "preToolUse", harnessName: harness.Copilot},
		{name: "codex", event: "PostToolUse", harnessName: harness.Codex},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kittyClient := &fakeIndicatorKittyClient{
				windows: ownershipTestWindows("api", []kitty.Window{
					{
						ID:       42,
						IsActive: true,
						UserVars: kitty.UserVars{
							Profile: "work",
							Harness: tc.harnessName,
						},
					},
					{ID: 43},
				}),
			}
			daemonClient := &fakeIndicatorDaemonClient{
				resp: daemon.Response{OK: true, Allowed: true, IconState: "bolt"},
			}

			if err := runWithDeps(tc.event, 42, "/tmp/amx", HookPayload{}, false, kittyClient, daemonClient); err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}

			assertOwnershipUpdate(t, kittyClient, daemonClient, 42, tc.event, tc.harnessName)
		})
	}
}

func TestRunWithDeps_StandaloneUnmanagedStillUpdates(t *testing.T) {
	cases := []struct {
		name  string
		event string
	}{
		{name: "claude", event: "PreToolUse"},
		{name: "copilot", event: "preToolUse"},
		{name: "codex", event: "PostToolUse"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kittyClient := &fakeIndicatorKittyClient{
				windows: ownershipTestWindows("api", []kitty.Window{{ID: 42, IsActive: true}}),
			}
			daemonClient := &fakeIndicatorDaemonClient{
				resp: daemon.Response{OK: true, Allowed: true, IconState: "bolt"},
			}

			if err := runWithDeps(tc.event, 42, "/tmp/amx", HookPayload{}, false, kittyClient, daemonClient); err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}

			assertOwnershipUpdate(t, kittyClient, daemonClient, 42, tc.event, "")
		})
	}
}

func ownershipTestWindows(tabTitle string, windows []kitty.Window) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:      100,
			Title:   tabTitle,
			Windows: windows,
		}},
	}}
}

func assertOwnershipNoOp(t *testing.T, kittyClient *fakeIndicatorKittyClient, daemonClient *fakeIndicatorDaemonClient) {
	t.Helper()
	if kittyClient.listCalls != 1 {
		t.Fatalf("ListWindows calls = %d, want 1", kittyClient.listCalls)
	}
	if len(kittyClient.getTextCalls) != 0 {
		t.Fatalf("GetText calls = %d, want 0", len(kittyClient.getTextCalls))
	}
	if len(kittyClient.setTitleCalls) != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if len(daemonClient.reqs) != 0 {
		t.Fatalf("daemon requests = %d, want 0 (%+v)", len(daemonClient.reqs), daemonClient.reqs)
	}
}

func assertOwnershipUpdate(t *testing.T, kittyClient *fakeIndicatorKittyClient, daemonClient *fakeIndicatorDaemonClient, windowID int, event, harnessName string) {
	t.Helper()
	if len(daemonClient.reqs) != 1 {
		t.Fatalf("daemon requests = %d, want 1 (%+v)", len(daemonClient.reqs), daemonClient.reqs)
	}
	req := daemonClient.reqs[0]
	if req.WindowID != windowID || req.Event != event || req.Action != "active" {
		t.Fatalf("daemon request = %+v, want window=%d event=%q action=active", req, windowID, event)
	}
	if req.Harness != harnessName {
		t.Fatalf("request harness = %q, want %q", req.Harness, harnessName)
	}

	wantTitle := palette.BuildTitle("api", "bolt", false)
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: kitty.MatchTabByWindowID(windowID), title: wantTitle}) {
		t.Fatalf("SetTabTitle call = %+v, want window %d title %q", kittyClient.setTitleCalls[0], windowID, wantTitle)
	}
}
