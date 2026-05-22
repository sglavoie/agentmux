package indicator

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type fakeIndicatorKittyClient struct {
	windows       []kitty.OSWindow
	listErr       error
	listCalls     int
	screenText    string
	getTextErr    error
	setTitleErr   error
	getTextCalls  []getTextCall
	setTitleCalls []setTitleCall
}

type getTextCall struct {
	match  string
	extent string
}

type setTitleCall struct {
	match string
	title string
}

func (f *fakeIndicatorKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]kitty.OSWindow, len(f.windows))
	copy(out, f.windows)
	return out, nil
}

func (f *fakeIndicatorKittyClient) GetText(matchExpr, extent string) (string, error) {
	f.getTextCalls = append(f.getTextCalls, getTextCall{match: matchExpr, extent: extent})
	if f.getTextErr != nil {
		return "", f.getTextErr
	}
	return f.screenText, nil
}

func (f *fakeIndicatorKittyClient) SetTabTitle(matchExpr, title string) error {
	f.setTitleCalls = append(f.setTitleCalls, setTitleCall{match: matchExpr, title: title})
	return f.setTitleErr
}

type fakeIndicatorDaemonClient struct {
	resp    daemon.Response
	err     error
	reqs    []daemon.Request
	amxBins []string
}

func (f *fakeIndicatorDaemonClient) SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error) {
	f.reqs = append(f.reqs, req)
	f.amxBins = append(f.amxBins, amxBin)
	if f.err != nil {
		return daemon.Response{}, f.err
	}
	return f.resp, nil
}

type handlerBackedIndicatorDaemonClient struct {
	mu   sync.Mutex
	h    *daemon.Handler
	reqs []daemon.Request
}

func (f *handlerBackedIndicatorDaemonClient) SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	return f.h.Handle(req), nil
}

func (f *handlerBackedIndicatorDaemonClient) snapshot() []daemon.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]daemon.Request, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func fakeIndicatorWindows(windowID int, tabTitle, profile, harnessName string) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: tabTitle,
					Windows: []kitty.Window{
						{
							ID: windowID,
							UserVars: kitty.UserVars{
								Profile: profile,
								Harness: harnessName,
							},
						},
					},
				},
			},
		},
	}
}

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
	)

	if !req.ResetFlow {
		t.Fatalf("buildStateUpdateRequest() reset_flow = false, want true: %+v", req)
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
