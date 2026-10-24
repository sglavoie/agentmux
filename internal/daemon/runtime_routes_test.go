package daemon

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// These tests pin hook-origin translation: managed hooks route by runtime
// session through the registry, legacy Kitty hooks keep their window slot, and
// no duplicate, reordered, late, or post-termination delivery can reach a
// different runtime session's state — including when Kitty instances and tmux
// servers reuse the same pane numbers.

const (
	kittyA = "unix:/tmp/amx-test-kitty-a"
	kittyB = "unix:/tmp/amx-test-kitty-b"
	tmuxT  = "/tmp/amx-test-tmux-t.sock"
	tmuxU  = "/tmp/amx-test-tmux-u.sock"
)

type fakeRuntimeDirectory struct {
	mu        sync.Mutex
	records   map[runtimesession.ID]runtimesession.Record
	err       error
	lookups   int
	refreshes int
}

func newFakeRuntimeDirectory() *fakeRuntimeDirectory {
	return &fakeRuntimeDirectory{records: make(map[runtimesession.ID]runtimesession.Record)}
}

func (d *fakeRuntimeDirectory) Lookup(id runtimesession.ID) (runtimesession.Record, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lookups++
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

func (d *fakeRuntimeDirectory) lookupCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lookups
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

func legacyHook(t *testing.T, windowID int, socket, event string, payload hookevent.HookPayload) protocol.Request {
	t.Helper()
	return protocol.Request{
		Type:          protocol.TypeHookEvent,
		WindowID:      windowID,
		KittyListenOn: socket,
		Event:         event,
		Harness:       "claude",
		PayloadJSON:   hookPayloadJSON(t, payload),
	}
}

func newRuntimeTestHandler(t *testing.T) (*Handler, *fakeRuntimeDirectory) {
	t.Helper()
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false)
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{
		{ID: 7, IsActive: true, UserVars: kitty.UserVars{Profile: "managed", Harness: "claude"}},
	})})
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

func TestSocketStampedHookDoesNotBorrowSnapshotWithUnknownSocket(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows("first", []kitty.Window{{
		ID: 7, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
	}})})
	req, rejection := h.translateLegacyKittyOrigin(protocol.Request{WindowID: 7, KittyListenOn: kittyB})
	if rejection != nil {
		t.Fatalf("hook origin rejected: %+v", rejection)
	}
	ctx := h.resolveHookEventContext(req)
	if !ctx.lookupFailed || ctx.win != nil || len(ctx.windows) != 0 {
		t.Fatalf("unknown-socket snapshot was trusted for %s: %+v", kittyB, ctx)
	}
}

func TestSessionDetailSelectsTheKittySocketAndWindowPair(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyA, "UserPromptSubmit", hookevent.HookPayload{SessionID: "first"})))
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyB, "PermissionRequest", hookevent.HookPayload{SessionID: "second"})))
	for _, tc := range []struct {
		socket string
		event  string
	}{
		{kittyA, "UserPromptSubmit"},
		{kittyB, "PermissionRequest"},
	} {
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 7, KittyListenOn: tc.socket})
		if !resp.OK || len(resp.Events) != 1 || resp.Events[0].Event != tc.event {
			t.Fatalf("detail for %s = %+v; want only %s", tc.socket, resp.Events, tc.event)
		}
	}
	unknown := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 7, KittyListenOn: "unix:/unknown"})
	if !unknown.OK || len(unknown.Events) != 0 {
		t.Fatalf("unknown Kitty socket received another instance's events: %+v", unknown.Events)
	}
	legacy := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 7})
	if legacy.OK || legacy.Error != protocol.OriginReasonTargetConflict {
		t.Fatalf("socketless detail read ambiguous Kitty events: %+v", legacy)
	}
	export := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 7})
	if export.OK || export.Error != protocol.OriginReasonTargetConflict {
		t.Fatalf("socketless export of ambiguous Kitty window = %+v", export)
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

func TestEventsExportSelectsTheKittySocketAndWindowPair(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyA, "UserPromptSubmit", hookevent.HookPayload{SessionID: "first"})))
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyB, "PermissionRequest", hookevent.HookPayload{SessionID: "second"})))
	h.stateMu.Lock()
	secondSlot := h.runtimeRoutes.kittySlotOf(kittyB, 7)
	h.stateMu.Unlock()
	resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 7, KittyListenOn: kittyB})
	if !resp.OK || len(resp.EventsByWindow) != 1 || len(resp.EventsByWindow[secondSlot]) != 1 ||
		resp.EventsByWindow[secondSlot][0].Event != "PermissionRequest" ||
		resp.KittySlots[secondSlot] != (protocol.KittySlot{Socket: kittyB, WindowID: 7}) {
		t.Fatalf("second Kitty export = %+v", resp)
	}
	unknown := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 7, KittyListenOn: "unix:/unknown"})
	if unknown.OK || len(unknown.EventsByWindow) != 0 {
		t.Fatalf("unknown socket exported another instance: %+v", unknown)
	}
}

func TestManagedHooksRouteByRuntimeSessionAcrossMixedBackends(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	onKittyA, onKittyB := newRuntimeID(t), newRuntimeID(t)
	onTmuxT, onTmuxU, onTmuxTRestarted := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	// Every target below is pane/window "7" of a different server.
	directory.put(onKittyA, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(onKittyB, runtimesession.StateLive, runtimesession.BackendKitty, kittyB, "kitty-b-1", "7")
	directory.put(onTmuxT, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	directory.put(onTmuxU, runtimesession.StateLive, runtimesession.BackendTmux, tmuxU, "tmux-u-1", "%7")
	directory.put(onTmuxTRestarted, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-2", "%7")

	for runtime, session := range map[runtimesession.ID]string{
		onKittyA: "session-kitty-a", onTmuxT: "session-tmux-t", onTmuxU: "session-tmux-u", onTmuxTRestarted: "session-tmux-t2",
	} {
		requireAccepted(t, h.Handle(runtimeHook(t, runtime, "UserPromptSubmit", hookevent.HookPayload{SessionID: session})))
	}
	// Each Kitty instance owns a separate reducer slot for its window 7.
	requireAccepted(t, h.Handle(runtimeHook(t, onKittyB, "UserPromptSubmit", hookevent.HookPayload{SessionID: "session-kitty-b"})))

	if slot := slotOf(t, h, onKittyA); slot != 7 {
		t.Fatalf("Kitty runtime slot = %d; want its window ID 7", slot)
	}
	slots := map[int]runtimesession.ID{}
	for _, runtime := range []runtimesession.ID{onKittyA, onKittyB, onTmuxT, onTmuxU, onTmuxTRestarted} {
		slot := slotOf(t, h, runtime)
		if other, dup := slots[slot]; dup {
			t.Fatalf("runtimes %s and %s share slot %d", other, runtime, slot)
		}
		slots[slot] = runtime
		if runtime != onKittyA && runtime != onKittyB && !isRuntimeSyntheticSlot(slot) {
			t.Fatalf("tmux runtime %s got Kitty-range slot %d", runtime, slot)
		}
	}
	expect := map[runtimesession.ID]string{
		onKittyA: "claude:session-kitty-a", onKittyB: "claude:session-kitty-b", onTmuxT: "claude:session-tmux-t",
		onTmuxU: "claude:session-tmux-u", onTmuxTRestarted: "claude:session-tmux-t2",
	}
	for runtime, key := range expect {
		facts := slotFacts(h, slotOf(t, h, runtime))
		if len(facts) != 1 || !hasFact(facts, protocol.SourceHarness, key) {
			t.Fatalf("runtime %s facts = %+v; want exactly %s", runtime, facts, key)
		}
	}
	if hasFact(slotFacts(h, 7), protocol.SourceHarness, "claude:session-kitty-b") {
		t.Fatal("Kitty B's hook reached Kitty A's slot")
	}

	// Legacy hooks for both instances reach their own claimed slots.
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyA, "PreToolUse", hookevent.HookPayload{SessionID: "session-kitty-a"})))
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyB, "Stop", hookevent.HookPayload{SessionID: "session-kitty-b"})))

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 7, KittyListenOn: kittyA})
	if len(detail.Events) != 2 {
		t.Fatalf("slot 7 events = %+v; want the managed prompt and the legacy tool event", detail.Events)
	}
	if o := detail.Events[0].Origin; o == nil || o.RuntimeSessionID != string(onKittyA) || o.Legacy ||
		o.Backend != "kitty" || o.Instance != kittyA || o.Generation != "kitty-a-1" || o.Slot != 7 {
		t.Fatalf("managed event origin = %+v", o)
	}
	if o := detail.Events[1].Origin; o == nil || o.RuntimeSessionID != string(onKittyA) || !o.Legacy || o.Instance != kittyA {
		t.Fatalf("legacy event origin = %+v", o)
	}
	tmuxDetail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: slotOf(t, h, onTmuxT)})
	if len(tmuxDetail.Events) != 1 || tmuxDetail.Events[0].Origin == nil ||
		tmuxDetail.Events[0].Origin.Backend != "tmux" || tmuxDetail.Events[0].Origin.Pane != "%7" {
		t.Fatalf("tmux events = %+v", tmuxDetail.Events)
	}
}

func TestLegacyKittyHooksFromAnotherInstanceUseIndependentWindowState(t *testing.T) {
	t.Parallel()

	h, _ := newRuntimeTestHandler(t)
	first := legacyHook(t, 7, kittyA, "UserPromptSubmit", hookevent.HookPayload{SessionID: "first"})
	requireAccepted(t, h.Handle(first))
	second := legacyHook(t, 7, kittyB, "UserPromptSubmit", hookevent.HookPayload{SessionID: "second"})
	secondResponse := h.Handle(second)
	requireAccepted(t, secondResponse)
	if secondResponse.Delivery.SourceWindowID != 7 || secondResponse.Delivery.TargetWindowID != 7 {
		t.Fatalf("delivery leaked the daemon slot as a Kitty window: %+v", secondResponse.Delivery)
	}
	facts := slotFacts(h, 7)
	if len(facts) != 1 || !hasFact(facts, protocol.SourceHarness, "claude:first") {
		t.Fatalf("foreign legacy hook changed first instance's state: %+v", facts)
	}
	other := h.runtimeRoutes.kittySlotOf(kittyB, 7)
	if other == 7 || !hasFact(slotFacts(h, other), protocol.SourceHarness, "claude:second") {
		t.Fatalf("second instance did not get independent state in slot %d", other)
	}
	query := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if query.KittySlots[7].Socket != kittyA || query.KittySlots[other].Socket != kittyB ||
		query.KittySlots[other].WindowID != 7 || len(query.States) != 2 {
		t.Fatalf("state query lost collision provenance: slots=%+v states=%+v", query.KittySlots, query.States)
	}
}

func TestDirectStateUpdateRequiresInstanceWhenKittyNumbersCollide(t *testing.T) {
	t.Parallel()

	h, _ := newRuntimeTestHandler(t)
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyA, "UserPromptSubmit", hookevent.HookPayload{SessionID: "first"})))
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyB, "UserPromptSubmit", hookevent.HookPayload{SessionID: "second"})))
	if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 7, Action: "user_clear"}); resp.OK {
		t.Fatal("ambiguous direct update was accepted")
	}
	if !hasFact(slotFacts(h, 7), protocol.SourceHarness, "claude:first") {
		t.Fatal("ambiguous update changed the first instance")
	}
}

func TestHookContextUsesTheSnapshotInstancesSlot(t *testing.T) {
	t.Parallel()

	h, _ := newRuntimeTestHandler(t)
	h.stateMu.Lock()
	h.kittySlotLocked(kittyA, 7)
	second := h.kittySlotLocked(kittyB, 7)
	h.stateMu.Unlock()
	h.publishSnapshotFrom(probes.Publication{
		Socket: kittyB, CapturedAt: time.Now(),
		Windows: hookEventWindows("second", []kitty.Window{{ID: 7, IsActive: true,
			UserVars: kitty.UserVars{Profile: "managed", Harness: "claude"}}}),
	})
	if ctx := h.resolveHookEventContext(protocol.Request{WindowID: second, KittyListenOn: kittyB}); ctx.win == nil || ctx.win.ID != second {
		t.Fatalf("second instance context = %+v; want slot %d", ctx.win, second)
	}
	if ctx := h.resolveHookEventContext(protocol.Request{WindowID: 7, KittyListenOn: kittyA}); ctx.win != nil {
		t.Fatalf("first instance matched second instance snapshot: %+v", ctx.win)
	}
}

func TestNotificationTargetUsesTheRealKittyWindowNumber(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.stateMu.Lock()
	h.kittySlotLocked(kittyA, 7)
	alias := h.kittySlotLocked(kittyB, 7)
	h.rememberKittySocketLocked(alias, kittyB)
	h.stateMu.Unlock()
	target := h.notificationTarget(alias, "work")
	if target.WindowID != alias || target.KittyWindowID != 7 || target.KittyListenOn != kittyB {
		t.Fatalf("notification target = %+v; want slot %d focusing Kitty B window 7", target, alias)
	}
}

// Two Kitty instances each with a window 7: the second one's pending
// notification is kept under its collision slot, so cancelling that slot
// retracts it and the first instance's window 7 is never touched.
func TestNotificationBookkeepingIsKeyedByTheKittyCollisionSlot(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	notifier, _ := newTestNotifier(&config.NotificationConfig{Permission: true, PermissionDebounceMs: 60000})
	h.notifier = notifier
	h.stateMu.Lock()
	h.kittySlotLocked(kittyA, 7)
	alias := h.kittySlotLocked(kittyB, 7)
	h.rememberKittySocketLocked(alias, kittyB)
	h.stateMu.Unlock()

	h.maybeNotify(alias, "api", "work", "permission", false)
	if !notifier.HasPending(alias) || notifier.HasPending(7) {
		t.Fatalf("pending: slot %d = %v, window 7 = %v; want only the slot", alias, notifier.HasPending(alias), notifier.HasPending(7))
	}
	h.cancelPendingNotification(alias)
	if notifier.HasPending(alias) {
		t.Fatal("cancelling the collision slot left its notification pending")
	}
}

func TestScreenProbeBindingTargetsTheCollidingKittySlot(t *testing.T) {
	t.Parallel()

	h, _ := newRuntimeTestHandler(t)
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyA, "UserPromptSubmit", hookevent.HookPayload{SessionID: "first"})))
	requireAccepted(t, h.Handle(legacyHook(t, 7, kittyB, "UserPromptSubmit", hookevent.HookPayload{SessionID: "second"})))
	alias := h.runtimeRoutes.kittySlotOf(kittyB, 7)
	h.publishSnapshotFrom(probes.Publication{
		Socket: kittyB, CapturedAt: time.Now(), ReducerReceipts: h.captureSnapshotReducerReceiptsFor(kittyB),
		Windows: hookEventWindows("second", []kitty.Window{{ID: 7, IsActive: true,
			UserVars: kitty.UserVars{Profile: "managed", Harness: "claude"}}}),
	})
	snapshot, ready := h.cachedScreenProbeSnapshot()
	if !ready {
		t.Fatal("probe snapshot is cold")
	}
	bindings := h.screenProbeBindings(snapshot, harness.Claude)
	if len(bindings) != 1 || bindings[0].TargetWindowID != 7 || bindings[0].TargetSlot() != alias ||
		bindings[0].SourceSlot() != alias || bindings[0].KittySocket != kittyB {
		t.Fatalf("colliding probe bindings = %+v; want Kitty B window 7 in slot %d", bindings, alias)
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
	// The Kitty socket memory never learns a tmux slot.
	slot := slotOf(t, h, runtime)
	h.stateMu.Lock()
	_, remembered := h.kittySockets[slot]
	h.stateMu.Unlock()
	if remembered {
		t.Fatal("tmux slot recorded a Kitty socket")
	}
}

func TestManagedHookTranslationRefusals(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	pending, ended, stale, abandoned, badKitty := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(pending, runtimesession.StatePending, runtimesession.BackendTmux, tmuxT, "g", "%1")
	directory.put(ended, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "g", "%2")
	directory.put(stale, runtimesession.StateStale, runtimesession.BackendTmux, tmuxT, "g", "%3")
	directory.put(abandoned, runtimesession.StateAbandoned, runtimesession.BackendTmux, tmuxT, "g", "%4")
	directory.put(badKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "g", "window_id:7")

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
		{"non-window kitty ref", runtimeHook(t, badKitty, "Stop", payload), protocol.OriginReasonInvalidRuntimeTarget, false},
		{"legacy slot-range window", legacyHook(t, runtimeSlotBase+1, kittyA, "Stop", payload), protocol.OriginReasonTargetConflict, false},
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

func TestManagedHooksWithoutRegistryAreRetryableAndLegacyStillRoutes(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: fakeOSWindowsWithTab(42, "W:api")})
	requireRefused(t, h.Handle(runtimeHook(t, newRuntimeID(t), "Stop", hookevent.HookPayload{SessionID: "s1"})),
		protocol.OriginReasonRuntimeRegistryUnavailable, true)

	directory := newFakeRuntimeDirectory()
	directory.err = errors.New("registry unreadable")
	h.SetRuntimeSessionDirectory(directory)
	requireRefused(t, h.Handle(runtimeHook(t, newRuntimeID(t), "Stop", hookevent.HookPayload{SessionID: "s1"})),
		protocol.OriginReasonRuntimeRegistryUnavailable, true)

	before := directory.lookupCount()
	requireAccepted(t, h.Handle(legacyHook(t, 42, kittyA, "UserPromptSubmit", hookevent.HookPayload{SessionID: "legacy"})))
	if directory.lookupCount() != before {
		t.Fatal("a legacy Kitty hook consulted the runtime registry")
	}
	if !hasFact(slotFacts(h, 42), protocol.SourceHarness, "claude:legacy") {
		t.Fatalf("legacy hook did not reach window 42: %+v", slotFacts(h, 42))
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
		t.Fatal("ended session's non-Kitty slot was not closed")
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

func TestSuccessorInTheSameKittyWindowStartsClean(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	old, successor := newRuntimeID(t), newRuntimeID(t)
	directory.put(old, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	requireAccepted(t, h.Handle(runtimeHook(t, old, "UserPromptSubmit", hookevent.HookPayload{SessionID: "old"})))
	requireAccepted(t, h.Handle(runtimeHook(t, old, "SubagentStart", hookevent.HookPayload{SessionID: "old", AgentID: "agent-old"})))
	if icon := slotIcon(h, 7); icon != "subagent" {
		t.Fatalf("old icon = %q; want subagent", icon)
	}

	// While the old session is live, nothing else may claim its window.
	directory.put(successor, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	requireRefused(t, h.Handle(runtimeHook(t, successor, "UserPromptSubmit", hookevent.HookPayload{SessionID: "new"})),
		protocol.OriginReasonTargetConflict, false)

	// Maintenance observes the end and releases the binding; the Kitty slot
	// stays until Kitty reports the window gone.
	directory.setState(old, runtimesession.StateEnded)
	h.retireEndedRuntimeSessions(time.Now())
	h.stateMu.Lock()
	_, bound := h.runtimeRoutes.binding(7)
	_, kept := h.store[7]
	h.stateMu.Unlock()
	if bound || !kept {
		t.Fatalf("after retirement bound=%v kept=%v; want released binding and kept Kitty state", bound, kept)
	}

	requireAccepted(t, h.Handle(runtimeHook(t, successor, "UserPromptSubmit", hookevent.HookPayload{SessionID: "new"})))
	if slot := slotOf(t, h, successor); slot != 7 {
		t.Fatalf("successor slot = %d; want window 7", slot)
	}
	facts := slotFacts(h, 7)
	if len(facts) != 1 || !hasFact(facts, protocol.SourceHarness, "claude:new") {
		t.Fatalf("successor facts = %+v; want the old session's prompt and subagent gone", facts)
	}
	if icon := slotIcon(h, 7); icon != "bolt" {
		t.Fatalf("successor icon = %q; want bolt", icon)
	}
	requireRefused(t, h.Handle(runtimeHook(t, old, "SubagentStop", hookevent.HookPayload{SessionID: "old", AgentID: "agent-old"})),
		protocol.OriginReasonRuntimeSessionEnded, false)
}

func TestMaintenanceClosesNonKittySlotsOfEndedSessions(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	ending, staying := newRuntimeID(t), newRuntimeID(t)
	directory.put(ending, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(staying, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%2")
	requireAccepted(t, h.Handle(runtimeHook(t, ending, "UserPromptSubmit", hookevent.HookPayload{SessionID: "a"})))
	requireAccepted(t, h.Handle(runtimeHook(t, staying, "UserPromptSubmit", hookevent.HookPayload{SessionID: "b"})))
	endingSlot, stayingSlot := slotOf(t, h, ending), slotOf(t, h, staying)

	// A Kitty listing that cannot contain either slot must not prune them.
	h.pruneClosedWindows(time.Now(), map[int]bool{})
	if slotFacts(h, endingSlot) == nil || slotFacts(h, stayingSlot) == nil {
		t.Fatal("a Kitty prune closed a tmux runtime slot")
	}

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
// reads the registry cache and daemon memory only, so a wedged Kitty listing
// cannot delay a managed hook any more than a legacy one.
func TestManagedHookAcknowledgementNeverWaitsOnBackendDiscovery(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	runtimes := []runtimesession.ID{newRuntimeID(t), newRuntimeID(t)}
	directory.put(runtimes[0], runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(runtimes[1], runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	release := make(chan struct{})
	fake := &fakeKittyClient{windows: fakeOSWindowsWithTab(7, "W:api")}
	h.SetKittyClient(fake)
	fake.mu.Lock()
	fake.beforeListWindows = func() { <-release }
	fake.mu.Unlock()
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
		t.Fatal("managed hook acknowledgement blocked behind a wedged Kitty list")
	}
}
