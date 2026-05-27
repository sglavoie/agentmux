package indicator

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestRunWithDeps_ClaudeClearSequenceUsesLifecycleResetSignals(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 40 * time.Millisecond
	t.Cleanup(func() {
		resetFlowStopDelay = oldDelay
	})

	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}

	boltTitle := palette.BuildTitle("api", "bolt", false)
	stopTitle := palette.BuildTitle("api", "stop", false)
	clearTitle := palette.BuildTitle("api", "", false)

	stopKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, boltTitle, "work", "claude"),
	}
	sessionEndKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, boltTitle, "work", "claude"),
	}
	sessionStartKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, clearTitle, "work", "claude"),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithDeps("Stop", 42, "/tmp/amx", HookPayload{}, false, stopKitty, daemonClient)
	}()

	time.Sleep(resetFlowStopDelay / 4)
	if err := runWithDeps("SessionEnd", 42, "/tmp/amx", HookPayload{Reason: "clear"}, false, sessionEndKitty, daemonClient); err != nil {
		t.Fatalf("SessionEnd(reset) error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if err := runWithDeps("SessionStart", 42, "/tmp/amx", HookPayload{Source: "clear"}, false, sessionStartKitty, daemonClient); err != nil {
		t.Fatalf("SessionStart(clear) error = %v", err)
	}

	reqs := daemonClient.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("daemon requests = %d, want 3 (%+v)", len(reqs), reqs)
	}
	wantReqs := []struct {
		event string
		reset bool
	}{
		{event: "SessionEnd", reset: true},
		{event: "Stop", reset: false},
		{event: "SessionStart", reset: true},
	}
	for i, want := range wantReqs {
		if reqs[i].Event != want.event || reqs[i].ResetFlow != want.reset {
			t.Fatalf("request[%d] = %+v, want event=%q reset_flow=%v", i, reqs[i], want.event, want.reset)
		}
	}

	calls := append([]setTitleCall{}, stopKitty.setTitleCalls...)
	calls = append(calls, sessionEndKitty.setTitleCalls...)
	calls = append(calls, sessionStartKitty.setTitleCalls...)
	if len(calls) == 0 {
		t.Fatal("expected at least one title update")
	}
	for _, call := range calls {
		if call.title == stopTitle {
			t.Fatalf("Claude /clear should suppress stop title, got %+v", calls)
		}
		if call.title != clearTitle {
			t.Fatalf("unexpected Claude /clear title update %q, want only %q", call.title, clearTitle)
		}
	}
}

func TestRunWithDeps_CopilotClearSequenceUsesPromptAndSessionStartResetSignals(t *testing.T) {
	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}

	stopTitle := palette.BuildTitle("api", "stop", false)
	boltTitle := palette.BuildTitle("api", "bolt", false)
	clearTitle := palette.BuildTitle("api", "", false)

	promptKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, stopTitle, "work", "copilot"),
	}
	agentStopKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, boltTitle, "work", "copilot"),
	}
	sessionEndKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, clearTitle, "work", "copilot"),
	}
	sessionStartKitty := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, clearTitle, "work", "copilot"),
	}

	for _, step := range []struct {
		event   string
		payload HookPayload
		kitty   *fakeIndicatorKittyClient
	}{
		{
			event:   "userPromptSubmitted",
			payload: HookPayload{Prompt: "/clear workspace"},
			kitty:   promptKitty,
		},
		{
			event: "agentStop",
			kitty: agentStopKitty,
		},
		{
			event: "sessionEnd",
			kitty: sessionEndKitty,
		},
		{
			event:   "sessionStart",
			payload: HookPayload{Source: "new"},
			kitty:   sessionStartKitty,
		},
	} {
		if err := runWithDeps(step.event, 42, "/tmp/amx", step.payload, false, step.kitty, daemonClient); err != nil {
			t.Fatalf("%s error = %v", step.event, err)
		}
	}

	reqs := daemonClient.snapshot()
	if len(reqs) != 4 {
		t.Fatalf("daemon requests = %d, want 4 (%+v)", len(reqs), reqs)
	}
	wantReqs := []struct {
		event string
		reset bool
	}{
		{event: "userPromptSubmitted", reset: true},
		{event: "agentStop", reset: false},
		{event: "sessionEnd", reset: false},
		{event: "sessionStart", reset: true},
	}
	for i, want := range wantReqs {
		if reqs[i].Event != want.event || reqs[i].ResetFlow != want.reset {
			t.Fatalf("request[%d] = %+v, want event=%q reset_flow=%v", i, reqs[i], want.event, want.reset)
		}
	}

	calls := append([]setTitleCall{}, promptKitty.setTitleCalls...)
	calls = append(calls, agentStopKitty.setTitleCalls...)
	calls = append(calls, sessionEndKitty.setTitleCalls...)
	calls = append(calls, sessionStartKitty.setTitleCalls...)
	if len(calls) == 0 {
		t.Fatal("expected Copilot /clear to update the title")
	}
	var sawBolt, sawClear bool
	for _, call := range calls {
		switch call.title {
		case boltTitle:
			sawBolt = true
		case clearTitle:
			sawClear = true
		case stopTitle:
			t.Fatalf("Copilot /clear should suppress stop title, got %+v", calls)
		default:
			t.Fatalf("unexpected Copilot /clear title update %q", call.title)
		}
	}
	if !sawBolt || !sawClear {
		t.Fatalf("expected Copilot /clear to show activity then clear, got %+v", calls)
	}
}

func TestRunWithDeps_CopilotUserExitSessionEndClearsImmediately(t *testing.T) {
	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}

	boltTitle := palette.BuildTitle("api", "bolt", false)
	stopTitle := palette.BuildTitle("api", "stop", false)
	clearTitle := palette.BuildTitle("api", "", false)

	kittyClient := &fakeIndicatorKittyClient{
		windows: fakeIndicatorWindows(42, boltTitle, "work", "copilot"),
	}

	if err := runWithDeps("sessionEnd", 42, "/tmp/amx", HookPayload{Reason: "user_exit"}, false, kittyClient, daemonClient); err != nil {
		t.Fatalf("sessionEnd(user_exit) error = %v", err)
	}

	reqs := daemonClient.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("daemon requests = %d, want 1 (%+v)", len(reqs), reqs)
	}
	if reqs[0].Event != "sessionEnd" || !reqs[0].ResetFlow {
		t.Fatalf("request = %+v, want event=sessionEnd reset_flow=true", reqs[0])
	}

	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("title updates = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if got := kittyClient.setTitleCalls[0].title; got != clearTitle {
		t.Fatalf("sessionEnd(user_exit) title = %q, want %q (and never %q)", got, clearTitle, stopTitle)
	}
}

func TestRunWithDeps_NonKittyHookIsSilentNoOp(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")
	t.Setenv("KITTY_WINDOW_ID", "")

	kittyClient := &fakeIndicatorKittyClient{}
	daemonClient := &fakeIndicatorDaemonClient{}

	err := runWithDeps(
		"sessionStart",
		0,
		"/tmp/amx",
		HookPayload{},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if kittyClient.listCalls != 0 {
		t.Fatalf("ListWindows calls = %d, want 0", kittyClient.listCalls)
	}
	if len(kittyClient.getTextCalls) != 0 {
		t.Fatalf("GetText calls = %d, want 0", len(kittyClient.getTextCalls))
	}
	if len(kittyClient.setTitleCalls) != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", len(kittyClient.setTitleCalls))
	}
	if len(daemonClient.reqs) != 0 {
		t.Fatalf("daemon requests = %d, want 0", len(daemonClient.reqs))
	}
}

func TestRunWithDeps_MissingWindowIDInsideKittyErrors(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("KITTY_WINDOW_ID", "")

	kittyClient := &fakeIndicatorKittyClient{}
	daemonClient := &fakeIndicatorDaemonClient{}

	err := runWithDeps(
		"sessionStart",
		0,
		"/tmp/amx",
		HookPayload{},
		false,
		kittyClient,
		daemonClient,
	)
	if err == nil {
		t.Fatal("runWithDeps() error = nil, want missing KITTY_WINDOW_ID error")
	}
	if got := err.Error(); got != "indicator: KITTY_WINDOW_ID is not set" {
		t.Fatalf("runWithDeps() error = %q, want %q", got, "indicator: KITTY_WINDOW_ID is not set")
	}
	if kittyClient.listCalls != 0 {
		t.Fatalf("ListWindows calls = %d, want 0", kittyClient.listCalls)
	}
	if len(daemonClient.reqs) != 0 {
		t.Fatalf("daemon requests = %d, want 0", len(daemonClient.reqs))
	}
}

func TestRunWithDeps_InvalidWindowIDInsideKittyErrors(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("KITTY_WINDOW_ID", "not-a-number")

	kittyClient := &fakeIndicatorKittyClient{}
	daemonClient := &fakeIndicatorDaemonClient{}

	err := runWithDeps(
		"sessionStart",
		0,
		"/tmp/amx",
		HookPayload{},
		false,
		kittyClient,
		daemonClient,
	)
	if err == nil {
		t.Fatal("runWithDeps() error = nil, want invalid KITTY_WINDOW_ID error")
	}
	if got := err.Error(); got != `indicator: invalid KITTY_WINDOW_ID "not-a-number"` {
		t.Fatalf("runWithDeps() error = %q, want %q", got, `indicator: invalid KITTY_WINDOW_ID "not-a-number"`)
	}
	if kittyClient.listCalls != 0 {
		t.Fatalf("ListWindows calls = %d, want 0", kittyClient.listCalls)
	}
	if len(daemonClient.reqs) != 0 {
		t.Fatalf("daemon requests = %d, want 0", len(daemonClient.reqs))
	}
}
