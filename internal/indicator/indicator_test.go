package indicator

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
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

func TestRunWithDeps_AgentSessionLoopFinishedReconcilesLoopMarker(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	discoverErr := errors.New("discover failed")
	boltLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		State:   "bolt",
		Looping: true,
	})
	keyLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		State:   "key",
		Looping: true,
	})
	loopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		Looping: true,
	})
	keyTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:  "api",
		State: "key",
	})

	cases := []struct {
		name        string
		title       string
		directory   string
		candidates  []agentloop.Candidate
		discoverErr error
		wantTitle   string
	}{
		{
			name:      "clears active state and loop marker when no loop remains",
			title:     boltLoopTitle,
			directory: dir,
			wantTitle: "api",
		},
		{
			name:       "clears active state and keeps loop marker when another loop remains",
			title:      boltLoopTitle,
			directory:  dir,
			candidates: []agentloop.Candidate{{CWD: dir}, {CWD: otherDir}},
			wantTitle:  loopTitle,
		},
		{
			name:      "preserves sticky key state while clearing loop marker",
			title:     keyLoopTitle,
			directory: dir,
			wantTitle: keyTitle,
		},
		{
			name:        "preserves loop marker on discovery error",
			title:       boltLoopTitle,
			directory:   dir,
			discoverErr: discoverErr,
			wantTitle:   loopTitle,
		},
		{
			name:      "preserves loop marker when directory is missing",
			title:     boltLoopTitle,
			wantTitle: loopTitle,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubIndicatorLoopDiscover(t, tc.candidates, tc.discoverErr)

			handler := daemon.NewHandler(
				daemon.NewStateGuard(time.Hour),
				daemon.NewNotifier(&config.NotificationConfig{}),
			)
			daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
			kittyClient := &fakeIndicatorKittyClient{
				windows: fakeIndicatorWindowsWithDirectory(42, tc.title, "work", "claude", tc.directory),
			}

			err := runWithDeps(
				"AgentSessionLoopFinished",
				42,
				"/tmp/amx",
				HookPayload{Source: "agent_session_loop", Reason: "run_finished"},
				false,
				kittyClient,
				daemonClient,
			)
			if err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}
			if len(kittyClient.setTitleCalls) != 1 {
				t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
			}
			if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:42", title: tc.wantTitle}) {
				t.Fatalf("SetTabTitle call = %+v, want window_id:42/%q", kittyClient.setTitleCalls[0], tc.wantTitle)
			}
		})
	}
}

func TestRunWithDeps_AgentSessionLoopFinishedUsesPayloadCWDForSplitTab(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	boltLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		State:   "bolt",
		Looping: true,
	})
	loopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		Looping: true,
	})

	cases := []struct {
		name       string
		payloadCWD string
		candidates []agentloop.Candidate
		wantTitle  string
		wantMatch  string
	}{
		{
			name:       "clears loop marker from loop pane without user vars",
			payloadCWD: dir,
			wantTitle:  "api",
			wantMatch:  "window_id:41",
		},
		{
			name:       "keeps loop marker when another active loop remains",
			payloadCWD: dir,
			candidates: []agentloop.Candidate{{CWD: dir}},
			wantTitle:  loopTitle,
			wantMatch:  "window_id:41",
		},
		{
			name:       "preserves loop marker when payload cwd belongs elsewhere",
			payloadCWD: otherDir,
			wantTitle:  loopTitle,
			wantMatch:  "window_id:42",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubIndicatorLoopDiscover(t, tc.candidates, nil)
			handler := daemon.NewHandler(
				daemon.NewStateGuard(time.Hour),
				daemon.NewNotifier(&config.NotificationConfig{}),
			)
			daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
			kittyClient := &fakeIndicatorKittyClient{
				windows: []kitty.OSWindow{{
					ID: 1,
					Tabs: []kitty.Tab{{
						ID:    100,
						Title: boltLoopTitle,
						Windows: []kitty.Window{
							{
								ID: 41,
								UserVars: kitty.UserVars{
									Profile:      "work",
									Harness:      "claude",
									AMXDirectory: dir,
								},
							},
							{
								ID:       42,
								IsActive: true,
							},
						},
					}},
				}},
			}

			err := runWithDeps(
				"AgentSessionLoopFinished",
				42,
				"/tmp/amx",
				HookPayload{Source: "agent_session_loop", Reason: "run_finished", CWD: tc.payloadCWD},
				false,
				kittyClient,
				daemonClient,
			)
			if err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}
			if len(kittyClient.setTitleCalls) != 1 {
				t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
			}
			if kittyClient.setTitleCalls[0] != (setTitleCall{match: tc.wantMatch, title: tc.wantTitle}) {
				t.Fatalf("SetTabTitle call = %+v, want %s/%q", kittyClient.setTitleCalls[0], tc.wantMatch, tc.wantTitle)
			}
		})
	}
}

func TestRunWithDeps_AgentSessionLoopStartedTargetsPayloadWorkspace(t *testing.T) {
	targetDir := t.TempDir()
	callerDir := t.TempDir()
	targetTitle := "Trad-X"
	wantTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "Trad-X",
		Looping: true,
	})

	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
	kittyClient := &fakeIndicatorKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: "scripts",
					Windows: []kitty.Window{{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile:      "scripts",
							Harness:      "claude",
							AMXDirectory: callerDir,
						},
					}},
				},
				{
					ID:    101,
					Title: targetTitle,
					Windows: []kitty.Window{{
						ID: 101,
						UserVars: kitty.UserVars{
							Profile:      "work",
							Harness:      "codex",
							AMXDirectory: targetDir,
						},
					}},
				},
			},
		}},
	}

	err := runWithDeps(
		"AgentSessionLoopStarted",
		42,
		"/tmp/amx",
		HookPayload{Source: "agent_session_loop", CWD: targetDir, Harness: "codex"},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:101", title: wantTitle}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:101/%q", kittyClient.setTitleCalls[0], wantTitle)
	}
	reqs := daemonClient.snapshot()
	if len(reqs) != 1 || reqs[0].WindowID != 101 || reqs[0].Event != "AgentSessionLoopStarted" || reqs[0].Action != "loop_marker" {
		t.Fatalf("daemon requests = %+v, want one loop_marker AgentSessionLoopStarted request for window 101", reqs)
	}
}

func TestRunWithDeps_AgentSessionLoopStartedTargetsPayloadWindowIDBeforeWorkspace(t *testing.T) {
	targetDir := t.TempDir()
	worktreeDir := t.TempDir()
	callerDir := t.TempDir()
	wantTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "Trad-X",
		Looping: true,
	})

	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
	kittyClient := &fakeIndicatorKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: "scripts",
					Windows: []kitty.Window{{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile:      "scripts",
							Harness:      "claude",
							AMXDirectory: callerDir,
						},
					}},
				},
				{
					ID:    101,
					Title: "Trad-X",
					Windows: []kitty.Window{{
						ID: 101,
						UserVars: kitty.UserVars{
							Profile:      "work",
							Harness:      "codex",
							AMXDirectory: targetDir,
						},
					}},
				},
			},
		}},
	}

	err := runWithDeps(
		"AgentSessionLoopStarted",
		42,
		"/tmp/amx",
		HookPayload{Source: "agent_session_loop", CWD: worktreeDir, Harness: "codex", KittyWindowID: 101},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:101", title: wantTitle}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:101/%q", kittyClient.setTitleCalls[0], wantTitle)
	}
	reqs := daemonClient.snapshot()
	if len(reqs) != 1 || reqs[0].WindowID != 101 || reqs[0].Event != "AgentSessionLoopStarted" || reqs[0].Action != "loop_marker" {
		t.Fatalf("daemon requests = %+v, want one loop_marker AgentSessionLoopStarted request for window 101", reqs)
	}
}

func TestRunWithDeps_AgentSessionLoopStartedUsesStatePathHarnessPreference(t *testing.T) {
	targetDir := t.TempDir()
	callerDir := t.TempDir()

	cases := []struct {
		name  string
		event string
		state map[string]any
	}{
		{
			name:  "top-level harness selects non-default project tab",
			event: "AgentSessionLoopStarted",
			state: map[string]any{
				"cwd":     targetDir,
				"harness": "copilot",
			},
		},
		{
			name:  "active-turn harness overrides top-level harness",
			event: "AgentSessionLoopTurnStarted",
			state: map[string]any{
				"cwd":     targetDir,
				"harness": "codex",
				"active_turn": map[string]any{
					"harness": "copilot",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statePath := writeLoopStateFile(t, tc.state)
			wantTitle := palette.BuildTitleParts(palette.TitleParts{
				Base:    "project-copilot",
				Looping: true,
			})
			handler := daemon.NewHandler(
				daemon.NewStateGuard(time.Hour),
				daemon.NewNotifier(&config.NotificationConfig{}),
			)
			daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
			kittyClient := &fakeIndicatorKittyClient{
				windows: []kitty.OSWindow{{
					ID: 1,
					Tabs: []kitty.Tab{
						{
							ID:    100,
							Title: "scripts",
							Windows: []kitty.Window{{
								ID: 42,
								UserVars: kitty.UserVars{
									Profile:      "scripts",
									Harness:      "claude",
									AMXDirectory: callerDir,
								},
							}},
						},
						{
							ID:    101,
							Title: "project-codex",
							Windows: []kitty.Window{{
								ID: 101,
								UserVars: kitty.UserVars{
									Profile:      "work-codex",
									Harness:      "codex",
									AMXDirectory: targetDir,
								},
							}},
						},
						{
							ID:    102,
							Title: "project-copilot",
							Windows: []kitty.Window{{
								ID: 102,
								UserVars: kitty.UserVars{
									Profile:      "work-copilot",
									Harness:      "copilot",
									AMXDirectory: targetDir,
								},
							}},
						},
					},
				}},
			}

			err := runWithDeps(
				tc.event,
				42,
				"/tmp/amx",
				HookPayload{Source: "agent_session_loop", StatePath: statePath},
				false,
				kittyClient,
				daemonClient,
			)
			if err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}
			if len(kittyClient.setTitleCalls) != 1 {
				t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
			}
			if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:102", title: wantTitle}) {
				t.Fatalf("SetTabTitle call = %+v, want window_id:102/%q", kittyClient.setTitleCalls[0], wantTitle)
			}
			reqs := daemonClient.snapshot()
			if len(reqs) != 1 || reqs[0].WindowID != 102 || reqs[0].Event != tc.event || reqs[0].Action != "loop_marker" {
				t.Fatalf("daemon requests = %+v, want one loop_marker %s request for window 102", reqs, tc.event)
			}
		})
	}
}

func TestRunWithDeps_AgentSessionLoopFinishedTargetsPayloadWorkspaceOnly(t *testing.T) {
	targetDir := t.TempDir()
	callerDir := t.TempDir()
	codexLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "Trad-X",
		State:   "bolt",
		Looping: true,
	})
	claudeLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "Trad-X Claude",
		State:   "bolt",
		Looping: true,
	})

	stubIndicatorLoopDiscover(t, nil, nil)
	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
	kittyClient := &fakeIndicatorKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: "scripts",
					Windows: []kitty.Window{{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile:      "scripts",
							Harness:      "claude",
							AMXDirectory: callerDir,
						},
					}},
				},
				{
					ID:    101,
					Title: codexLoopTitle,
					Windows: []kitty.Window{{
						ID: 101,
						UserVars: kitty.UserVars{
							Profile:      "work-codex",
							Harness:      "codex",
							AMXDirectory: targetDir,
						},
					}},
				},
				{
					ID:    102,
					Title: claudeLoopTitle,
					Windows: []kitty.Window{{
						ID: 102,
						UserVars: kitty.UserVars{
							Profile:      "work-claude",
							Harness:      "claude",
							AMXDirectory: targetDir,
						},
					}},
				},
			},
		}},
	}

	err := runWithDeps(
		"AgentSessionLoopFinished",
		42,
		"/tmp/amx",
		HookPayload{Source: "agent_session_loop", Reason: "run_finished", CWD: targetDir, Harness: "codex"},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:101", title: "Trad-X"}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:101/Trad-X", kittyClient.setTitleCalls[0])
	}
	reqs := daemonClient.snapshot()
	if len(reqs) != 1 || reqs[0].WindowID != 101 || reqs[0].Event != "AgentSessionLoopFinished" || reqs[0].Action != "idle" {
		t.Fatalf("daemon requests = %+v, want one idle AgentSessionLoopFinished request for window 101", reqs)
	}
}

func TestRunWithDeps_AgentSessionLoopFinishedTargetsPayloadWindowIDBeforeWorkspace(t *testing.T) {
	targetDir := t.TempDir()
	worktreeDir := t.TempDir()
	callerDir := t.TempDir()
	codexLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "Trad-X",
		State:   "bolt",
		Looping: true,
	})
	copilotLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "Trad-X Copilot",
		State:   "bolt",
		Looping: true,
	})

	stubIndicatorLoopDiscover(t, nil, nil)
	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
	kittyClient := &fakeIndicatorKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: "scripts",
					Windows: []kitty.Window{{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile:      "scripts",
							Harness:      "claude",
							AMXDirectory: callerDir,
						},
					}},
				},
				{
					ID:    101,
					Title: codexLoopTitle,
					Windows: []kitty.Window{{
						ID: 101,
						UserVars: kitty.UserVars{
							Profile:      "work-codex",
							Harness:      "codex",
							AMXDirectory: targetDir,
						},
					}},
				},
				{
					ID:    102,
					Title: copilotLoopTitle,
					Windows: []kitty.Window{{
						ID: 102,
						UserVars: kitty.UserVars{
							Profile:      "work-copilot",
							Harness:      "copilot",
							AMXDirectory: targetDir,
						},
					}},
				},
			},
		}},
	}

	err := runWithDeps(
		"AgentSessionLoopFinished",
		42,
		"/tmp/amx",
		HookPayload{
			Source:        "agent_session_loop",
			Reason:        "run_finished",
			CWD:           worktreeDir,
			Harness:       "codex",
			KittyWindowID: 101,
		},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:101", title: "Trad-X"}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:101/Trad-X", kittyClient.setTitleCalls[0])
	}
	reqs := daemonClient.snapshot()
	if len(reqs) != 1 || reqs[0].WindowID != 101 || reqs[0].Event != "AgentSessionLoopFinished" || reqs[0].Action != "idle" {
		t.Fatalf("daemon requests = %+v, want one idle AgentSessionLoopFinished request for window 101", reqs)
	}
}

func TestRunWithDeps_AgentSessionLoopFinishedClearsNonDefaultProjectTabNotCaller(t *testing.T) {
	targetDir := t.TempDir()
	callerDir := t.TempDir()
	callerLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "scripts",
		State:   "bolt",
		Looping: true,
	})
	codexLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "project-codex",
		State:   "bolt",
		Looping: true,
	})
	copilotLoopTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "project-copilot",
		State:   "bolt",
		Looping: true,
	})

	stubIndicatorLoopDiscover(t, nil, nil)
	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
	kittyClient := &fakeIndicatorKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: callerLoopTitle,
					Windows: []kitty.Window{{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile:      "scripts",
							Harness:      "claude",
							AMXDirectory: callerDir,
						},
					}},
				},
				{
					ID:    101,
					Title: codexLoopTitle,
					Windows: []kitty.Window{{
						ID: 101,
						UserVars: kitty.UserVars{
							Profile:      "work-codex",
							Harness:      "codex",
							AMXDirectory: targetDir,
						},
					}},
				},
				{
					ID:    102,
					Title: copilotLoopTitle,
					Windows: []kitty.Window{{
						ID: 102,
						UserVars: kitty.UserVars{
							Profile:      "work-copilot",
							Harness:      "copilot",
							AMXDirectory: targetDir,
						},
					}},
				},
			},
		}},
	}

	err := runWithDeps(
		"AgentSessionLoopFinished",
		42,
		"/tmp/amx",
		HookPayload{
			Source:  "agent_session_loop",
			Reason:  "run_finished",
			CWD:     targetDir,
			Harness: "copilot",
		},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:102", title: "project-copilot"}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:102/project-copilot", kittyClient.setTitleCalls[0])
	}
	reqs := daemonClient.snapshot()
	if len(reqs) != 1 || reqs[0].WindowID != 102 || reqs[0].Event != "AgentSessionLoopFinished" || reqs[0].Action != "idle" {
		t.Fatalf("daemon requests = %+v, want one idle AgentSessionLoopFinished request for window 102", reqs)
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

// TestRunWithDeps_AgentSessionLoopStartedSplitTabLiveLayouts exercises the
// three live-shaped split-tab scenarios called out by the loop-marker plan:
//
//   - "scripts": the ASL pane was launched inside the project tab and copied
//     amx_* user vars from its parent, so it is a managed window itself.
//   - "LogScanner": the ASL pane lacks amx_* user vars; the project pane in
//     the same tab is the only managed window. Routing must fall back from the
//     payload's window ID to CWD/harness matching against the project pane.
//   - "sis": the tab already carries a sticky stop icon (no longer cleared by
//     a fresh active event because the start lifecycle is marker-only). The
//     existing state icon must survive while only 🔁 is added.
//
// All three shapes share the same expected daemon contract — exactly one
// loop_marker request — and the same title-update invariants: tab base name
// and existing state icon preserved, Looping flipped to true.
func TestRunWithDeps_AgentSessionLoopStartedSplitTabLiveLayouts(t *testing.T) {
	projectDir := t.TempDir()
	boltTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "bolt"})
	boltLoopTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "bolt", Looping: true})
	stopTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"})
	stopLoopTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Looping: true})

	projectPaneVars := kitty.UserVars{
		Profile:      "api",
		Harness:      "claude",
		AMXDirectory: projectDir,
	}

	cases := []struct {
		name        string
		tabTitle    string
		aslPaneVars kitty.UserVars
		wantMatch   string
		wantTitle   string
	}{
		{
			name:        "scripts: ASL pane has copied amx_* vars and adds 🔁",
			tabTitle:    boltTitle,
			aslPaneVars: projectPaneVars,
			wantMatch:   "window_id:43",
			wantTitle:   boltLoopTitle,
		},
		{
			name:        "LogScanner: ASL pane lacks amx_*, project pane owns metadata",
			tabTitle:    boltTitle,
			aslPaneVars: kitty.UserVars{},
			wantMatch:   "window_id:42",
			wantTitle:   boltLoopTitle,
		},
		{
			name:        "sis: sticky stop icon remains while 🔁 is added",
			tabTitle:    stopTitle,
			aslPaneVars: kitty.UserVars{},
			wantMatch:   "window_id:42",
			wantTitle:   stopLoopTitle,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := daemon.NewHandler(
				daemon.NewStateGuard(time.Hour),
				daemon.NewNotifier(&config.NotificationConfig{}),
			)
			daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
			kittyClient := &fakeIndicatorKittyClient{
				windows: []kitty.OSWindow{{
					ID: 1,
					Tabs: []kitty.Tab{{
						ID:    100,
						Title: tc.tabTitle,
						Windows: []kitty.Window{
							{ID: 42, UserVars: projectPaneVars},
							{ID: 43, UserVars: tc.aslPaneVars},
						},
					}},
				}},
			}

			err := runWithDeps(
				"AgentSessionLoopStarted",
				43,
				"/tmp/amx",
				HookPayload{
					Source:        "agent_session_loop",
					CWD:           projectDir,
					Harness:       "claude",
					KittyWindowID: 43,
				},
				false,
				kittyClient,
				daemonClient,
			)
			if err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}
			if len(kittyClient.setTitleCalls) != 1 {
				t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
			}
			if kittyClient.setTitleCalls[0] != (setTitleCall{match: tc.wantMatch, title: tc.wantTitle}) {
				t.Fatalf("SetTabTitle call = %+v, want %s/%q", kittyClient.setTitleCalls[0], tc.wantMatch, tc.wantTitle)
			}
			reqs := daemonClient.snapshot()
			if len(reqs) != 1 {
				t.Fatalf("daemon requests = %+v, want exactly one loop_marker request", reqs)
			}
			if reqs[0].Action != "loop_marker" || reqs[0].Event != "AgentSessionLoopStarted" {
				t.Fatalf("daemon request = %+v, want loop_marker / AgentSessionLoopStarted", reqs[0])
			}
		})
	}
}

func TestRunWithDeps_AgentSessionLoopStartedDaemonDownPreservesStateIcon(t *testing.T) {
	projectDir := t.TempDir()
	cases := []struct {
		state string
	}{
		{state: "bolt"},
		{state: "stop"},
	}

	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			title := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: tc.state})
			wantTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: tc.state, Looping: true})
			kittyClient := &fakeIndicatorKittyClient{
				windows: fakeIndicatorWindowsWithDirectory(42, title, "api", "claude", projectDir),
			}
			daemonClient := &fakeIndicatorDaemonClient{err: errors.New("daemon down")}

			err := runWithDeps(
				"AgentSessionLoopStarted",
				42,
				"/tmp/amx",
				HookPayload{
					Source:        "agent_session_loop",
					CWD:           projectDir,
					Harness:       "claude",
					KittyWindowID: 42,
				},
				false,
				kittyClient,
				daemonClient,
			)
			if err != nil {
				t.Fatalf("runWithDeps() error = %v", err)
			}
			if len(kittyClient.setTitleCalls) != 1 {
				t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
			}
			if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:42", title: wantTitle}) {
				t.Fatalf("SetTabTitle call = %+v, want window_id:42/%q", kittyClient.setTitleCalls[0], wantTitle)
			}
			if len(daemonClient.reqs) != 1 || daemonClient.reqs[0].Action != "loop_marker" {
				t.Fatalf("daemon requests = %+v, want one loop_marker request", daemonClient.reqs)
			}
		})
	}
}

// TestRunWithDeps_AgentSessionLoopFinishedSplitTabClearsOnlyLoopMarker
// verifies the finish-side complement to the split-tab live-shaped scenarios:
// when no active loop remains for the project workspace, finish reconciliation
// clears 🔁 from the project pane's tab while preserving the existing state
// icon — including a sticky stop icon — and the unmanaged ASL pane in the same
// tab is not retargeted.
func TestRunWithDeps_AgentSessionLoopFinishedSplitTabClearsOnlyLoopMarker(t *testing.T) {
	projectDir := t.TempDir()
	stopLoopTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Looping: true})
	stopTitle := palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"})

	stubIndicatorLoopDiscover(t, nil, nil)
	handler := daemon.NewHandler(
		daemon.NewStateGuard(time.Hour),
		daemon.NewNotifier(&config.NotificationConfig{}),
	)
	daemonClient := &handlerBackedIndicatorDaemonClient{h: handler}
	kittyClient := &fakeIndicatorKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:    100,
				Title: stopLoopTitle,
				Windows: []kitty.Window{
					{
						ID: 42,
						UserVars: kitty.UserVars{
							Profile:      "api",
							Harness:      "claude",
							AMXDirectory: projectDir,
						},
					},
					{ID: 43},
				},
			}},
		}},
	}

	err := runWithDeps(
		"AgentSessionLoopFinished",
		43,
		"/tmp/amx",
		HookPayload{
			Source:        "agent_session_loop",
			Reason:        "run_finished",
			CWD:           projectDir,
			Harness:       "claude",
			KittyWindowID: 43,
		},
		false,
		kittyClient,
		daemonClient,
	)
	if err != nil {
		t.Fatalf("runWithDeps() error = %v", err)
	}
	if len(kittyClient.setTitleCalls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (%+v)", len(kittyClient.setTitleCalls), kittyClient.setTitleCalls)
	}
	if kittyClient.setTitleCalls[0] != (setTitleCall{match: "window_id:42", title: stopTitle}) {
		t.Fatalf("SetTabTitle call = %+v, want window_id:42/%q", kittyClient.setTitleCalls[0], stopTitle)
	}
	reqs := daemonClient.snapshot()
	if len(reqs) != 1 || reqs[0].Event != "AgentSessionLoopFinished" || reqs[0].Action != "idle" || reqs[0].WindowID != 42 {
		t.Fatalf("daemon requests = %+v, want one idle AgentSessionLoopFinished request for window 42", reqs)
	}
}

func stubIndicatorLoopDiscover(t *testing.T, candidates []agentloop.Candidate, err error) {
	t.Helper()
	old := indicatorLoopDiscover
	indicatorLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		out := make([]agentloop.Candidate, len(candidates))
		copy(out, candidates)
		return out, err
	}
	t.Cleanup(func() {
		indicatorLoopDiscover = old
	})
}

func writeLoopStateFile(t *testing.T, payload map[string]any) string {
	t.Helper()
	statePath := t.TempDir() + "/state.json"
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal loop state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("write loop state: %v", err)
	}
	return statePath
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
