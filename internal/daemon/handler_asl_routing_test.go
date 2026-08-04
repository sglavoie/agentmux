package daemon

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
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

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
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
	ownerDetail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(ownerDetail.Events) != 1 || ownerDetail.Events[0].Action != "active" || ownerDetail.Events[0].Suppressed {
		t.Fatalf("owner events = %+v; want one allowed active event", ownerDetail.Events)
	}
	srcDetail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(srcDetail.Events) != 0 {
		t.Fatalf("source events = %+v; want none on the routed source pane", srcDetail.Events)
	}
}

func TestHandler_ActiveLoopSiblingRetargetKeepsProducerIdentity(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)})

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    43,
		Event:       "UserPromptSubmit",
		Harness:     harness.Codex,
		PayloadJSON: `{"session_id":"routed-codex"}`,
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("owner events = %+v, want one routed event", detail.Events)
	}
	event := detail.Events[0]
	if event.RouteDecision != routeDecisionOwner {
		t.Fatalf("route decision = %q, want owner", event.RouteDecision)
	}
	if event.SourceKind != string(SourceHarness) || event.SourceKey != "codex:routed-codex" {
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

			resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: tc.event})
			if !resp.OK {
				t.Fatalf("hook response = %+v", resp)
			}
			h.stateMu.Lock()
			st := h.store[42]
			h.stateMu.Unlock()
			if st == nil || st.DesiredIcon != tc.wantIcon {
				t.Fatalf("owner state = %+v; want icon %q for %s", st, tc.wantIcon, tc.event)
			}
			detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "Stop"}); !resp.OK {
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
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "UserPromptSubmit"}); !resp.OK {
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

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
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
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

			resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
			if !resp.OK {
				t.Fatalf("hook response = %+v", resp)
			}

			h.stateMu.Lock()
			owner := h.store[42]
			h.stateMu.Unlock()
			if owner != nil && owner.DesiredIcon != "" {
				t.Fatalf("owner state = %+v; want untouched", owner)
			}
			detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
			if len(detail.Events) != 1 || !detail.Events[0].Suppressed || detail.Events[0].SuppressionReason != suppressionReasonOwnerMismatch {
				t.Fatalf("source events = %+v; want owner_mismatch suppression", detail.Events)
			}
		})
	}
}

// TestHandler_ActiveLoopSiblingAmbiguousWorkspaceLeavesStateUnchanged verifies
// the unique-match requirement: when an unmanaged sibling pane's active loop
// matches more than one managed owner in the same workspace, routing which
// owner should borrow the state machine is ambiguous, so the daemon diagnoses it
// and leaves state unchanged — falling back to owner-mismatch suppression rather
// than fanning one pane's hook onto several owners.
func TestHandler_ActiveLoopSiblingAmbiguousWorkspaceLeavesStateUnchanged(t *testing.T) {
	projectDir := t.TempDir()
	// The loop runs on the unmanaged source pane 43 (in tab A, owned by managed
	// window 42). A second managed tab B (owned by window 44) shares the exact
	// same workspace, so the candidate matches both owners.
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)
	h := newTestHandler()
	windows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			{
				ID:    100,
				Title: "api",
				Windows: []kitty.Window{
					{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "api", Harness: harness.Claude, AMXDirectory: projectDir}},
					{ID: 43, IsActive: false},
				},
			},
			{
				ID:    101,
				Title: "api",
				Windows: []kitty.Window{
					{ID: 44, IsActive: true, UserVars: kitty.UserVars{Profile: "api", Harness: harness.Claude, AMXDirectory: projectDir}},
				},
			},
		},
	}}
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	h.stateMu.Lock()
	owner42 := h.store[42]
	owner44 := h.store[44]
	h.stateMu.Unlock()
	if owner42 != nil && owner42.DesiredIcon != "" {
		t.Fatalf("owner 42 state = %+v; ambiguous match must leave it unchanged", owner42)
	}
	if owner44 != nil && owner44.DesiredIcon != "" {
		t.Fatalf("owner 44 state = %+v; ambiguous match must leave it unchanged", owner44)
	}
	// The source pane records the ordinary owner-mismatch suppression instead.
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || !detail.Events[0].Suppressed || detail.Events[0].SuppressionReason != suppressionReasonOwnerMismatch {
		t.Fatalf("source events = %+v; want owner_mismatch suppression under ambiguity", detail.Events)
	}
}

// TestHandler_ActiveLoopManagedNonOwnerUsesOwnState verifies that managed panes
// never borrow the active title owner's reducer, even when an exact loop
// candidate happens to identify the pane.
func TestHandler_ActiveLoopManagedNonOwnerUsesOwnState(t *testing.T) {
	projectDir := t.TempDir()
	// Managed panes short-circuit before loop discovery and use their own state.
	var calls int
	old := daemonLoopDiscover
	daemonLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		calls++
		return []agentloop.Candidate{aslCandidate(43, projectDir)}, nil
	}
	t.Cleanup(func() { daemonLoopDiscover = old })

	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab("api", []kitty.Window{
		{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "api", Harness: harness.Claude, AMXDirectory: projectDir},
		},
		{
			ID:       43,
			IsActive: false,
			UserVars: kitty.UserVars{Profile: "api", Harness: harness.Claude, AMXDirectory: projectDir},
		},
	})}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	if calls != 0 {
		t.Fatalf("loop discovery calls = %d; want 0 (managed non-owner must not enter the sibling exception)", calls)
	}

	h.stateMu.Lock()
	owner := h.store[42]
	source := h.store[43]
	h.stateMu.Unlock()
	if owner != nil && owner.DesiredIcon != "" {
		t.Fatalf("owner state = %+v; want untouched", owner)
	}
	if source == nil || source.DesiredIcon != "bolt" {
		t.Fatalf("managed source state = %+v; want its own bolt", source)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || detail.Events[0].Suppressed || detail.Events[0].RouteDecision != routeDecisionSource {
		t.Fatalf("source events = %+v; want an allowed source-owned event", detail.Events)
	}
}

func TestHandler_ActiveLoopManagedOwnerUsesOrdinaryPath(t *testing.T) {
	projectDir := t.TempDir()
	// Owner-origin hooks never enter the sibling exception, so discovery must
	// not run for them.
	stubDaemonLoopDiscover(t, nil, errors.New("discovery must not run for the owner path"))
	h := newTestHandler()
	fake := &fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "bolt" {
		t.Fatalf("owner state = %+v; want bolt via the ordinary owner path", st)
	}
}

func TestHandler_ActiveLoopSiblingCachesDiscoveryWithinInterval(t *testing.T) {
	projectDir := t.TempDir()
	var calls int
	old := daemonLoopDiscover
	daemonLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		calls++
		return []agentloop.Candidate{aslCandidate(43, projectDir)}, nil
	}
	t.Cleanup(func() { daemonLoopDiscover = old })

	h := newTestHandler()
	fake := &fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)}
	h.SetKittyClient(fake)

	for i := 0; i < 3; i++ {
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"}); !resp.OK {
			t.Fatalf("hook %d response = %+v", i, resp)
		}
	}
	if calls != 1 {
		t.Fatalf("discovery calls = %d; want 1 (hook burst reuses the cache)", calls)
	}

	// A lifecycle event invalidates the cache; the next routed hook rescans.
	h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 43,
		Event:    "AgentSessionLoopTurnStarted",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source: "agent_session_loop", CWD: projectDir, Harness: harness.Claude, KittyWindowID: 43,
		}),
	})
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"}); !resp.OK {
		t.Fatalf("post-invalidate hook response = %+v", resp)
	}
	if calls != 2 {
		t.Fatalf("discovery calls = %d; want 2 after lifecycle invalidation", calls)
	}
}

// TestHandler_ActiveLoopCacheDiscardsStaleScanAfterInvalidation proves the
// arrival-generation ordering: a discovery scan that begins before a lifecycle
// invalidation must not publish its now-stale result as the authoritative
// cache. A channel-gated discovery stub blocks the first scan until after the
// cache has been invalidated, then the next lookup must rescan rather than serve
// the discarded slice.
func TestHandler_ActiveLoopCacheDiscardsStaleScanAfterInvalidation(t *testing.T) {
	projectDir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int32
	old := daemonLoopDiscover
	daemonLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(started)
			<-release
		}
		return []agentloop.Candidate{aslCandidate(43, projectDir)}, nil
	}
	t.Cleanup(func() { daemonLoopDiscover = old })

	h := newTestHandler()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := h.activeLoopCandidates(); err != nil {
			t.Errorf("in-flight scan error: %v", err)
		}
	}()

	<-started
	// A lifecycle event invalidates the cache while the first scan is in flight.
	h.invalidateActiveLoopCache()
	// Let the in-flight scan finish; its result predates the invalidation.
	close(release)
	<-done

	h.stateMu.Lock()
	valid := h.activeLoopCacheValid
	h.stateMu.Unlock()
	if valid {
		t.Fatalf("cache marked valid; a scan that started before invalidation must not publish")
	}

	// The next lookup must rescan rather than serve the discarded result.
	if _, err := h.activeLoopCandidates(); err != nil {
		t.Fatalf("post-invalidate lookup error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("discovery calls = %d; want 2 (stale scan discarded, fresh scan run)", got)
	}
}

func TestHandler_ActiveLoopSiblingConcurrentHooksRaceSafe(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)
	h := newTestHandler()
	fake := &fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)}
	h.SetKittyClient(fake)

	loopPayload := hookPayloadJSON(t, hookevent.HookPayload{
		Source: "agent_session_loop", CWD: projectDir, Harness: harness.Claude, KittyWindowID: 43,
	})

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%4 == 0 {
				h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "AgentSessionLoopTurnStarted", PayloadJSON: loopPayload})
				return
			}
			h.Handle(Request{Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse"})
		}(i)
	}
	wg.Wait()

	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil {
		t.Fatalf("owner state missing after concurrent routing")
	}
}
