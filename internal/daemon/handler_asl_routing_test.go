package daemon

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// aslSiblingWindows builds a split tab whose title owner is the managed window
// ownerID (rooted at projectDir) and whose sourceID pane is an unmanaged split
// running an agent-session-loop. The source is deliberately inactive: these
// tests exercise exact ASL authorization rather than ordinary active-pane
// borrowing.
func aslSiblingWindows(tabTitle, projectDir string, ownerID, sourceID int) []kitty.OSWindow {
	return fakeOSWindowsWithSplitTab(tabTitle, []kitty.Window{
		{
			ID:       ownerID,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "api", Harness: harness.Claude, AMXDirectory: projectDir},
		},
		{ID: sourceID, IsActive: false},
	})
}

// aslCandidate is a live, unfinished loop candidate pinned to windowID and
// projectDir. agentloop.Discover only ever returns live/unfinished candidates,
// so the stub mirrors that invariant.
func aslCandidate(windowID int, projectDir string) agentloop.Candidate {
	return agentloop.Candidate{WindowID: windowID, CWD: projectDir, Harness: harness.Claude, PID: 4242}
}

func TestHandler_ActiveLoopSiblingRoutesActivityToOwner(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)

	h := newTestHandler()
	fake := &fakeKittyClient{windows: aslSiblingWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", Looping: true}),
		projectDir, 42, 43,
	)}
	h.SetKittyClient(fake)
	h.stateMu.Lock()
	owner := h.ensureWindowStateLocked(42)
	owner.Looping = true
	owner.LoopingSet = true
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	h.stateMu.Lock()
	st := h.store[42]
	src := h.store[43]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "bolt" || !st.Looping || !st.LoopingSet {
		t.Fatalf("owner state = %+v; want bolt with loop preserved", st)
	}
	if src != nil && (src.DesiredIcon != "" || src.StateSet) {
		t.Fatalf("source state = %+v; unmanaged source must not gain a desired icon", src)
	}

	// Event history belongs to the managed owner, not the unmanaged source.
	ownerDetail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(ownerDetail.Events) != 1 || ownerDetail.Events[0].Action != "active" || ownerDetail.Events[0].Suppressed {
		t.Fatalf("owner events = %+v; want one allowed active event", ownerDetail.Events)
	}
	srcDetail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	if len(srcDetail.Events) != 0 {
		t.Fatalf("source events = %+v; want none on the routed source pane", srcDetail.Events)
	}
}

func TestHandler_ActiveLoopSiblingRetargetKeepsProducerIdentity(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)})

	resp := h.Handle(protocol.Request{
		Type:        protocol.TypeHookEvent,
		WindowID:    43,
		Event:       "UserPromptSubmit",
		Harness:     harness.Codex,
		PayloadJSON: `{"session_id":"routed-codex"}`,
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("owner events = %+v, want one routed event", detail.Events)
	}
	event := detail.Events[0]
	if event.RouteDecision != routeDecisionOwner {
		t.Fatalf("route decision = %q, want owner", event.RouteDecision)
	}
	if event.SourceKind != string(protocol.SourceHarness) || event.SourceKey != "codex:routed-codex" {
		t.Fatalf("routed source = %q/%q, want harness/codex:routed-codex", event.SourceKind, event.SourceKey)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Claude {
		t.Fatalf("durable target harness = %q, want claude", meta.Durable.Harness)
	}
}

func TestHandler_ActiveLoopSiblingRoutesAllStatesToOwner(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0 // route Stop synchronously instead of via the reset-defer timer
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	cases := []struct {
		event    string
		wantIcon string
	}{
		{"Stop", "stop"},
		{"PermissionRequest", "key"},
		{"PostToolUseFailure", "warning"},
		{"PreCompact", "compact"},
		{"Elicitation", "question"},
		{"SubagentStart", "subagent"},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			projectDir := t.TempDir()
			stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)
			h := newTestHandler()
			h.SetStopConfirm(0, 0, false) // route Stop straight to the reducer, no confirmed-idle hold
			fake := &fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)}
			h.SetKittyClient(fake)

			resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: tc.event})
			if !resp.OK {
				t.Fatalf("hook response = %+v", resp)
			}
			h.stateMu.Lock()
			st := h.store[42]
			h.stateMu.Unlock()
			if st == nil || st.DesiredIcon != tc.wantIcon {
				t.Fatalf("owner state = %+v; want icon %q for %s", st, tc.wantIcon, tc.event)
			}
			detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
			if len(detail.Events) != 1 || detail.Events[0].Suppressed {
				t.Fatalf("owner events = %+v; want one allowed event", detail.Events)
			}
		})
	}
}

func TestHandler_ActiveLoopSiblingTurnCyclePreservesLoop(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0 // route Stop synchronously instead of via the reset-defer timer
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)

	h := newTestHandler()
	h.SetStopConfirm(0, 0, false) // route Stop straight to the reducer, no confirmed-idle hold
	fake := &fakeKittyClient{windows: aslSiblingWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", Looping: true}),
		projectDir, 42, 43,
	)}
	h.SetKittyClient(fake)
	h.stateMu.Lock()
	owner := h.ensureWindowStateLocked(42)
	owner.Looping = true
	owner.LoopingSet = true
	h.stateMu.Unlock()

	// A turn's Stop routed from the unmanaged source pane must land stop on the
	// managed owner while keeping the loop marker authoritative.
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "Stop"}); !resp.OK {
		t.Fatalf("routed Stop response = %+v", resp)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "stop" || !st.Looping || !st.LoopingSet {
		t.Fatalf("owner state after Stop = %+v; want stop with loop preserved", st)
	}

	// The next prompt in the same loop turn drives the owner back to bolt, still
	// with the loop marker intact — the routed transition never resets 🔁.
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "UserPromptSubmit"}); !resp.OK {
		t.Fatalf("routed UserPromptSubmit response = %+v", resp)
	}
	h.stateMu.Lock()
	st = h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "bolt" || !st.Looping || !st.LoopingSet {
		t.Fatalf("owner state after prompt = %+v; want bolt with loop preserved", st)
	}
}

func TestHandler_ActiveLoopSiblingContextLimitProbesSourceRendersErrorOnOwner(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false) // route the base transition straight to the reducer
	// Run the async context-limit probe inline for a deterministic promotion.
	h.probeRunner = func(fn func()) { fn() }
	fake := &fakeKittyClient{
		windows: aslSiblingWindows(palette.BuildTitle("api", "bolt", false), projectDir, 42, 43),
		getText: map[string]string{
			// Keyed on the SOURCE pane: the banner is only visible where the loop stalled.
			kitty.MatchWindowByID(43): "Context limit reached - /compact or /clear to continue\n",
		},
	}
	h.SetKittyClient(fake)

	resp := h.Handle(protocol.Request{
		Type:        protocol.TypeHookEvent,
		WindowID:    43,
		Event:       "Notification",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{NotificationType: "agent_idle"}),
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "error" {
		t.Fatalf("owner state = %+v; want error icon promoted from the source-pane context limit", st)
	}
	// The base transition routes to the owner first, then the source-pane probe
	// upgrades it to stop_failure asynchronously.
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	last := detail.Events[len(detail.Events)-1]
	if last.Action != "stop_failure" || !last.ContextLimit {
		t.Fatalf("owner events = %+v; want a stop_failure context-limit upgrade on the owner", detail.Events)
	}
}

func TestHandler_ActiveLoopSiblingFallsBackToOwnerMismatch(t *testing.T) {
	cases := []struct {
		name  string
		build func(projectDir string) ([]agentloop.Candidate, error)
	}{
		{
			name:  "no active candidate (ordinary sibling or discovery-filtered stale/finished run)",
			build: func(string) ([]agentloop.Candidate, error) { return nil, nil },
		},
		{
			name:  "discovery failure",
			build: func(string) ([]agentloop.Candidate, error) { return nil, errors.New("boom") },
		},
		{
			name: "candidate window id does not match the source pane",
			build: func(projectDir string) ([]agentloop.Candidate, error) {
				return []agentloop.Candidate{aslCandidate(99, projectDir)}, nil
			},
		},
		{
			name: "multiple exact candidates are ambiguous",
			build: func(projectDir string) ([]agentloop.Candidate, error) {
				return []agentloop.Candidate{
					aslCandidate(43, projectDir),
					aslCandidate(43, projectDir),
				}, nil
			},
		},
		{
			name: "exact candidate but no managed workspace target",
			build: func(string) ([]agentloop.Candidate, error) {
				return []agentloop.Candidate{aslCandidate(43, "/nonexistent/other-project")}, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			cands, derr := tc.build(projectDir)
			stubDaemonLoopDiscover(t, cands, derr)
			h := newTestHandler()
			fake := &fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)}
			h.SetKittyClient(fake)

			resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
			if !resp.OK {
				t.Fatalf("hook response = %+v", resp)
			}

			h.stateMu.Lock()
			owner := h.store[42]
			h.stateMu.Unlock()
			if owner != nil && owner.DesiredIcon != "" {
				t.Fatalf("owner state = %+v; want untouched", owner)
			}
			detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
			if len(detail.Events) != 1 || !detail.Events[0].Suppressed || detail.Events[0].SuppressionReason != suppressionReasonOwnerMismatch {
				t.Fatalf("source events = %+v; want owner_mismatch suppression", detail.Events)
			}
		})
	}
}
