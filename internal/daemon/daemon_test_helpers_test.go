package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// Older reducer tests address an isolated handler by its in-memory slot. Give
// those requests a test origin; production ingress still requires a registered
// runtime session. Runtime routing tests install a directory and exercise the
// real translation path.
func init() {
	legacyStateUpdate = func(h *Handler, req protocol.Request) protocol.Response {
		if h.runtimeDirectory != nil {
			return protocol.Response{OK: false, Error: protocol.OriginReasonInvalidRuntimeSession}
		}
		return h.handleStateUpdate(req)
	}
	legacyHookTranslator = func(h *Handler, req protocol.Request) (protocol.Request, *originRejection) {
		if h.runtimeDirectory != nil {
			return req, rejectOrigin(protocol.OriginReasonInvalidRuntimeSession)
		}
		if req.WindowID <= 0 {
			return req, nil
		}
		req.Origin = &protocol.HookOrigin{Backend: "tmux", Slot: req.WindowID}
		return req, nil
	}
}

type notifyCall struct {
	windowID int
	account  string
	title    string
	subtitle string
	clicked  bool
}

// callRecorder is a thread-safe collector for dispatcher invocations. The
// notify dispatch runs in its own goroutine, so every read from the test
// must hold the same mutex as the write — otherwise the race detector flags
// it. Handler-level tests in this package use this helper to avoid
// hand-rolling per-test mutex bookkeeping.
type callRecorder struct {
	mu          sync.Mutex
	calls       []notifyCall
	clickResult bool
}

func (r *callRecorder) add(c notifyCall) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
}

func (r *callRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// snapshot returns a copy so tests can iterate without holding the mutex.
func (r *callRecorder) snapshot() []notifyCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notifyCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func newTestNotifier(cfg *config.NotificationConfig) (*notification.Notifier, *callRecorder) {
	n := notification.NewNotifier(cfg)
	rec := &callRecorder{}
	n.SetBackend(func(_ context.Context, windowID int, account, title, subtitle string) (bool, error) {
		rec.mu.Lock()
		clicked := rec.clickResult
		rec.mu.Unlock()
		rec.add(notifyCall{
			windowID: windowID,
			account:  account,
			title:    title,
			subtitle: subtitle,
			clicked:  clicked,
		})
		if clicked {
			return true, nil
		}
		return false, nil
	})
	return n, rec
}

func newTestHandler() *Handler {
	h, _ := newTestHandlerWithNotificationConfig()
	return h
}

func (h *Handler) setMutedFromSnapshot(windowID int, muted bool) {
	if windowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.ensureWindowStateLocked(windowID).Muted = muted
	h.stateMu.Unlock()
}

// newTestHandlerWithNotificationConfig is newTestHandler for tests that need to
// toggle a notification setting mid-test. The notifier keeps the returned
// pointer, so a later write to it is observed by the next dispatch decision.
func newTestHandlerWithNotificationConfig() (*Handler, *config.NotificationConfig) {
	guard := activity.NewStateGuard(time.Hour)
	cfg := &config.NotificationConfig{}
	notifier := notification.NewNotifier(cfg)
	notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
		return false, nil
	})
	h := NewHandler(guard, notifier)
	return h, cfg
}

// windowEffective returns a stored slot's reduced and rendered state.
func windowEffective(t *testing.T, h *Handler, slot int) (state.EffectiveState, string) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok {
		t.Fatalf("slot %d has no state", slot)
	}
	return st.Effective, rendering.DesiredIcon(st)
}

func shadowMeta(t *testing.T, h *Handler, slot int) protocol.WindowDebugShadow {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return eventlog.CloneWindowMetadata(h.windowMetadata[slot]).Shadow
}

func readCodexCapture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "screenstate", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	return string(data)
}

func lastEventNamed(events []protocol.SessionEvent, name string) (protocol.SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == name {
			return events[i], true
		}
	}
	return protocol.SessionEvent{}, false
}

func countEventsNamed(t *testing.T, h *Handler, slot int, name string) int {
	t.Helper()
	count := 0
	for _, event := range h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: slot}).Events {
		if event.Event == name {
			count++
		}
	}
	return count
}

func shortenCompanionCadence() func() {
	debounce, safety, heartbeat := companionDebounce, companionSafetyTick, companionHeartbeat
	companionDebounce, companionSafetyTick, companionHeartbeat = 5*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
	return func() { companionDebounce, companionSafetyTick, companionHeartbeat = debounce, safety, heartbeat }
}

func nextFrameOfType(t *testing.T, sub *ipc.Subscription, typ protocol.StreamFrameType) protocol.StreamFrame {
	t.Helper()
	type result struct {
		frame protocol.StreamFrame
		err   error
	}
	got := make(chan result, 1)
	go func() {
		for {
			frame, err := sub.Next()
			if err != nil || frame.Type == typ {
				got <- result{frame, err}
				return
			}
		}
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("waiting for %s frame: %v", typ, r.err)
		}
		return r.frame
	case <-time.After(5 * time.Second):
		_ = sub.Close()
		t.Fatalf("no %s frame within 5s", typ)
		return protocol.StreamFrame{}
	}
}

func decodeCompanionSnapshot(t *testing.T, data json.RawMessage) protocol.CompanionSnapshot {
	t.Helper()
	var snap protocol.CompanionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snap
}

func terminalHoldPending(t *testing.T, h *Handler, slot int) (pendingStopHold, bool) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	pending, ok := h.pendingStopHolds[slot]
	return pending, ok
}

func windowFacts(t *testing.T, h *Handler, slot int) []protocol.SourceFactSnapshot {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok || st.Reducer == nil {
		t.Fatalf("slot %d has no reducer", slot)
	}
	return st.Reducer.Facts()
}

func assertNoProbeStopFact(t *testing.T, h *Handler, slot int) {
	t.Helper()
	for _, fact := range windowFacts(t, h, slot) {
		if fact.Kind == protocol.SourceProbe {
			t.Fatalf("probe fact %+v settled during a held stop", fact)
		}
	}
}

func hookPayloadJSON(t *testing.T, payload hookevent.HookPayload) string {
	t.Helper()
	if payload.Source == events.ASLHookSource {
		payload.SchemaVersion = events.ASLHookSchemaVersion
		payload.ContractSetID = aslcontract.ContractSetID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hook payload: %v", err)
	}
	return string(data)
}

func stubDaemonLoopDiscover(t *testing.T, candidates []agentloop.Candidate, err error) {
	t.Helper()
	result := agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: true}
	for _, candidate := range candidates {
		result.Observations = append(result.Observations, agentloop.PlanObservation{
			StateDir: candidate.StateDir, PlanDir: candidate.PlanDir, RunID: candidate.RunID,
			Presence: agentloop.PresencePresent, Reason: agentloop.ReasonLiveProcess, Candidate: candidate,
		})
	}
	stubDaemonDiscoverPlans(t, result, err)
}

func stubDaemonDiscoverPlans(t *testing.T, result agentloop.DiscoveryResult, err error) {
	t.Helper()
	old := daemonLoopDiscoverPlans
	daemonLoopDiscoverPlans = func(agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
		out := result
		out.Observations = append([]agentloop.PlanObservation(nil), result.Observations...)
		return out, err
	}
	t.Cleanup(func() { daemonLoopDiscoverPlans = old })
}

func seedUnseen(h *Handler, slot int, unseen, muted bool) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.ensureWindowStateLocked(slot)
	st.Unseen, st.Muted = unseen, muted
}

func seedReducerFact(h *Handler, slot int, now time.Time, events ...protocol.ReducerEvent) {
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(slot)
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	for _, ev := range events {
		st.Reducer.Apply(ev)
	}
	h.publishEffectiveLocked(slot, st, st.Reducer.Reduce(now.Unix()))
	h.stateMu.Unlock()
}

func effectiveState(h *Handler, slot int) state.EffectiveState {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.store[slot].Effective
}

func factSnapshotBySource(facts []protocol.SourceFactSnapshot, source protocol.SourceID) *protocol.SourceFactSnapshot {
	for i := range facts {
		if facts[i].Source() == source {
			fact := facts[i]
			return &fact
		}
	}
	return nil
}

const (
	terminalHoldTestDelay    = 6 * time.Second
	terminalHoldTestTimeout  = 15 * time.Second
	copilotTurnEndTranscript = `{"type":"session.start","data":{}}` + "\n" + `{"type":"assistant.turn_start","data":{}}` + "\n" + `{"type":"assistant.turn_end","data":{}}` + "\n"
)

func writeCopilotTranscript(t *testing.T, home, session, content string) {
	t.Helper()
	dir := filepath.Join(home, "session-state", session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func appendCopilotTranscript(t *testing.T, home, session, content string) {
	t.Helper()
	path := filepath.Join(home, "session-state", session, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript for append: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
}

func lastStopEvent(t *testing.T, h *Handler, slot int) protocol.SessionEvent {
	t.Helper()
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: slot})
	for i := len(detail.Events) - 1; i >= 0; i-- {
		if detail.Events[i].Event == "Stop" {
			return detail.Events[i]
		}
	}
	t.Fatalf("no committed Stop event in %+v", detail.Events)
	return protocol.SessionEvent{}
}

func loadClaudeContextLimitFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "contextlimit", "testdata", "claude_context_limit_full.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(data)
}

func unseen(t *testing.T, h *Handler, slot int) bool {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok {
		t.Fatalf("slot %d has no state", slot)
	}
	return st.Unseen
}

func stubLoopDiscovery(t *testing.T) *bool {
	t.Helper()
	called := false
	old := daemonLoopDiscoverPlans
	daemonLoopDiscoverPlans = func(agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
		called = true
		return agentloop.DiscoveryResult{RootPresent: true}, nil
	}
	t.Cleanup(func() { daemonLoopDiscoverPlans = old })
	return &called
}

func windowEventNames(h *Handler, slot int) []string {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	var names []string
	if st, ok := h.store[slot]; ok {
		for _, ev := range st.EventLog {
			names = append(names, ev.Event)
		}
	}
	return names
}

func containsEvent(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
