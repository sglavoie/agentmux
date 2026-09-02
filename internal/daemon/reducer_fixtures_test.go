package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// This file holds the reducer fixtures the daemon's own tests share with the
// state package's tests. The state package owns the reducer, so its test files
// moved there with it; these are the same fixtures re-declared for the handler
// tests that still drive a reducer through the daemon.

// zeroCfg disables staleness and the legacy grace so precedence/ordering tests
// isolate the property under test.
var zeroCfg = state.ReducerConfig{}

func harnessSource() protocol.SourceID {
	return protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"}
}

func aslSource(k string) protocol.SourceID {
	return protocol.SourceID{Kind: protocol.SourceASL, Key: k}
}

// factOf returns the reducer's snapshot of one source, or nil when the reducer
// has never seen it. The pointer keeps the "no fact at all" case distinct from a
// withdrawn one, so assertions read the same way they did against the reducer's
// own fact map before it became package-private to state.
func factOf(r *state.Reducer, source protocol.SourceID) *protocol.SourceFactSnapshot {
	fact, ok := r.Fact(source)
	if !ok {
		return nil
	}
	return &fact
}

func lastTrace(t *testing.T, r *state.Reducer) state.ReducerTrace {
	t.Helper()
	traces := r.Traces()
	if len(traces) == 0 {
		t.Fatal("no traces recorded")
	}
	return traces[len(traces)-1]
}

func processReducerEvent(
	source protocol.SourceID,
	action string,
	seq uint64,
	at int64,
	sessionID string,
	pipeMode bool,
	identity protocol.HarnessProcessIdentity,
) protocol.ReducerEvent {
	return protocol.ReducerEvent{
		Source:         source,
		Action:         action,
		Seq:            seq,
		At:             at,
		SessionID:      sessionID,
		PipeMode:       pipeMode,
		HarnessProcess: identity,
	}
}
