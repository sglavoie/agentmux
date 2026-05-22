package indicator

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type fakeIndicatorKittyClient struct {
	windows       []kitty.OSWindow
	listErr       error
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
