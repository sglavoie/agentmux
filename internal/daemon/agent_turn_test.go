package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestStateQuerySeparatesAgentCompletionAndASLAfterRestore(t *testing.T) {
	now := time.Now().Unix()
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:parent"}, SessionID: "parent", Action: "stop", Seq: 1, At: now - 42})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run"}, RunID: "run", Phase: state.ASLPhaseBetween, Seq: 1, At: now - 10})
	encoded, err := (state.WindowState{Reducer: r, Effective: r.Reduce(now)}).MarshalUserVar()
	if err != nil {
		t.Fatal(err)
	}
	var restored state.WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatal(err)
	}
	h := newTestHandler()
	h.store[8] = &restored
	got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[8]
	if got.AgentTurn == nil || got.AgentTurn.IconState != "stop" || got.AgentTurn.FinishedAt != now-42 {
		t.Fatalf("restored agent turn = %+v", got.AgentTurn)
	}
	if !got.Looping || got.IconState == "stop" || len(got.ASLPhases) != 1 || got.ASLPhases[0] != "between" {
		t.Fatalf("aggregate/run state changed: %+v", got)
	}
	got.AgentTurn.IconState = "error"
	got.ASLPhases[0] = "turn"
	again := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[8]
	if again.AgentTurn.IconState != "stop" || again.ASLPhases[0] != "between" {
		t.Fatal("query exposed mutable daemon state")
	}
}

func TestLegacyStateQueryOmitsIndependentStatus(t *testing.T) {
	h := newTestHandler()
	h.store[8] = &state.WindowState{}
	data, err := json.Marshal(h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[8])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "agent_turn") || strings.Contains(string(data), "asl_phases") {
		t.Fatalf("legacy snapshot invented independent status: %s", data)
	}
}
