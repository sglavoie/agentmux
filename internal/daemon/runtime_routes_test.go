package daemon

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// These tests pin hook-origin translation: managed hooks route by runtime
// session through the registry, and no duplicate, reordered, late, or
// post-termination delivery can reach a different runtime session's state.

const (
	tmuxT = "/tmp/amx-test-tmux-t.sock"
	tmuxU = "/tmp/amx-test-tmux-u.sock"
)

type fakeRuntimeDirectory struct {
	mu        sync.Mutex
	records   map[runtimesession.ID]runtimesession.Record
	err       error
	refreshes int
}

func newFakeRuntimeDirectory() *fakeRuntimeDirectory {
	return &fakeRuntimeDirectory{records: make(map[runtimesession.ID]runtimesession.Record)}
}

func (d *fakeRuntimeDirectory) Lookup(id runtimesession.ID) (runtimesession.Record, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.records[id]
	return record, ok, d.err
}

func (d *fakeRuntimeDirectory) Refresh() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refreshes++
	return d.err
}

func (d *fakeRuntimeDirectory) put(id runtimesession.ID, state runtimesession.State, kind runtimesession.BackendKind, address, generation, ref string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records[id] = runtimesession.Record{
		ID:    id,
		State: state,
		Target: &runtimesession.Target{
			Instance: runtimesession.BackendInstance{Kind: kind, Address: address, Generation: generation},
			Ref:      ref,
		},
	}
}

func (d *fakeRuntimeDirectory) setProject(id runtimesession.ID, project string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record := d.records[id]
	record.Project = project
	d.records[id] = record
}

func (d *fakeRuntimeDirectory) setState(id runtimesession.ID, state runtimesession.State) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record := d.records[id]
	record.State = state
	d.records[id] = record
}

func newRuntimeID(t *testing.T) runtimesession.ID {
	t.Helper()
	id, err := runtimesession.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func runtimeHook(t *testing.T, runtime runtimesession.ID, event string, payload hookevent.HookPayload) protocol.Request {
	t.Helper()
	return protocol.Request{
		Type:             protocol.TypeHookEvent,
		RuntimeSessionID: string(runtime),
		Event:            event,
		Harness:          "claude",
		PayloadJSON:      hookPayloadJSON(t, payload),
	}
}

func newRuntimeTestHandler(t *testing.T) (*Handler, *fakeRuntimeDirectory) {
	t.Helper()
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	return h, directory
}

func slotOf(t *testing.T, h *Handler, runtime runtimesession.ID) int {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	slot, ok := h.runtimeRoutes.byRuntime[string(runtime)]
	if !ok {
		t.Fatalf("runtime %s is bound to no slot", runtime)
	}
	return slot
}

func slotFacts(h *Handler, slot int) []protocol.SourceFactSnapshot {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok || st.Reducer == nil {
		return nil
	}
	return st.Reducer.Facts()
}

func slotIcon(h *Handler, slot int) string {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok {
		return ""
	}
	return st.Effective.Icon
}

func hasFact(facts []protocol.SourceFactSnapshot, kind protocol.SourceKind, key string) bool {
	for _, f := range facts {
		if f.Kind == kind && f.Key == key {
			return true
		}
	}
	return false
}

func requireAccepted(t *testing.T, resp protocol.Response) {
	t.Helper()
	if !resp.OK || resp.Delivery == nil || resp.Delivery.Status != protocol.DeliveryAcknowledgementAccepted {
		t.Fatalf("response = %+v (delivery %+v); want accepted", resp, resp.Delivery)
	}
}

func requireRefused(t *testing.T, resp protocol.Response, reason string, retryable bool) {
	t.Helper()
	if resp.OK || resp.Retryable != retryable || resp.Delivery == nil ||
		resp.Delivery.Status != protocol.DeliveryAcknowledgementRejected || resp.Delivery.Reason != reason {
		t.Fatalf("response = %+v (delivery %+v); want refused %s retryable=%v", resp, resp.Delivery, reason, retryable)
	}
}

func TestSessionDetailSelectsRuntimeSession(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	first, second := newRuntimeID(t), newRuntimeID(t)
	directory.put(first, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	directory.put(second, runtimesession.StateLive, runtimesession.BackendTmux, tmuxU, "tmux-u-1", "%7")
	requireAccepted(t, h.Handle(runtimeHook(t, first, "UserPromptSubmit", hookevent.HookPayload{SessionID: "first"})))
	requireAccepted(t, h.Handle(runtimeHook(t, second, "PermissionRequest", hookevent.HookPayload{SessionID: "second"})))
	for _, tc := range []struct {
		runtime runtimesession.ID
		event   string
	}{
		{first, "UserPromptSubmit"},
		{second, "PermissionRequest"},
	} {
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, RuntimeSessionID: string(tc.runtime)})
		if !resp.OK || len(resp.Events) != 1 || resp.Events[0].Event != tc.event {
			t.Fatalf("detail for %s = %+v; want only %s", tc.runtime, resp.Events, tc.event)
		}
	}
	unknown := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, RuntimeSessionID: string(newRuntimeID(t))})
	if !unknown.OK || len(unknown.Events) != 0 {
		t.Fatalf("unknown runtime received another session's events: %+v", unknown.Events)
	}
}

func TestManagedHooksRouteByRuntimeSessionAcrossTmuxInstances(t *testing.T) {
	t.Parallel()
	h, directory := newRuntimeTestHandler(t)
	onT, onU, restartedT := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(onT, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	directory.put(onU, runtimesession.StateLive, runtimesession.BackendTmux, tmuxU, "tmux-u-1", "%7")
	directory.put(restartedT, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-2", "%7")
	cases := []struct {
		id                            runtimesession.ID
		session, instance, generation string
	}{
		{onT, "session-t", tmuxT, "tmux-t-1"},
		{onU, "session-u", tmuxU, "tmux-u-1"},
		{restartedT, "session-t2", tmuxT, "tmux-t-2"},
	}
	slots := map[int]bool{}
	for _, tc := range cases {
		requireAccepted(t, h.Handle(runtimeHook(t, tc.id, "UserPromptSubmit", hookevent.HookPayload{SessionID: tc.session})))
		slot := slotOf(t, h, tc.id)
		if slots[slot] || !isRuntimeSyntheticSlot(slot) {
			t.Fatalf("runtime %s received duplicate or invalid slot %d", tc.id, slot)
		}
		slots[slot] = true
		if facts := slotFacts(h, slot); len(facts) != 1 || !hasFact(facts, protocol.SourceHarness, "claude:"+tc.session) {
			t.Fatalf("runtime %s facts = %+v", tc.id, facts)
		}
		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, RuntimeSessionID: string(tc.id)})
		if len(detail.Events) != 1 || detail.Events[0].Origin == nil {
			t.Fatalf("runtime %s detail = %+v", tc.id, detail)
		}
		origin := detail.Events[0].Origin
		if origin.RuntimeSessionID != string(tc.id) || origin.Backend != "tmux" || origin.Instance != tc.instance || origin.Generation != tc.generation || origin.Pane != "%7" || origin.Slot != slot {
			t.Fatalf("runtime %s origin = %+v", tc.id, origin)
		}
	}
}

func TestManagedHookAcknowledgementNamesTheRuntimeNotASlot(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	runtime := newRuntimeID(t)
	directory.put(runtime, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")

	resp := h.Handle(runtimeHook(t, runtime, "UserPromptSubmit", hookevent.HookPayload{SessionID: "s1"}))
	requireAccepted(t, resp)
	if resp.Delivery.RuntimeSessionID != string(runtime) || resp.Delivery.SourceWindowID != 0 || resp.Delivery.TargetWindowID != 0 {
		t.Fatalf("acknowledgement = %+v; want the runtime session and no window IDs", resp.Delivery)
	}
}

func TestManagedHookTranslationRefusals(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	pending, ended, stale, abandoned, badTarget := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(pending, runtimesession.StatePending, runtimesession.BackendTmux, tmuxT, "g", "%1")
	directory.put(ended, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "g", "%2")
	directory.put(stale, runtimesession.StateStale, runtimesession.BackendTmux, tmuxT, "g", "%3")
	directory.put(abandoned, runtimesession.StateAbandoned, runtimesession.BackendTmux, tmuxT, "g", "%4")
	directory.put(badTarget, runtimesession.StateLive, runtimesession.BackendKind("unsupported"), tmuxT, "g", "%7")

	payload := hookevent.HookPayload{SessionID: "s1"}
	for _, tc := range []struct {
		name      string
		req       protocol.Request
		reason    string
		retryable bool
	}{
		{"malformed", protocol.Request{Type: protocol.TypeHookEvent, RuntimeSessionID: "rs-nope", Event: "Stop", PayloadJSON: "{}"}, protocol.OriginReasonInvalidRuntimeSession, false},
		{"unknown", runtimeHook(t, newRuntimeID(t), "Stop", payload), protocol.OriginReasonRuntimeSessionUnknown, false},
		{"pending", runtimeHook(t, pending, "Stop", payload), protocol.OriginReasonRuntimeSessionPending, true},
		{"ended", runtimeHook(t, ended, "Stop", payload), protocol.OriginReasonRuntimeSessionEnded, false},
		{"stale", runtimeHook(t, stale, "Stop", payload), protocol.OriginReasonRuntimeSessionEnded, false},
		{"abandoned", runtimeHook(t, abandoned, "Stop", payload), protocol.OriginReasonRuntimeSessionEnded, false},
		{"unsupported target", runtimeHook(t, badTarget, "Stop", payload), protocol.OriginReasonInvalidRuntimeTarget, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireRefused(t, h.Handle(tc.req), tc.reason, tc.retryable)
		})
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if len(h.store) != 0 {
		t.Fatalf("refused deliveries created state: %v", len(h.store))
	}

	// Direct state updates cannot name a runtime slot either.
	h.stateMu.Unlock()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: runtimeSlotBase, Action: "active"})
	h.stateMu.Lock()
	if resp.OK {
		t.Fatalf("direct state update to a runtime slot = %+v", resp)
	}
}

func TestDuplicateAndReorderedHooksStayInsideTheirRuntimeSession(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	first, second := newRuntimeID(t), newRuntimeID(t)
	directory.put(first, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(second, runtimesession.StateLive, runtimesession.BackendTmux, tmuxU, "g1", "%1")

	// Both sessions run the same harness session ID, so their facts share a
	// source key; only runtime routing keeps them apart.
	at := time.Now().Unix()
	seq := func(n int) hookevent.HookPayload {
		return hookevent.HookPayload{SessionID: "shared", SourceSequence: strconv.Itoa(n), EmittedTime: strconv.FormatInt(at+int64(n), 10)}
	}
	requireAccepted(t, h.Handle(runtimeHook(t, first, "UserPromptSubmit", seq(5))))
	requireAccepted(t, h.Handle(runtimeHook(t, second, "UserPromptSubmit", seq(1))))
	// first's reordered older permission and an exact duplicate prompt change
	// nothing. (Permission rather than Stop keeps the stop-confirmation hold
	// out of the picture.)
	requireAccepted(t, h.Handle(runtimeHook(t, first, "PermissionRequest", seq(3))))
	requireAccepted(t, h.Handle(runtimeHook(t, first, "UserPromptSubmit", seq(5))))
	// second's newer permission applies to second only.
	requireAccepted(t, h.Handle(runtimeHook(t, second, "PermissionRequest", seq(2))))

	if icon := slotIcon(h, slotOf(t, h, first)); icon != "bolt" {
		t.Fatalf("first runtime icon = %q; want bolt (reordered permission and duplicate ignored)", icon)
	}
	if icon := slotIcon(h, slotOf(t, h, second)); icon != "key" {
		t.Fatalf("second runtime icon = %q; want its own key", icon)
	}
}

func TestPipeChildAndSubagentsStayWithTheirRuntimeSession(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	parent, neighbour := newRuntimeID(t), newRuntimeID(t)
	directory.put(parent, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(neighbour, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%2")

	requireAccepted(t, h.Handle(runtimeHook(t, neighbour, "UserPromptSubmit", hookevent.HookPayload{SessionID: "n1"})))
	requireAccepted(t, h.Handle(runtimeHook(t, parent, "UserPromptSubmit", hookevent.HookPayload{SessionID: "p1"})))
	// A `claude -p` child inherits its parent's runtime session.
	child := runtimeHook(t, parent, "UserPromptSubmit", hookevent.HookPayload{SessionID: "child"})
	child.PipeMode = true
	child.HarnessProcess = &protocol.HarnessProcessIdentity{Harness: "claude", PID: 4242, StartToken: "Thu Oct  9 12:00:00 2026"}
	requireAccepted(t, h.Handle(child))
	requireAccepted(t, h.Handle(runtimeHook(t, parent, "SubagentStart", hookevent.HookPayload{SessionID: "p1", AgentID: "agent-1"})))

	parentFacts := slotFacts(h, slotOf(t, h, parent))
	neighbourFacts := slotFacts(h, slotOf(t, h, neighbour))
	if !hasFact(parentFacts, protocol.SourceHarness, "claude:child") || !hasFact(parentFacts, protocol.SourceSubagent, "agent-1") {
		t.Fatalf("parent facts = %+v; want the pipe child and the subagent", parentFacts)
	}
	if len(neighbourFacts) != 1 || !hasFact(neighbourFacts, protocol.SourceHarness, "claude:n1") {
		t.Fatalf("neighbour facts = %+v; want only its own session", neighbourFacts)
	}
}

func TestPostTerminationHooksCannotReachASuccessorOnTheSameTarget(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	old, successor := newRuntimeID(t), newRuntimeID(t)
	directory.put(old, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	requireAccepted(t, h.Handle(runtimeHook(t, old, "UserPromptSubmit", hookevent.HookPayload{SessionID: "old"})))
	requireAccepted(t, h.Handle(runtimeHook(t, old, "SubagentStart", hookevent.HookPayload{SessionID: "old", AgentID: "agent-old"})))
	oldSlot := slotOf(t, h, old)

	// The pane is respawned in place: the old session ends and a new one
	// registers the very same target.
	directory.setState(old, runtimesession.StateEnded)
	directory.put(successor, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")

	// The old session's delayed hooks are refused and close its slot.
	requireRefused(t, h.Handle(runtimeHook(t, old, "Stop", hookevent.HookPayload{SessionID: "old"})),
		protocol.OriginReasonRuntimeSessionEnded, false)
	h.stateMu.Lock()
	_, oldStateRemains := h.store[oldSlot]
	h.stateMu.Unlock()
	if oldStateRemains {
		t.Fatal("ended session's tmux slot was not closed")
	}

	requireAccepted(t, h.Handle(runtimeHook(t, successor, "UserPromptSubmit", hookevent.HookPayload{SessionID: "new"})))
	requireRefused(t, h.Handle(runtimeHook(t, old, "SubagentStop", hookevent.HookPayload{SessionID: "old", AgentID: "agent-old"})),
		protocol.OriginReasonRuntimeSessionEnded, false)
	newSlot := slotOf(t, h, successor)
	if newSlot == oldSlot {
		t.Fatalf("successor reused the closed slot %d", oldSlot)
	}
	facts := slotFacts(h, newSlot)
	if len(facts) != 1 || !hasFact(facts, protocol.SourceHarness, "claude:new") {
		t.Fatalf("successor facts = %+v; want only its own session", facts)
	}
}

func TestMaintenanceClosesTmuxSlotsOfEndedSessions(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	ending, staying := newRuntimeID(t), newRuntimeID(t)
	directory.put(ending, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(staying, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%2")
	requireAccepted(t, h.Handle(runtimeHook(t, ending, "UserPromptSubmit", hookevent.HookPayload{SessionID: "a"})))
	requireAccepted(t, h.Handle(runtimeHook(t, staying, "UserPromptSubmit", hookevent.HookPayload{SessionID: "b"})))
	endingSlot, stayingSlot := slotOf(t, h, ending), slotOf(t, h, staying)

	directory.setState(ending, runtimesession.StateEnded)
	h.retireEndedRuntimeSessions(time.Now())
	if directory.refreshes == 0 {
		t.Fatal("maintenance did not refresh the registry")
	}
	h.stateMu.Lock()
	_, endingKept := h.store[endingSlot]
	_, stayingKept := h.store[stayingSlot]
	h.stateMu.Unlock()
	if endingKept || !stayingKept {
		t.Fatalf("after retirement ending kept=%v staying kept=%v", endingKept, stayingKept)
	}
}

// TestManagedHookAcknowledgementNeverWaitsOnBackendDiscovery: translation
// reads the registry cache and daemon memory only, so a wedged screen reader
// cannot delay a managed hook.
func TestManagedHookAcknowledgementNeverWaitsOnBackendDiscovery(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	runtimes := []runtimesession.ID{newRuntimeID(t), newRuntimeID(t)}
	directory.put(runtimes[0], runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(runtimes[1], runtimesession.StateLive, runtimesession.BackendTmux, tmuxU, "tmux-u-1", "%7")
	release := make(chan struct{})
	h.SetRuntimeScreenReader(func(runtimesession.BackendInstance) (RuntimeScreen, error) {
		<-release
		return nil, nil
	})
	defer close(release)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 40 {
			h.Handle(runtimeHook(t, runtimes[i%2], "PreToolUse", hookevent.HookPayload{SessionID: "s"}))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("managed hook acknowledgement blocked behind a wedged screen reader")
	}
}

// TestHooksTranslatedBeforeTheirTmuxSessionEndsLeaveTheSlotClosed: a hook
// translated while its session was live may be handled after maintenance ended
// the session and closed its slot. Whatever the hook path does with it — record
// an ignored or unknown event, arm a stop hold, refresh metadata — the closed
// slot's state stays dropped, since nothing would ever drop it again.
func TestHooksTranslatedBeforeTheirTmuxSessionEndsLeaveTheSlotClosed(t *testing.T) {
	for _, event := range []string{"NoSuchHookEvent", "Stop"} {
		t.Run(event, func(t *testing.T) {
			h := newTestHandler()
			h.SetStopConfirm(time.Hour, time.Hour, false)
			directory := newFakeRuntimeDirectory()
			h.SetRuntimeSessionDirectory(directory)
			runtime := newRuntimeID(t)
			directory.put(runtime, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
			requireAccepted(t, h.Handle(runtimeHook(t, runtime, "UserPromptSubmit", hookevent.HookPayload{})))
			slot := slotOf(t, h, runtime)

			late, rejection := h.translateHookOrigin(runtimeHook(t, runtime, event, hookevent.HookPayload{}))
			if rejection != nil {
				t.Fatalf("translation refused: %+v", rejection)
			}
			directory.setState(runtime, runtimesession.StateEnded)
			h.retireEndedRuntimeSessions(time.Now())
			h.handleHookEvent(late)

			h.stateMu.Lock()
			_, revived := h.store[slot]
			_, held := h.pendingStopHolds[slot]
			h.stateMu.Unlock()
			if revived {
				t.Fatalf("a late %s brought the closed slot's state back", event)
			}
			if held {
				t.Fatalf("a late %s armed a stop hold on the closed slot", event)
			}
		})
	}
}
