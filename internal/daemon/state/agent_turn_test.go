package state

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestAgentTurnIsIndependentOfASL(t *testing.T) {
	for _, phase := range []string{ASLPhaseBetween, ASLPhaseWaiting, ASLPhaseTurn, ASLPhaseVerification} {
		t.Run(phase, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: "stop", Seq: 1, At: 100, TurnID: "first"})
			r.Apply(protocol.ReducerEvent{Source: aslSource("run"), RunID: "run", Phase: phase, Seq: 1, At: 110})
			before := r.Reduce(120)
			if before.Icon == "stop" || !before.Looping {
				t.Fatalf("aggregate no longer masks completion: %+v", before)
			}
			if got := r.AgentTurn(120); got.IconState != "stop" || got.FinishedAt != 100 {
				t.Fatalf("agent turn = %+v; want completed at 100", got)
			}
			if got := r.ASLPhases(); !reflect.DeepEqual(got, []string{phase}) {
				t.Fatalf("run phases = %v", got)
			}
			if after := r.Reduce(120); after != before {
				t.Fatalf("reading the turn changed aggregate state: %+v -> %+v", before, after)
			}
			r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: "active", Seq: 2, At: 130, TurnID: "second"})
			if got := r.AgentTurn(130); got.IconState != "bolt" || got.FinishedAt != 0 {
				t.Fatalf("next turn still looks completed: %+v", got)
			}
		})
	}
}

func TestAgentTurnDoesNotInventCompletionFromASL(t *testing.T) {
	r := NewReducer(ReducerConfig{StaleActive: 10})
	r.Apply(protocol.ReducerEvent{Source: aslSource("run"), Phase: ASLPhaseFinished, Seq: 1, At: 100})
	if got := r.AgentTurn(100); got.IconState != "" || got.FinishedAt != 0 {
		t.Fatalf("ASL completion became agent completion: %+v", got)
	}
	r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: "active", Seq: 1, At: 101})
	if got := r.AgentTurn(111); got.IconState != "" {
		t.Fatalf("stale activity remained visible: %+v", got)
	}
	r.Apply(protocol.ReducerEvent{Source: harnessSource(), Action: "stop_failure", Seq: 2, At: 112})
	if got := r.AgentTurn(112); got.IconState != "error" || got.FinishedAt != 112 {
		t.Fatalf("failure lost: %+v", got)
	}
	r.Apply(protocol.ReducerEvent{Source: protocol.UserActionSource(), Action: "user_clear", Seq: 1, At: 113})
	if got := r.AgentTurn(113); got.IconState != "" || got.FinishedAt != 0 {
		t.Fatalf("acknowledged outcome remained visible: %+v", got)
	}
}

func TestAgentTurnKeepsInteractiveCompletionWhilePipeWorkerRuns(t *testing.T) {
	r := NewReducer(zeroCfg)
	parent := protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:parent"}
	r.Apply(protocol.ReducerEvent{Source: parent, SessionID: "parent", Action: "stop", Seq: 1, At: 100, TurnID: "first"})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:worker"}, SessionID: "worker", PipeMode: true, Action: "active", Seq: 1, At: 110})
	r.Apply(protocol.ReducerEvent{Source: subagentSource("child"), SessionID: "worker", Action: "subagent", Seq: 1, At: 111})
	r.Apply(protocol.ReducerEvent{Source: aslSource("run"), Phase: ASLPhaseTurn, Seq: 1, At: 112})
	if got := r.AgentTurn(120); got.IconState != "stop" || got.FinishedAt != 100 {
		t.Fatalf("background worker hid interactive completion: %+v", got)
	}
	r.Apply(protocol.ReducerEvent{Source: parent, SessionID: "parent", Action: "active", Seq: 2, At: 130, TurnID: "second"})
	if got := r.AgentTurn(130); got.IconState != "bolt" || got.FinishedAt != 0 {
		t.Fatalf("next interactive turn did not replace completion: %+v", got)
	}
}

func TestASLPhasesAreDistinctAndExcludeFinishedRuns(t *testing.T) {
	r := NewReducer(zeroCfg)
	for _, key := range []string{"a", "b", "c"} {
		r.Apply(protocol.ReducerEvent{Source: aslSource(key), Phase: ASLPhasePaused, Seq: 1, At: 100})
	}
	r.Apply(protocol.ReducerEvent{Source: aslSource("b"), Phase: ASLPhaseTurn, Seq: 2, At: 101})
	r.Apply(protocol.ReducerEvent{Source: aslSource("c"), Phase: ASLPhaseFinished, Seq: 2, At: 102})
	if got := r.ASLPhases(); !reflect.DeepEqual(got, []string{"turn"}) {
		t.Fatalf("phases = %v", got)
	}
}
