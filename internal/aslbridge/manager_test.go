package aslbridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock is a manually advanced clock. Supervision timing (stale window,
// healthy-restart reset) is decided by it rather than by wall time, so those
// behaviours are asserted exactly instead of approximately.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// waitForReason blocks until the published capability block reports a failure
// mentioning want. Supervision is asynchronous, so a test asserts on the
// observed reason rather than on having closed the manager.
func waitForReason(t *testing.T, m *Manager, want string) {
	t.Helper()
	waitFor(t, "reason containing "+want, func() bool {
		return strings.Contains(m.Status().Reason, want)
	})
	if m.Status().Available {
		t.Fatalf("a failed session must not publish availability: %+v", m.Status())
	}
}

// startManager wires a manager to one launcher and guarantees it is joined.
func startManager(t *testing.T, opts Options) *Manager {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = discardLogger()
	}
	m := New(opts)
	t.Cleanup(m.Close)
	m.Start(context.Background())
	return m
}

func TestDisabledModeNeverProbes(t *testing.T) {
	m := startManager(t, Options{
		Mode:    ModeDisabled,
		Command: "asl",
		Launcher: func(context.Context) (*Child, error) {
			t.Error("disabled mode must never start a bridge child")
			return nil, errors.New("unreachable")
		},
	})

	status := m.Status()
	if status.Mode != string(ModeDisabled) || status.Available {
		t.Fatalf("disabled status = %+v", status)
	}
	if _, _, err := m.Snapshot(); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Snapshot error = %v, want ErrDisabled", err)
	}
	if _, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Act error = %v, want ErrDisabled", err)
	}
}

func TestRequiredModeReportsUnavailableWithoutBlocking(t *testing.T) {
	m := startManager(t, Options{
		Mode:    ModeRequired,
		Command: "asl",
		Launcher: func(context.Context) (*Child, error) {
			return nil, fmt.Errorf("asl bridge: %w", &exec.Error{Name: "asl", Err: exec.ErrNotFound})
		},
	})
	waitForReason(t, m, "not found")

	if status := m.Status(); status.Mode != string(ModeRequired) {
		t.Fatalf("mode = %q", status.Mode)
	}
	// A missing executable stops supervision rather than respawning forever.
	m.Close()
	if restarts := m.Status().RestartCount; restarts != 0 {
		t.Fatalf("restart count = %d, want no retry against a missing executable", restarts)
	}
}

func TestHandshakePublishesAvailabilityAndMutations(t *testing.T) {
	fake := newFakeBridge(t, serveSnapshots)
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	status := m.Status()
	if status.Protocol != ProtocolVersion || status.Version != "9.9.9" {
		t.Fatalf("status = %+v", status)
	}
	if status.Executable != "/fake/asl" {
		t.Fatalf("executable = %q", status.Executable)
	}
	if !hasCapability(status.Capabilities, MethodRunsSnapshot) {
		t.Fatalf("capabilities = %v", status.Capabilities)
	}
	if _, meta, err := m.Snapshot(); err != nil || meta.Stale {
		t.Fatalf("Snapshot() = %+v, %v; want a fresh snapshot", meta, err)
	}
}

func TestHelloProtocolMismatchIsChildFailure(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.writeFrame(map[string]any{
			"type": "hello", "protocol": 2, "asl_version": "9.9.9",
			"capabilities": []string{MethodRunsSnapshot},
		})
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "protocol")
}

func TestHelloWithoutSnapshotCapabilityIsChildFailure(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello(MethodQuestionGet)
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "runs.snapshot")
}

func TestResponseBeforeHelloIsChildFailure(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.respond("1", emptySnapshot())
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "response before its hello")
}

func TestDuplicateHelloIsChildFailure(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		f.hello()
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "duplicate hello")
}

func TestStdoutContaminationEndsSession(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		f.writeLine("Traceback (most recent call last):")
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "non-protocol")
}

func TestOversizeFrameEndsSession(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		f.writeLine(strings.Repeat("x", MaxFrameBytes+1))
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "ceiling")
}

func TestUnknownFrameTypeEndsSession(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		f.writeFrame(map[string]any{"type": "banner", "protocol": ProtocolVersion})
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "unknown type")
}

func TestStderrTailStaysOutOfPublishedReason(t *testing.T) {
	const secret = "plan /home/op/secret-plan blew up"
	launcher := func(context.Context) (*Child, error) {
		fake := newFakeBridge(t, func(f *fakeBridge) {
			f.writeLine("Traceback (most recent call last):")
			<-f.exited
		})
		child := fake.child()
		child.Stderr = strings.NewReader(secret + "\n")
		return child, nil
	}
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: launcher,
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "handshake failed")

	if reason := m.Status().Reason; strings.Contains(reason, "secret-plan") {
		t.Fatalf("reason = %q, want the stderr tail kept private", reason)
	}
}

func TestRestartBackoffFollowsSchedule(t *testing.T) {
	var (
		mu     sync.Mutex
		delays []time.Duration
	)
	done := make(chan struct{})
	launcher := func(context.Context) (*Child, error) {
		fake := newFakeBridge(t, func(f *fakeBridge) {
			f.hello()
			f.exit()
		})
		return fake.child(), nil
	}
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: launcher,
		Sleep: func(_ context.Context, d time.Duration) bool {
			mu.Lock()
			delays = append(delays, d)
			full := len(delays) == 4
			mu.Unlock()
			if full {
				close(done)
				return false
			}
			return true
		},
	})
	<-done
	m.Close()

	mu.Lock()
	got := append([]time.Duration(nil), delays...)
	mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delays = %v, want %v", got, want)
		}
	}
	if m.Status().RestartCount != len(want) {
		t.Fatalf("restart count = %d, want %d", m.Status().RestartCount, len(want))
	}
}

func TestHealthySessionResetsBackoff(t *testing.T) {
	clock := newTestClock()
	var (
		mu     sync.Mutex
		delays []time.Duration
	)
	done := make(chan struct{})
	launcher := func(context.Context) (*Child, error) {
		fake := newFakeBridge(t, func(f *fakeBridge) {
			f.hello()
			req := f.nextRequestOf(MethodRunsSnapshot)
			f.respond(req["id"].(string), emptySnapshot())
			// The clock only moves once the daemon has already recorded the
			// connection, so every session is unambiguously "healthy".
			clock.advance(HealthyResetInterval + time.Second)
			f.exit()
		})
		return fake.child(), nil
	}
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: launcher, Now: clock.Now,
		Sleep: func(_ context.Context, d time.Duration) bool {
			mu.Lock()
			delays = append(delays, d)
			full := len(delays) == 3
			mu.Unlock()
			if full {
				close(done)
				return false
			}
			return true
		},
	})
	<-done
	m.Close()

	mu.Lock()
	got := append([]time.Duration(nil), delays...)
	mu.Unlock()
	for i, d := range got {
		if d != time.Second {
			t.Fatalf("delay %d = %s, want the schedule reset to 1s (all: %v)", i, d, got)
		}
	}
}

func TestDisconnectServesStaleDataThenClearsIt(t *testing.T) {
	clock := newTestClock()
	fake := newFakeBridge(t, serveSnapshots)
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(), Now: clock.Now,
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	fake.exit()
	waitFor(t, "disconnect", func() bool { return !m.Status().Available })

	// Mutations are disabled at once, but the last projection survives as
	// visibly stale read-only data.
	if _, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}); err == nil {
		t.Fatal("a disconnected bridge must refuse mutations")
	}
	_, meta, err := m.Snapshot()
	if err != nil {
		t.Fatalf("stale snapshot error = %v, want the stale window to serve it", err)
	}
	if !meta.Stale {
		t.Fatal("a snapshot served after disconnect must be marked stale")
	}
	if !m.Status().SnapshotStale {
		t.Fatal("status must mark the retained snapshot stale")
	}

	clock.advance(StaleWindow + time.Second)
	if _, _, err := m.Snapshot(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired snapshot error = %v, want ErrUnavailable", err)
	}
	if m.Status().SnapshotAgeSeconds != nil {
		t.Fatal("an expired snapshot must not report an age")
	}
}

func TestActionsRequireAFreshSnapshotAfterReconnect(t *testing.T) {
	// The bridge answers the handshake but never the snapshot, so the daemon is
	// connected without current state: actions must stay disabled.
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitFor(t, "connection", func() bool { return m.Status().Available })

	if m.Status().MutationsEnabled {
		t.Fatal("a handshake alone must not re-enable mutations")
	}
	if _, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}); !errors.Is(err, ErrMutationsDisabled) {
		t.Fatalf("Act error = %v, want ErrMutationsDisabled", err)
	}
}

func TestActionIsSentExactlyOnceOnRefusal(t *testing.T) {
	var aborts int
	var mu sync.Mutex
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		for {
			select {
			case req := <-f.requests:
				switch req["method"] {
				case MethodRunsSnapshot:
					f.respond(req["id"].(string), emptySnapshot())
				case MethodRunAbort:
					mu.Lock()
					aborts++
					mu.Unlock()
					f.refuse(req["id"].(string), Error{Code: "stale-action-token", Message: "token for plan /home/op/x is stale"})
				}
			case <-f.exited:
				return
			}
		}
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	_, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", RunID: "r", OperationID: "op", ActionToken: "tok",
	})
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Code != "stale-action-token" {
		t.Fatalf("Act error = %v, want a typed stale-action-token refusal", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if aborts != 1 {
		t.Fatalf("run.abort was sent %d times; a mutation must never be retried", aborts)
	}
}

func TestActionValidationRejectsIncompleteRequests(t *testing.T) {
	m := New(Options{Mode: ModeAuto, Command: "asl", Logger: discardLogger()})
	cases := map[string]ActionRequest{
		"missing plan_dir":     {Method: MethodRunAbort, OperationID: "op", ActionToken: "tok"},
		"missing operation_id": {Method: MethodRunAbort, PlanDir: "/p", ActionToken: "tok"},
		"missing action_token": {Method: MethodRunAbort, PlanDir: "/p", OperationID: "op"},
		"missing reply text":   {Method: MethodQuestionReply, PlanDir: "/p", OperationID: "op", ActionToken: "tok"},
		"missing resolution":   {Method: MethodQuestionResolve, PlanDir: "/p", OperationID: "op", ActionToken: "tok"},
		"unknown method":       {Method: "run.delete", PlanDir: "/p", OperationID: "op", ActionToken: "tok"},
	}
	for name, req := range cases {
		if _, err := m.Act(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: error = %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestOperationEventsRecordBoundedBodyFreeReceipts(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		for i := 0; i < maxReceipts+10; i++ {
			f.emit(uint64(i+1), EventOperationUpdated, Operation{
				OperationID: string(rune('a'+i%26)) + string(rune('0'+i/26)),
				Method:      MethodQuestionReply,
				Status:      OperationConsumed,
			})
		}
		<-f.exited
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	waitFor(t, "receipt window to fill", func() bool { return len(m.Receipts()) == maxReceipts })
	for _, op := range m.Receipts() {
		if op.Status != OperationConsumed {
			t.Fatalf("receipt = %+v", op)
		}
	}
}

func TestRunsChangedTriggersRefresh(t *testing.T) {
	digests := make(chan string, 4)
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		generation := 0
		for {
			select {
			case req := <-f.requests:
				if req["method"] != MethodRunsSnapshot {
					continue
				}
				generation++
				snapshot := emptySnapshot()
				snapshot["digest"] = "d" + string(rune('0'+generation))
				f.respond(req["id"].(string), snapshot)
				digests <- snapshot["digest"].(string)
			case <-f.exited:
				return
			}
		}
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	<-digests
	fake.emit(1, EventRunsChanged, map[string]any{"digest": "d2", "row_count": 0})
	<-digests

	waitFor(t, "second snapshot", func() bool {
		_, meta, err := m.Snapshot()
		return err == nil && meta.Generation >= 2
	})
}

func TestQuestionBodyIsFetchedAndNeverRetained(t *testing.T) {
	const body = "Should I delete the production bucket?"
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		for {
			select {
			case req := <-f.requests:
				switch req["method"] {
				case MethodRunsSnapshot:
					f.respond(req["id"].(string), emptySnapshot())
				case MethodQuestionGet:
					f.respond(req["id"].(string), map[string]any{
						"plan_dir": "/p", "question_id": "q1", "body": body, "body_bytes": len(body),
						"action_token": "tok",
					})
				}
			case <-f.exited:
				return
			}
		}
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	got, err := m.QuestionBody(context.Background(), QuestionRequest{PlanDir: "/p", QuestionID: "q1"})
	if err != nil {
		t.Fatalf("QuestionBody: %v", err)
	}
	if got.Body.Reveal() != body {
		t.Fatalf("body = %q", got.Body.Reveal())
	}
	if len(m.Receipts()) != 0 {
		t.Fatal("fetching a question body must not create a receipt")
	}
	for method := range m.Metrics().RequestsByMethod {
		switch method {
		case MethodRunsSnapshot, MethodQuestionGet:
		default:
			t.Fatalf("metrics recorded an unexpected method key %q", method)
		}
	}
}

func TestWorkbenchRejectsUnknownFocus(t *testing.T) {
	m := New(Options{Mode: ModeAuto, Command: "asl", Logger: discardLogger()})
	if _, err := m.Workbench(context.Background(), WorkbenchRequest{PlanDir: "/p", Focus: "everything"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Workbench error = %v, want ErrInvalidRequest", err)
	}
}

func TestCloseJoinsSupervisionAndIsIdempotent(t *testing.T) {
	fake := newFakeBridge(t, serveSnapshots)
	m := New(Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(), Logger: discardLogger()})
	m.Start(context.Background())
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	done := make(chan struct{})
	go func() {
		m.Close()
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join bridge goroutines")
	}
	if m.Status().Available {
		t.Fatal("a closed manager must not report availability")
	}
}

func TestBackoffForSaturates(t *testing.T) {
	if got := backoffFor(-1); got != time.Second {
		t.Fatalf("backoffFor(-1) = %s", got)
	}
	if got := backoffFor(len(restartBackoff) + 5); got != 30*time.Second {
		t.Fatalf("saturated backoff = %s, want 30s", got)
	}
}
