package runtimesession

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
)

var testEpoch = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func testTarget(generation, ref string) Target {
	return Target{
		Instance: BackendInstance{Kind: BackendTmux, Address: "/tmp/amx-tmux.sock", Generation: generation},
		Ref:      ref,
	}
}

func testProcess(pid int, start string) ProcessFingerprint {
	return ProcessFingerprint{PID: pid, StartToken: start}
}

func testConversation(t *testing.T, sessionID string) *conversation.Identity {
	t.Helper()
	identity, ok := conversation.New("claude", sessionID)
	if !ok {
		t.Fatalf("conversation.New(claude, %q) failed", sessionID)
	}
	return &identity
}

func beginLive(t *testing.T, registry *Registry, launchID string, target Target, process ProcessFingerprint) Record {
	t.Helper()
	record, err := registry.Begin(BeginRequest{Project: "API", LaunchID: launchID, Relation: RelationFresh}, testEpoch)
	if err != nil {
		t.Fatalf("Begin(%s) = %v", launchID, err)
	}
	record, err = registry.Commit(record.ID, target, process, testEpoch.Add(time.Second))
	if err != nil {
		t.Fatalf("Commit(%s) = %v", launchID, err)
	}
	return record
}

func TestIDsAreOpaqueAndStrictlyParsed(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := ParseID(string(id)); err != nil || parsed != id {
		t.Fatalf("ParseID(%q) = %q, %v", id, parsed, err)
	}
	other, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if other == id {
		t.Fatal("two generated IDs were equal")
	}
	for _, bad := range []string{
		"",
		"12",                                   // a Kitty window ID is not a runtime ID
		"%3",                                   // nor is a tmux pane ID
		"rs-" + strings.Repeat("A", 32),        // not canonical lowercase
		"rs-" + strings.Repeat("a", 31),        // short
		"rt-" + strings.Repeat("a", 32),        // wrong prefix
		"rs-" + strings.Repeat("a", 31) + "g",  // not hex
		" rs-" + strings.Repeat("a", 32),       // untrimmed
		"rs-" + strings.Repeat("a", 32) + "\n", // trailing newline from a shell
	} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) accepted a malformed ID", bad)
		}
	}
}

// The four identities a runtime session carries change independently: one
// conversation can be relaunched into several runtime sessions over time, one
// project can run several, and attachment never changes the runtime ID.
func TestIdentitiesStaySeparate(t *testing.T) {
	registry := NewRegistry()
	conv := testConversation(t, "conv-1")
	first, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh, Conversation: conv}, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Commit(first.ID, testTarget("gen-1", "%1"), testProcess(100, "a"), testEpoch); err != nil {
		t.Fatal(err)
	}
	// The same project may run a second, unrelated conversation.
	if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-2", Relation: RelationFresh}, testEpoch); err != nil {
		t.Fatalf("second session for the same project = %v", err)
	}
	if _, err := registry.End(first.ID, testEpoch.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Resuming the conversation creates a new runtime identity linked to the old.
	resumed, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-3", Relation: RelationResume, Parent: first.ID, Conversation: conv}, testEpoch.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID == first.ID {
		t.Fatal("resume reused the previous runtime ID")
	}
	if resumed.Parent != first.ID || resumed.Relation != RelationResume {
		t.Fatalf("resume relationship = %s from %s", resumed.Relation, resumed.Parent)
	}
	if !resumed.Conversation.Equal(*conv) {
		t.Fatalf("resume conversation = %+v", resumed.Conversation)
	}
	live, err := registry.Commit(resumed.ID, testTarget("gen-1", "%2"), testProcess(200, "b"), testEpoch.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	attached, err := registry.Attach(live.ID, Attachment{ID: "view-1", Client: "desktop"}, testEpoch.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if attached.ID != live.ID || *attached.Target != *live.Target {
		t.Fatal("attaching changed runtime identity or target")
	}
}

func TestBeginRejectsMissingOrInconsistentRelationships(t *testing.T) {
	registry := NewRegistry()
	for _, tc := range []struct {
		name    string
		request BeginRequest
	}{
		{name: "no project", request: BeginRequest{LaunchID: "l", Relation: RelationFresh}},
		{name: "no launch", request: BeginRequest{Project: "API", Relation: RelationFresh}},
		{name: "unknown relation", request: BeginRequest{Project: "API", LaunchID: "l", Relation: "clone"}},
		{name: "resume without parent", request: BeginRequest{Project: "API", LaunchID: "l", Relation: RelationResume}},
		{name: "unregistered parent", request: BeginRequest{Project: "API", LaunchID: "l", Relation: RelationFork, Parent: ID("rs-" + strings.Repeat("0", 32))}},
		{name: "malformed explicit ID", request: BeginRequest{ID: "12", Project: "API", LaunchID: "l", Relation: RelationFresh}},
		{name: "incomplete conversation", request: BeginRequest{Project: "API", LaunchID: "l", Relation: RelationFresh, Conversation: &conversation.Identity{Harness: "claude"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := registry.Begin(tc.request, testEpoch); err == nil {
				t.Fatal("Begin accepted an invalid request")
			}
		})
	}
	if got := len(registry.List()); got != 0 {
		t.Fatalf("rejected requests left %d records", got)
	}
}

func TestGeneratedIDCollisionsRetryAndExplicitCollisionsFail(t *testing.T) {
	registry := NewRegistry()
	fixed := ID("rs-" + strings.Repeat("1", 32))
	fresh := ID("rs-" + strings.Repeat("2", 32))
	sequence := []ID{fixed, fixed, fresh}
	registry.newID = func() (ID, error) {
		next := sequence[0]
		sequence = sequence[1:]
		return next, nil
	}
	first, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "l1", Relation: RelationFresh}, testEpoch)
	if err != nil || first.ID != fixed {
		t.Fatalf("first Begin = %s, %v", first.ID, err)
	}
	second, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "l2", Relation: RelationFresh}, testEpoch)
	if err != nil || second.ID != fresh {
		t.Fatalf("Begin after a generated collision = %s, %v; want retry to %s", second.ID, err, fresh)
	}

	registry.newID = func() (ID, error) { return fixed, nil }
	if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "l3", Relation: RelationFresh}, testEpoch); !errors.Is(err, ErrIDCollision) {
		t.Fatalf("Begin with a stuck generator = %v, want ErrIDCollision", err)
	}
	// IDs are never reused, even after their session ended.
	if _, err := registry.End(first.ID, testEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Begin(BeginRequest{ID: fixed, Project: "API", LaunchID: "l4", Relation: RelationFresh}, testEpoch); !errors.Is(err, ErrIDCollision) {
		t.Fatalf("explicit reuse of an ended ID = %v, want ErrIDCollision", err)
	}
}

func TestDuplicateRegistrationIsRejected(t *testing.T) {
	registry := NewRegistry()
	live := beginLive(t, registry, "launch-1", testTarget("gen-1", "%1"), testProcess(100, "a"))

	t.Run("same launch", func(t *testing.T) {
		existing, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch)
		if !errors.Is(err, ErrDuplicateLaunch) {
			t.Fatalf("Begin(same launch) = %v, want ErrDuplicateLaunch", err)
		}
		if existing.ID != live.ID {
			t.Fatalf("duplicate launch returned %s, want the existing %s", existing.ID, live.ID)
		}
	})
	t.Run("same target", func(t *testing.T) {
		pending, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-2", Relation: RelationFresh}, testEpoch)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Commit(pending.ID, testTarget("gen-1", "%1"), testProcess(200, "b"), testEpoch); !errors.Is(err, ErrTargetInUse) {
			t.Fatalf("Commit(same target) = %v, want ErrTargetInUse", err)
		}
		if _, err := registry.Commit(pending.ID, testTarget("gen-1", "%2"), testProcess(100, "a"), testEpoch); !errors.Is(err, ErrProcessInUse) {
			t.Fatalf("Commit(same process) = %v, want ErrProcessInUse", err)
		}
		record, err := registry.Get(pending.ID)
		if err != nil || record.State != StatePending || record.Target != nil {
			t.Fatalf("rejected commit changed the record: %+v, %v", record, err)
		}
	})
	t.Run("double commit", func(t *testing.T) {
		if _, err := registry.Commit(live.ID, testTarget("gen-1", "%9"), testProcess(900, "z"), testEpoch); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("second Commit = %v, want ErrInvalidState", err)
		}
	})
	t.Run("same conversation", func(t *testing.T) {
		conv := testConversation(t, "conv-1")
		if _, err := registry.BindConversation(live.ID, *conv, testEpoch); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-3", Relation: RelationFork, Parent: live.ID, Conversation: conv}, testEpoch); !errors.Is(err, ErrConversationInUse) {
			t.Fatalf("Begin(conversation in use) = %v, want ErrConversationInUse", err)
		}
		// Harness names compare canonically: "Claude" is the same conversation.
		mixedCase := conversation.Identity{Harness: "Claude", SessionID: "conv-1"}
		if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-3", Relation: RelationFresh, Conversation: &mixedCase}, testEpoch); !errors.Is(err, ErrConversationInUse) {
			t.Fatalf("Begin(non-canonical duplicate) = %v, want ErrConversationInUse", err)
		}
		other := testConversation(t, "conv-2")
		if _, err := registry.BindConversation(live.ID, *other, testEpoch); !errors.Is(err, ErrConversationMismatch) {
			t.Fatalf("rebinding to another conversation = %v, want ErrConversationMismatch", err)
		}
		if _, err := registry.BindConversation(live.ID, *conv, testEpoch); err != nil {
			t.Fatalf("rebinding to the same conversation = %v", err)
		}
	})
	t.Run("identical session ID on another harness", func(t *testing.T) {
		codex, ok := conversation.New("codex", "conv-1")
		if !ok {
			t.Fatal("conversation.New(codex)")
		}
		if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-4", Relation: RelationFresh, Conversation: &codex}, testEpoch); err != nil {
			t.Fatalf("a codex conversation sharing a claude session ID = %v; harness is part of identity", err)
		}
	})
}

// A server restarted at the same address reuses pane IDs from %0. Neither
// resolution nor validation may let the new pane inherit the old registration.
func TestBackendGenerationChangeMakesTargetsStale(t *testing.T) {
	registry := NewRegistry()
	live := beginLive(t, registry, "launch-1", testTarget("gen-1", "%1"), testProcess(100, "a"))

	if _, ok := registry.Resolve(testTarget("gen-2", "%1"), testProcess(100, "a")); ok {
		t.Fatal("a pane in a new server generation resolved to the old registration")
	}
	restarted := testTarget("gen-2", "%1").Instance
	record, err := registry.Validate(live.ID, Observation{Instance: restarted, Present: true, Process: &ProcessFingerprint{PID: 100, StartToken: "a"}}, testEpoch.Add(time.Minute))
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("Validate(new generation) = %v, want ErrStaleTarget", err)
	}
	if record.State != StateStale || record.StaleReason != "backend generation changed" {
		t.Fatalf("record after generation change = %s (%s)", record.State, record.StaleReason)
	}
	// The stale session released the target: the new server's pane can be registered.
	beginLive(t, registry, "launch-2", testTarget("gen-2", "%1"), testProcess(300, "c"))
	if _, err := registry.Attach(live.ID, Attachment{ID: "v", Client: "desktop"}, testEpoch); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Attach(stale) = %v, want ErrInvalidState", err)
	}
}

func TestPIDReuseDoesNotReviveASession(t *testing.T) {
	registry := NewRegistry()
	target := testTarget("gen-1", "%1")
	live := beginLive(t, registry, "launch-1", target, testProcess(100, "Thu Oct  9 12:00:00 2026"))

	reused := testProcess(100, "Thu Oct  9 13:30:00 2026")
	if _, ok := registry.Resolve(target, reused); ok {
		t.Fatal("a reused PID resolved to the original registration")
	}
	if _, ok := registry.Resolve(target, testProcess(100, "Thu Oct  9 12:00:00 2026")); !ok {
		t.Fatal("the exact registered process did not resolve")
	}
	_, err := registry.Validate(live.ID, Observation{Instance: target.Instance, Present: true, Process: &reused}, testEpoch)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("Validate(reused PID) = %v, want ErrStaleTarget", err)
	}
	if _, ok := registry.Resolve(target, testProcess(100, "Thu Oct  9 12:00:00 2026")); ok {
		t.Fatal("a stale session still resolved")
	}
	// Stale is terminal: the same original observation does not revive it.
	original := testProcess(100, "Thu Oct  9 12:00:00 2026")
	if _, err := registry.Validate(live.ID, Observation{Instance: target.Instance, Present: true, Process: &original}, testEpoch); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Validate(stale) = %v, want ErrInvalidState", err)
	}
}

func TestValidateOutcomes(t *testing.T) {
	target := testTarget("gen-1", "%1")
	process := testProcess(100, "a")
	for _, tc := range []struct {
		name        string
		observation Observation
		wantErr     error
		wantState   State
		wantReason  string
	}{
		{name: "unchanged", observation: Observation{Instance: target.Instance, Present: true, Process: &process}, wantState: StateLive},
		{name: "target gone", observation: Observation{Instance: target.Instance}, wantErr: ErrStaleTarget, wantState: StateStale, wantReason: "target is gone"},
		{name: "no process", observation: Observation{Instance: target.Instance, Present: true}, wantErr: ErrStaleTarget, wantState: StateStale, wantReason: "no process at target"},
		{name: "other server", observation: Observation{Instance: BackendInstance{Kind: BackendTmux, Address: "/tmp/other.sock", Generation: "gen-1"}, Present: true, Process: &process}, wantErr: ErrWrongInstance, wantState: StateLive},
		{name: "other backend", observation: Observation{Instance: BackendInstance{Kind: BackendKitty, Address: target.Instance.Address, Generation: "gen-1"}, Present: true, Process: &process}, wantErr: ErrWrongInstance, wantState: StateLive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry()
			live := beginLive(t, registry, "launch", target, process)
			if _, err := registry.Attach(live.ID, Attachment{ID: "v", Client: "desktop"}, testEpoch); err != nil {
				t.Fatal(err)
			}
			record, err := registry.Validate(live.ID, tc.observation, testEpoch.Add(time.Minute))
			if tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate = %v, want %v", err, tc.wantErr)
			}
			if record.State != tc.wantState || record.StaleReason != tc.wantReason {
				t.Fatalf("record = %s (%q), want %s (%q)", record.State, record.StaleReason, tc.wantState, tc.wantReason)
			}
			if tc.wantState == StateStale && record.Attachment != nil {
				t.Fatal("a stale session kept its interactive attachment")
			}
		})
	}
}

func TestAttachmentTransferAndLateDetach(t *testing.T) {
	registry := NewRegistry()
	live := beginLive(t, registry, "launch-1", testTarget("gen-1", "%1"), testProcess(100, "a"))
	if _, err := registry.Attach(live.ID, Attachment{ID: "terminal-1", Client: "amx attach"}, testEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Attach(live.ID, Attachment{ID: "desktop-1", Client: "desktop"}, testEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Detach(live.ID, "terminal-1", testEpoch); !errors.Is(err, ErrAttachmentMismatch) {
		t.Fatalf("late Detach by the previous owner = %v, want ErrAttachmentMismatch", err)
	}
	record, err := registry.Detach(live.ID, "desktop-1", testEpoch)
	if err != nil || record.Attachment != nil || record.State != StateLive {
		t.Fatalf("owner Detach = %+v, %v; want a live, unattached session", record, err)
	}
	if _, err := registry.Attach(live.ID, Attachment{ID: "", Client: "desktop"}, testEpoch); err == nil {
		t.Fatal("Attach accepted an attachment without an ID")
	}
}

// A launcher that crashes after Begin leaves a pending record. It holds its
// launch until recovery abandons it; recovery never touches recent pending
// records that may still be mid-launch, nor live ones.
func TestInterruptedRegistrationRecovery(t *testing.T) {
	registry := NewRegistry()
	conv := testConversation(t, "conv-1")
	interrupted, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh, Conversation: conv}, testEpoch)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-2", Relation: RelationFresh}, testEpoch.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	live := beginLive(t, registry, "launch-3", testTarget("gen-1", "%1"), testProcess(100, "a"))

	if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-4", Relation: RelationFresh, Conversation: conv}, testEpoch); !errors.Is(err, ErrConversationInUse) {
		t.Fatalf("pending registration did not reserve its conversation: %v", err)
	}
	abandoned := registry.AbandonInterrupted(testEpoch.Add(30*time.Minute), testEpoch.Add(2*time.Hour))
	if len(abandoned) != 1 || abandoned[0] != interrupted.ID {
		t.Fatalf("abandoned = %v, want only %s", abandoned, interrupted.ID)
	}
	for id, want := range map[ID]State{interrupted.ID: StateAbandoned, recent.ID: StatePending, live.ID: StateLive} {
		record, err := registry.Get(id)
		if err != nil || record.State != want {
			t.Fatalf("%s state = %s, %v; want %s", id, record.State, err, want)
		}
	}
	if _, err := registry.Commit(interrupted.ID, testTarget("gen-1", "%5"), testProcess(500, "e"), testEpoch); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Commit after abandonment = %v, want ErrInvalidState", err)
	}
	// The abandoned launch's identity is free for a retry.
	if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh, Conversation: conv}, testEpoch.Add(3*time.Hour)); err != nil {
		t.Fatalf("retrying an abandoned launch = %v", err)
	}
}

func TestRecordsAreReturnedAsCopies(t *testing.T) {
	registry := NewRegistry()
	live := beginLive(t, registry, "launch-1", testTarget("gen-1", "%1"), testProcess(100, "a"))
	live.Target.Ref = "%99"
	live.Process.PID = 999
	record, err := registry.Get(live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Target.Ref != "%1" || record.Process.PID != 100 {
		t.Fatal("mutating a returned record changed the registry")
	}
}

func TestUpdatedAtNeverMovesBackwards(t *testing.T) {
	registry := NewRegistry()
	live := beginLive(t, registry, "launch-1", testTarget("gen-1", "%1"), testProcess(100, "a"))
	record, err := registry.End(live.ID, testEpoch.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if record.UpdatedAt.Before(live.UpdatedAt) {
		t.Fatalf("UpdatedAt moved from %s back to %s", live.UpdatedAt, record.UpdatedAt)
	}
}
