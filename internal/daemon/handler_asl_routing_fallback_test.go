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
)

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
