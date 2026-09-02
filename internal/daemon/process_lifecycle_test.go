package daemon

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type fakeHarnessProcessInspector struct {
	mu       sync.Mutex
	statuses map[protocol.HarnessProcessIdentity]HarnessProcessInspection
	calls    [][]protocol.HarnessProcessIdentity
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (f *fakeHarnessProcessInspector) InspectHarnessProcesses(
	ctx context.Context,
	identities []protocol.HarnessProcessIdentity,
) map[protocol.HarnessProcessIdentity]HarnessProcessInspection {
	f.mu.Lock()
	f.calls = append(f.calls, append([]protocol.HarnessProcessIdentity(nil), identities...))
	f.mu.Unlock()
	if f.entered != nil {
		f.once.Do(func() { close(f.entered) })
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil
		}
	}
	out := make(map[protocol.HarnessProcessIdentity]HarnessProcessInspection, len(identities))
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, identity := range identities {
		out[identity] = f.statuses[identity]
	}
	return out
}

func (f *fakeHarnessProcessInspector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func applyLifecycleEvent(
	t *testing.T,
	h *Handler,
	windowID int,
	identity protocol.HarnessProcessIdentity,
	sessionID string,
	action string,
	seq uint64,
	pipeMode bool,
) {
	t.Helper()
	event := "LifecycleTestEvent"
	if action == "stop" || action == "stop_failure" {
		event = "Stop"
	}
	normalized := protocol.NormalizedEvent{
		Source:         protocol.SourceID{Kind: protocol.SourceHarness, Key: identity.Harness + ":" + sessionID},
		Action:         action,
		Event:          event,
		Seq:            seq,
		At:             time.Now().Unix(),
		SessionID:      sessionID,
		PipeMode:       pipeMode,
		HarnessProcess: identity,
	}
	resp := h.handleStateUpdate(protocol.Request{
		Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: event,
		Harness: identity.Harness, Account: "work", SessionTitle: "W:api",
		PipeMode: pipeMode, Normalized: &normalized,
	})
	if !resp.OK {
		t.Fatalf("apply lifecycle event: %+v", resp)
	}
}

func lifecycleEvents(st *state.WindowState) []protocol.SessionEvent {
	if st == nil {
		return nil
	}
	out := make([]protocol.SessionEvent, 0)
	for _, event := range st.EventLog {
		if event.Event == eventHarnessProcessExited {
			out = append(out, event)
		}
	}
	return out
}

// lifecycleExitEvent returns the single process-exit row a window recorded.
func lifecycleExitEvent(t *testing.T, h *Handler, windowID int) protocol.SessionEvent {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	events := lifecycleEvents(h.store[windowID])
	if len(events) != 1 {
		t.Fatalf("window %d process-exit rows = %+v, want exactly one", windowID, events)
	}
	return events[0]
}

func TestProcessLifecycleAliveAndUnknownPreserveState(t *testing.T) {
	for _, status := range []HarnessProcessInspection{HarnessProcessAliveSameInstance, HarnessProcessUnknown} {
		t.Run(string(status), func(t *testing.T) {
			h := newTestHandler()
			identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5101, StartToken: "Thu Jul 16 10:20:30 2026"}
			applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
			h.renderQueue = rendering.NewQueue()
			h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{identity: status}})

			h.stateMu.Lock()
			beforeEffective := h.store[42].Effective
			beforeReceipt := h.store[42].ShadowSeq
			beforeEvents := len(h.store[42].EventLog)
			h.stateMu.Unlock()
			beforeGuard := h.guard.Current(42)
			if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 0 {
				t.Fatalf("committed = %d, want 0", committed)
			}

			h.stateMu.Lock()
			st := h.store[42]
			if st.Effective != beforeEffective || st.ShadowSeq != beforeReceipt || len(st.EventLog) != beforeEvents {
				t.Fatalf("state changed: %+v", st)
			}
			h.stateMu.Unlock()
			if got := h.guard.Current(42); got != beforeGuard {
				t.Fatalf("guard = %q, want %q", got, beforeGuard)
			}
			if !h.renderQueue.Empty() {
				t.Fatal("no-op inspection queued a render")
			}
		})
	}
}

func TestProcessLifecycleMissingAndReplacedSettleInteractiveSilently(t *testing.T) {
	for _, status := range []HarnessProcessInspection{HarnessProcessMissing, HarnessProcessReplacedInstance} {
		t.Run(string(status), func(t *testing.T) {
			notifier, calls := newTestNotifier(&config.NotificationConfig{Stop: true})
			h := NewHandler(NewStateGuard(time.Hour), notifier)
			identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5201, StartToken: "Thu Jul 16 10:20:30 2026"}
			applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
			h.renderQueue = rendering.NewQueue()
			h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{identity: status}})

			if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
				t.Fatalf("committed = %d, want 1", committed)
			}
			h.stateMu.Lock()
			st := h.store[42]
			events := lifecycleEvents(st)
			if st.Effective.Icon != "stop" || st.LastAction != "stop" || st.LastPipeMode || st.SubagentCount != 0 {
				t.Fatalf("settled state = %+v", st)
			}
			if len(events) != 1 || events[0].Action != "stop" || events[0].Source != "claude" ||
				events[0].ProcessObservation != string(status) {
				t.Fatalf("lifecycle events = %+v", events)
			}
			h.stateMu.Unlock()
			if got := h.guard.Current(42); got != "stop" {
				t.Fatalf("guard = %q, want stop", got)
			}
			if calls.len() != 0 {
				t.Fatalf("synthetic process exit dispatched %d notifications", calls.len())
			}
			if h.renderQueue.Empty() {
				t.Fatal("visible interactive exit did not queue a render")
			}
		})
	}
}

func TestProcessLifecycleSettlementPublishesSafeDiagnosticEvidenceOnce(t *testing.T) {
	paths := diagnostics.PathsForRoot(t.TempDir())
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHandler()
	h.setDecisionJournal(journal)
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5242, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	h.renderQueue = rendering.NewQueue()
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{
		identity: HarnessProcessMissing,
	}})
	observedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	if committed := h.processLifecycleTick(context.Background(), observedAt); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	if committed := h.processLifecycleTick(context.Background(), observedAt.Add(time.Second)); committed != 0 {
		t.Fatalf("duplicate observation committed = %d, want 0", committed)
	}

	h.stateMu.Lock()
	events := lifecycleEvents(h.store[42])
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if len(events) != 1 {
		t.Fatalf("lifecycle events = %+v, want one", events)
	}
	event := events[0]
	if event.Event != protocol.EventHarnessProcessExited || event.Reason != state.HarnessProcessExitReasonInteractive ||
		event.ProcessHarness != identity.Harness || event.ProcessPID != identity.PID ||
		event.ProcessStartToken != identity.StartToken || event.ProcessObservation != string(HarnessProcessMissing) ||
		event.ProcessMode != "interactive" || event.SourceKind != string(protocol.SourceHarness) ||
		event.SourceKey != "claude:session" || event.OriginWindowID != 42 ||
		event.WithdrawnFacts == nil || *event.WithdrawnFacts != 0 ||
		event.ClearedSubagents == nil || *event.ClearedSubagents != 0 ||
		event.SynthesizedStop == nil || !*event.SynthesizedStop ||
		event.TerminalHookEvidence != processTerminalHookEvidenceAbsent || event.RenderGeneration == 0 ||
		event.EffectiveBefore == nil || event.EffectiveBefore.Icon != "bolt" ||
		event.EffectiveAfter == nil || event.EffectiveAfter.Icon != "stop" || len(event.ProcessSources) != 1 {
		t.Fatalf("process lifecycle event = %+v", event)
	}
	if meta.Watchdog.ProcessExits != 1 || meta.Watchdog.LastProcessExitReason != state.HarnessProcessExitReasonInteractive ||
		meta.Watchdog.LastProcessExitObservation != string(HarnessProcessMissing) ||
		meta.Watchdog.LastProcessExitAt != observedAt.Unix() || meta.Renderer.QueuedGen != event.RenderGeneration ||
		len(meta.Renderer.Generations) == 0 || meta.Renderer.Generations[len(meta.Renderer.Generations)-1].Generation != event.RenderGeneration {
		t.Fatalf("process lifecycle metadata = %+v renderer=%+v", meta.Watchdog, meta.Renderer)
	}

	records, err := journal.Read(context.Background(), diagnostics.Selector{Stages: []string{processLifecycleJournalStage}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("journal records = %+v, want one committed settlement", records)
	}
	record := records[0]
	if record.Outcome != "settled" || record.Reason != state.HarnessProcessExitReasonInteractive ||
		record.Evidence["event"] != protocol.EventHarnessProcessExited || record.Evidence["process_start_token"] != identity.StartToken ||
		record.Evidence["terminal_hook_evidence"] != processTerminalHookEvidenceAbsent ||
		record.Evidence["render_generation"] == "" || record.Evidence["before_icon"] != "bolt" ||
		record.Evidence["after_icon"] != "stop" {
		t.Fatalf("process lifecycle journal record = %+v", record)
	}
	for _, forbidden := range []string{"command", "command_line", "prompt", "payload", "path"} {
		if _, ok := record.Evidence[forbidden]; ok {
			t.Fatalf("lifecycle evidence contains forbidden field %q: %+v", forbidden, record.Evidence)
		}
	}
}

func TestProcessLifecycleInspectionFailuresAreRateLimitedAndHealthyPollsSilent(t *testing.T) {
	paths := diagnostics.PathsForRoot(t.TempDir())
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHandler()
	h.setDecisionJournal(journal)
	identity := protocol.HarnessProcessIdentity{Harness: "codex", PID: 5243, StartToken: "Thu Jul 16 10:21:30 2026"}
	applyLifecycleEvent(t, h, 43, identity, "session", "active", 1, false)
	inspector := &fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{
		identity: HarnessProcessUnknown,
	}}
	h.SetHarnessProcessInspector(inspector)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{base, base.Add(time.Second), base.Add(time.Minute + time.Second)} {
		if committed := h.processLifecycleTick(context.Background(), at); committed != 0 {
			t.Fatalf("unknown inspection committed = %d", committed)
		}
	}
	inspector.mu.Lock()
	inspector.statuses[identity] = HarnessProcessAliveSameInstance
	inspector.mu.Unlock()
	if committed := h.processLifecycleTick(context.Background(), base.Add(2*time.Minute+time.Second)); committed != 0 {
		t.Fatalf("healthy inspection committed = %d", committed)
	}

	records, err := journal.Read(context.Background(), diagnostics.Selector{Stages: []string{processLifecycleJournalStage}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("inspection failure records = %+v, want two rate-limited rows", records)
	}
	for _, record := range records {
		if record.Outcome != "degraded" || record.Reason != "inspection_unknown" ||
			record.Evidence["attempted_processes"] != "1" || record.Evidence["unknown_processes"] != "1" {
			t.Fatalf("inspection failure record = %+v", record)
		}
	}
}

func TestProcessLifecycleCancelsPendingPermissionNotification(t *testing.T) {
	notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true, PermissionDebounceMs: 60_000})
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5251, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "permission", 1, false)
	if !notifier.HasPending(42) {
		t.Fatal("permission event did not schedule pending notification")
	}
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{identity: HarnessProcessMissing}})

	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	if notifier.HasPending(42) {
		t.Fatal("exited process left a pending permission notification")
	}
	if calls.len() != 0 {
		t.Fatalf("process exit dispatched %d notifications", calls.len())
	}
}

func TestProcessLifecyclePipeExitRevealsParentAndQueuesRender(t *testing.T) {
	h := newTestHandler()
	parent := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5301, StartToken: "Thu Jul 16 10:20:30 2026"}
	pipe := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5302, StartToken: "Thu Jul 16 10:21:30 2026"}
	applyLifecycleEvent(t, h, 42, parent, "parent", "stop", 1, false)
	applyLifecycleEvent(t, h, 42, pipe, "pipe", "active", 1, true)
	h.renderQueue = rendering.NewQueue()
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{pipe: HarnessProcessMissing}})

	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	h.stateMu.Lock()
	st := h.store[42]
	meta := h.windowMetadata[42]
	if st.Effective.Icon != "stop" || st.Effective.Source.Key != "claude:parent" || st.LastAction != "stop" || st.LastPipeMode {
		t.Fatalf("revealed state = %+v", st)
	}
	if meta.Durable.SessionTitle != "W:api" || meta.Durable.Profile != "work" || meta.Durable.Harness != "claude" {
		t.Fatalf("durable identity was discarded: %+v", meta.Durable)
	}
	h.stateMu.Unlock()
	if h.renderQueue.Empty() {
		t.Fatal("pipe exit visible transition did not queue render")
	}
}

func TestProcessLifecycleResetClearsWithoutSynthesizingStop(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5401, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	h.stateMu.Lock()
	h.store[42].PendingReset = state.ResetFlowClearing
	h.stateMu.Unlock()
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{identity: HarnessProcessMissing}})

	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[42]
	events := lifecycleEvents(st)
	if st.PendingReset != state.ResetFlowClearing || st.Effective.Icon != "" || st.LastAction != "" {
		t.Fatalf("reset state = %+v", st)
	}
	if len(events) != 1 || events[0].Action != "idle" || events[0].Reason == "" {
		t.Fatalf("reset lifecycle event = %+v", events)
	}
}

func TestProcessLifecycleLeavesASLFactUntouched(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5501, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	aslSource := protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}
	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(protocol.ReducerEvent{Source: aslSource, RunID: "run-1", Phase: state.ASLPhaseBetween, Seq: 1, At: time.Now().Unix()})
	st.ShadowSeq++
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(time.Now().Unix()))
	before := factSnapshotBySource(st.Reducer.Facts(), aslSource)
	h.stateMu.Unlock()
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]HarnessProcessInspection{identity: HarnessProcessMissing}})

	if committed := h.processLifecycleTick(context.Background(), time.Now()); committed != 1 {
		t.Fatalf("committed = %d, want 1", committed)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	after := factSnapshotBySource(h.store[42].Reducer.Facts(), aslSource)
	if before == nil || after == nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("ASL fact changed: before=%+v after=%+v", before, after)
	}
	if !h.store[42].Effective.Looping || h.store[42].Effective.Icon != "" {
		t.Fatalf("ASL between-turn state = %+v", h.store[42].Effective)
	}
}
