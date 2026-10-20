package aslbridge

// The gate between "a child is connected" and "the daemon may act on what it
// sent": an exact contract set, a snapshot, and a handshake grant pinned to that
// snapshot's digest. Every way that gate can close is exercised here, because
// each one has to leave reads working and mutations off rather than the reverse.

import (
	"context"
	"errors"
	"github.com/sglavoie/agentmux/internal/testutil"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The hello is not the last word on identity. A bridge that reloaded its corpus
// without restarting would still be inside a session whose hello matched, so the
// snapshot restates the set it was built under and the daemon checks it again.
func TestASnapshotNamingAForeignContractSetEndsTheSession(t *testing.T) {
	var starts atomic.Int32
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			if reqMethod(req) != MethodRunsSnapshot {
				return
			}
			snapshot := emptySnapshot()
			snapshot["contract_set_id"] = "asl-amx-1.ffffffffffffffff"
			f.respond(reqID(req), snapshot)
		})
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
	if m.Status().MutationsEnabled {
		t.Error("a foreign snapshot left mutations enabled")
	}
	if _, _, err := m.Snapshot(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Snapshot() error = %v; a refused projection must never be stored", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := starts.Load(); got != 1 {
		t.Errorf("the daemon started %d children for a terminal mismatch, want 1", got)
	}
}

// The bridge gets the same veto from its own side. Its refusal code is the
// mismatch, so it is terminal for the same reason the hello check is, and the
// published reason still names only this build's identity.
func TestAHandshakeRefusedAsIncompatibleEndsTheSession(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			switch reqMethod(req) {
			case MethodRunsSnapshot:
				f.respond(reqID(req), emptySnapshot())
			case MethodSessionHandshake:
				f.refuse(reqID(req), Error{
					Code:    CodeContractSetMismatch,
					Message: "agent mux offered a set /home/op/plans/alpha was not built for",
				})
			}
		})
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})

	waitForReason(t, m, ContractSetID)
	if reason := m.Status().Reason; strings.Contains(reason, "plans/alpha") {
		t.Errorf("reason %q quoted the bridge's own message", reason)
	}
	if m.Status().MutationsEnabled {
		t.Error("a refused handshake left mutations enabled")
	}
}

// A handshake the bridge answers against an older projection than the one the
// daemon just stored is not a failure: the corpus moved between the two calls.
// It costs the grant and nothing else, so the operator keeps seeing runs and
// simply cannot act on them until a refresh catches up.
func TestAHandshakeAgainstAnOlderDigestIsReadOnlyNotFatal(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			switch reqMethod(req) {
			case MethodRunsSnapshot:
				f.respond(reqID(req), snapshotWithDigest("d-current"))
			case MethodSessionHandshake:
				// The bridge still holds the projection before this one.
				f.respond(reqID(req), grantFor("d-previous"))
			}
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	testutil.Eventually(t, "the stale-handshake reason", func() bool {
		return m.Status().Reason == reasonHandshakeStale
	})
	status := m.Status()
	if !status.Available {
		t.Error("a moved digest marked the whole integration unavailable")
	}
	if status.MutationsEnabled {
		t.Error("a grant against another digest enabled mutations")
	}
	snapshot, meta, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v; reads survive a withdrawn grant", err)
	}
	if snapshot.Digest != "d-current" || !meta.Stale {
		t.Errorf("snapshot = %+v, meta = %+v; want the current projection marked stale", snapshot, meta)
	}
	if _, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", RunID: "r", OperationID: "op", ActionToken: "tok",
	}); !errors.Is(err, ErrMutationsDisabled) {
		t.Errorf("Act error = %v, want ErrMutationsDisabled", err)
	}
}

// Same withdrawal, said outright: the bridge matched on identity and digest and
// still declined to authorize. Nothing about that is retryable by force, so the
// daemon goes read-only and asks again rather than acting anyway.
func TestAWithheldGrantLeavesTheDaemonReadOnly(t *testing.T) {
	var granting atomic.Bool
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			switch reqMethod(req) {
			case MethodRunsSnapshot:
				f.respond(reqID(req), emptySnapshot())
			case MethodSessionHandshake:
				if granting.Load() {
					f.answerHandshake(req)
					return
				}
				params, _ := req["params"].(map[string]any)
				digest, _ := params["snapshot_digest"].(string)
				f.respond(reqID(req), map[string]any{
					"contract_set_id":   ContractSetID,
					"handshake_id":      "hs-withheld",
					"mutations_enabled": false,
					"snapshot_digest":   digest,
				})
			}
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	testutil.Eventually(t, "the withheld grant", func() bool {
		status := m.Status()
		return status.Available && status.Reason == reasonHandshakeStale
	})
	if m.Status().MutationsEnabled {
		t.Fatal("a bridge that withheld the grant still enabled mutations")
	}
	// The daemon keeps asking, so the grant is re-earned without a restart.
	granting.Store(true)
	testutil.Eventually(t, "the grant to be re-earned", func() bool { return m.Status().MutationsEnabled })
	if restarts := m.Status().RestartCount; restarts != 0 {
		t.Errorf("restart count = %d; a withheld grant is not a child failure", restarts)
	}
}

// A receipt the daemon cannot decode is the one outcome that is neither success
// nor refusal: ASL answered, and what it applied is unknown. The mutation is
// never retried, so the only safe response is to stop acting until a fresh
// snapshot and handshake re-establish what is true.
func TestAnUndecodableReceiptDisablesMutationsAsAmbiguous(t *testing.T) {
	fake := scriptedBridge(t, func(f *fakeBridge, req map[string]any) {
		// Well-formed protocol, unreadable payload: ok, but not an operation.
		f.respond(reqID(req), "applied")
	})
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: fake.launcher(),
		Sleep: func(context.Context, time.Duration) bool { return false },
	})
	testutil.Eventually(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	_, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", RunID: "r", OperationID: "op-ambiguous", ActionToken: "tok",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Act error = %v, want ErrUnavailable", err)
	}
	var refusal *Error
	if errors.As(err, &refusal) {
		t.Errorf("an ambiguous outcome was reported as refusal %q", refusal.Code)
	}
	if m.Status().MutationsEnabled {
		t.Error("an ambiguous operation left mutations enabled")
	}
	if len(m.Receipts()) != 0 {
		t.Error("an ambiguous operation left a receipt implying it settled")
	}
}

// storeSnapshot is what enforces "a grant is against one projection". A refresh
// that lands new rows must withdraw the previous grant on its own, before any
// handshake answers, or the window between the two calls would be mutable
// against rows the operator never saw.
func TestANewProjectionWithdrawsThePreviousGrant(t *testing.T) {
	// Unstarted: this is the state machine alone, with no child to supervise.
	m := New(Options{Mode: ModeAuto, Command: "asl", Logger: discardLogger()})

	m.mu.Lock()
	m.connected = true
	m.handshakeGen = 1
	m.mu.Unlock()

	m.storeSnapshot(Snapshot{ContractSetID: ContractSetID, Digest: "d1"})
	m.grantMutations("d1", "hs-1")
	if !m.mutationsEnabled() {
		t.Fatal("a handshaked snapshot did not enable mutations")
	}

	m.storeSnapshot(Snapshot{ContractSetID: ContractSetID, Digest: "d2"})
	if m.mutationsEnabled() {
		t.Error("new rows arrived under the grant earned for the previous ones")
	}

	m.grantMutations("d2", "hs-2")
	if !m.mutationsEnabled() {
		t.Error("the handshake for the current digest did not restore mutations")
	}

	// A reconnect starts over: a grant from the previous process authorizes
	// nothing about the projection this one built.
	m.setConnected("/fake/asl", SourceConfigured, Hello{Protocol: ProtocolVersion, ContractSetID: ContractSetID}, nil)
	if m.mutationsEnabled() {
		t.Error("a grant survived the child that issued it")
	}
}
