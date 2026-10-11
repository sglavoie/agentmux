package daemon

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// countingListRunner answers every `kitten @ ls` with one payload and counts
// them.
type countingListRunner struct {
	lists   atomic.Int64
	payload []byte
}

func (r *countingListRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	if _, isList := parseKittenList(args); !isList {
		return []byte("{}"), nil, nil
	}
	r.lists.Add(1)
	return r.payload, nil, nil
}

// A daemon restarted from a terminal outside Kitty has no socket, and with no
// controlling terminal either, a `kitten @ ls` can only fail. It must come up
// ready without asking, and still restore durable state from the first Kitty
// a hook introduces.
func TestDaemonWithNoKittySocketIsReadyAndRestoresFromTheFirstKitty(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: now.Unix()})
	encoded, err := state.WindowState{LastAction: "active", Reducer: r, Effective: r.Reduce(now.Unix()), ShadowSeq: 1}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	runner := &countingListRunner{payload: mustMarshalWindows(t, []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:    100,
			Title: "api",
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}})}
	h.useKittyClient(kitty.NewClientWithSocket(runner, ""))

	if h.kittyKnown() {
		t.Fatal("a client with no socket was taken for a Kitty the daemon can list")
	}
	h.MarkReadyWithoutKitty()
	h.maintenanceTick(now)
	if got := runner.lists.Load(); got != 0 {
		t.Fatalf("the daemon listed windows %d times with no Kitty socket; want none", got)
	}
	if ready, reason := h.Readiness(); !ready || reason != "" {
		t.Fatalf("readiness = (%v, %q); want ready with no Kitty to read", ready, reason)
	}

	// A hook from a Kitty window brings the socket.
	h.stateMu.Lock()
	h.rememberKittySocketLocked(42, coordinationSocket)
	h.stateMu.Unlock()
	h.maintenanceTick(now)

	h.stateMu.Lock()
	restored, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok || restored.Reducer == nil {
		t.Fatal("the first Kitty snapshot did not restore the window's durable state")
	}
	if h.kittyRestorePending.Load() {
		t.Fatal("the restore is still owed after a successful snapshot")
	}
}
