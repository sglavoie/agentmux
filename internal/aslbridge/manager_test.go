package aslbridge

// Mode selection and the handshake: what a bridge child has to prove before the
// daemon will publish availability, and every protocol violation that ends a
// session instead of being tolerated. Restart and staleness behaviour lives in
// manager_supervision_test.go; the request surface in manager_calls_test.go.

import (
	"context"
	"errors"
	"fmt"
	"github.com/sglavoie/agentmux/internal/testutil"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// waitForReason blocks until the published capability block reports a failure
// mentioning want. Supervision is asynchronous, so a test asserts on the
// observed reason rather than on having closed the manager.
func waitForReason(t *testing.T, m *Manager, want string) {
	t.Helper()
	testutil.Eventually(t, "reason containing "+want, func() bool {
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

// Between Start and the first child's verdict the block says the bridge is
// starting rather than carrying no reason at all, which doctor would otherwise
// have to report as an unexplained failure; the handshake clears it.
func TestStartPublishesStartingUntilTheFirstHandshake(t *testing.T) {
	release := make(chan struct{})
	fake := newFakeBridge(t, serveSnapshots)
	launch := fake.launcher()
	m := startManager(t, Options{Mode: ModeRequired, Command: "asl", Launcher: func(ctx context.Context) (*Child, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return launch(ctx)
	}})

	if status := m.Status(); status.Available || status.Reason != ReasonStarting {
		t.Fatalf("status before the first handshake = %+v; want reason %q", status, ReasonStarting)
	}
	close(release)
	testutil.Eventually(t, "available", func() bool { return m.Status().Available })
	if reason := m.Status().Reason; reason == ReasonStarting {
		t.Fatalf("reason after the handshake = %q; want the starting reason cleared", reason)
	}
}

func TestHandshakePublishesAvailabilityAndMutations(t *testing.T) {
	fake := newFakeBridge(t, serveSnapshots)
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	testutil.Eventually(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

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

// Protocol 1 is retired. There is no parser left for it, so a bridge still
// serving it fails at the frame version rather than being read as far as its
// contract-set ID.
func TestHelloProtocolMismatchIsChildFailure(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.writeFrame(map[string]any{
			"type": "hello", "protocol": 1, "asl_version": "9.9.9",
			"contract_set_id": ContractSetID,
			"capabilities":    []string{MethodSessionHandshake, MethodRunsSnapshot},
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
		f.hello(MethodSessionHandshake, MethodQuestionGet)
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "runs.snapshot")
}

// A bridge that cannot handshake can never grant mutations, so the session is
// ended at the hello rather than published as a permanently read-only one.
func TestHelloWithoutHandshakeCapabilityIsChildFailure(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello(MethodRunsSnapshot, MethodQuestionGet)
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, MethodSessionHandshake)
}

// A foreign contract set is terminal, not transient: the same pairing
// reproduces it forever, so supervision stops rather than backing off, and the
// published reason names only this build's own ID.
func TestAForeignContractSetEndsSupervision(t *testing.T) {
	var starts atomic.Int32
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.helloAs("asl-amx-1.ffffffffffffffff")
		<-f.exited
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl",
		Launcher: func(ctx context.Context) (*Child, error) {
			starts.Add(1)
			return fake.launcher()(ctx)
		},
		Sleep: func(context.Context, time.Duration) bool { return false },
	})

	waitForReason(t, m, ContractSetID)
	if got := m.Status().Reason; strings.Contains(got, "ffffffffffffffff") {
		t.Errorf("reason %q quotes the identity the peer offered", got)
	}
	// Give a restart every chance to happen before asserting none did.
	time.Sleep(50 * time.Millisecond)
	if got := starts.Load(); got != 1 {
		t.Errorf("the daemon started %d children for a terminal mismatch, want 1", got)
	}
	if m.Status().MutationsEnabled {
		t.Error("a foreign contract set left mutations enabled")
	}
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

// A child that dies before its hello still names the executable it was
// started from, so doctor can tell a crashing bridge from a missing one and
// stop recommending a re-resolution that would persist the same path.
func TestAChildThatFailsItsHandshakeStillReportsItsExecutable(t *testing.T) {
	launcher := func(context.Context) (*Child, error) {
		fake := newFakeBridge(t, func(f *fakeBridge) {
			f.writeLine("not a hello")
			<-f.exited
		})
		child := fake.child()
		child.Path = "/opt/asl/bin/asl"
		child.Source = SourceConfigured
		return child, nil
	}
	m := startManager(t, Options{
		Mode: ModeRequired, Command: "/opt/asl/bin/asl", Launcher: launcher,
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	waitForReason(t, m, "handshake failed")

	status := m.Status()
	if status.Available {
		t.Fatal("a child that never said hello is not available")
	}
	if status.Executable != "/opt/asl/bin/asl" || status.ExecutableSource != SourceConfigured {
		t.Fatalf("executable = %q (%s), want the launched path", status.Executable, status.ExecutableSource)
	}
}

func TestWorkbenchRejectsUnknownFocus(t *testing.T) {
	m := New(Options{Mode: ModeAuto, Command: "asl", Logger: discardLogger()})
	if _, err := m.Workbench(context.Background(), WorkbenchRequest{PlanDir: "/p", Focus: "everything"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Workbench error = %v, want ErrInvalidRequest", err)
	}
}
