package indicator

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestRunWithDeps_ScreenOnlyContextLimitPromotesStopFailure(t *testing.T) {
	tabTitle := palette.BuildTitle("api", "bolt", false)
	kittyClient := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, tabTitle, "work", "claude"),
		screenText: "earlier line\n\n" +
			"Context limit reached · /compact or /clear to continue\n\n" +
			"✻ Cooked for 17m 6s\n",
	}
	daemonClient := &fakeIndicatorDaemonClient{
		resp: daemon.Response{OK: true, Allowed: true, IconState: "error"},
	}

	err := runWithDeps(
		"Notification",
		42,
		"/tmp/amx",
		HookPayload{NotificationType: "agent_idle"},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(daemonClient.reqs) != 1 {
		t.Fatalf("daemon requests = %d, want 1", len(daemonClient.reqs))
	}
	req := daemonClient.reqs[0]
	if req.Action != "stop_failure" {
		t.Fatalf("request action = %q, want stop_failure", req.Action)
	}
	if !req.ContextLimit {
		t.Fatal("request context_limit = false, want true")
	}
	if req.SessionTitle != "api" {
		t.Fatalf("request session_title = %q, want api", req.SessionTitle)
	}
	if req.Account != "work" {
		t.Fatalf("request account = %q, want work", req.Account)
	}
	if req.Harness != "claude" {
		t.Fatalf("request harness = %q, want claude", req.Harness)
	}
	if req.CurrentIconState != "bolt" {
		t.Fatalf("request current_icon_state = %q, want bolt", req.CurrentIconState)
	}
	if req.NotificationType != "agent_idle" {
		t.Fatalf("request notification_type = %q, want agent_idle", req.NotificationType)
	}
	if len(kittyClient.getTextCalls) != 1 {
		t.Fatalf("GetText calls = %d, want 1", len(kittyClient.getTextCalls))
	}
	if kittyClient.getTextCalls[0] != (getTextCall{match: "id:42", extent: "screen"}) {
		t.Fatalf("GetText call = %+v, want id:42/screen", kittyClient.getTextCalls[0])
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(kittyClient.setTitleCalls))
	}
	wantTitle := palette.BuildTitle("api", "error", false)
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:42", title: wantTitle}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:42/%q", kittyClient.setTitleCalls[0], wantTitle)
	}
}

func TestRunWithDeps_DaemonDownFallbackStillShowsContextLimitError(t *testing.T) {
	tabTitle := palette.BuildTitle("api", "bolt", false)
	kittyClient := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, tabTitle, "work", "claude"),
		screenText: "earlier line\n\n" +
			"Context limit reached · /compact or /clear to continue\n\n" +
			"✻ Cooked for 17m 6s\n",
	}
	daemonClient := &fakeIndicatorDaemonClient{err: errors.New("daemon down")}

	err := runWithDeps(
		"Notification",
		42,
		"/tmp/amx",
		HookPayload{NotificationType: "agent_idle"},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(daemonClient.reqs) != 1 {
		t.Fatalf("daemon requests = %d, want 1", len(daemonClient.reqs))
	}
	if daemonClient.reqs[0].Action != "stop_failure" || !daemonClient.reqs[0].ContextLimit {
		t.Fatalf("daemon request = %+v, want stop_failure with context_limit", daemonClient.reqs[0])
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(kittyClient.setTitleCalls))
	}
	wantTitle := palette.BuildTitle("api", "error", false)
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:42", title: wantTitle}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:42/%q", kittyClient.setTitleCalls[0], wantTitle)
	}
}
