package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestStatusEvidenceFollowsWinningFactAndSurvivesRestore(t *testing.T) {
	for _, tc := range []struct {
		source protocol.SourceID
		want   string
	}{
		{protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:private-session"}, "harness"},
		{protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource}, "screen"},
		{protocol.SourceID{Kind: protocol.SourceProbe, Key: copilotTranscriptStopProbeSource}, "transcript"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			r := state.NewReducer(state.ReducerConfig{})
			r.Apply(protocol.ReducerEvent{Source: tc.source, Action: "permission", Seq: 1, At: 100})
			st := state.WindowState{Reducer: r, Effective: r.Reduce(110)}
			encoded, err := st.MarshalUserVar()
			if err != nil {
				t.Fatal(err)
			}
			var restored state.WindowState
			if err := restored.UnmarshalUserVar(encoded); err != nil {
				t.Fatal(err)
			}
			h := newTestHandler()
			h.store[7] = &restored
			got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[7].StatusEvidence
			if got == nil || got.Kind != tc.want || got.At != 100 {
				t.Fatalf("evidence = %+v", got)
			}
			got.Kind = "mutated"
			if statusEvidence(&restored).Kind != tc.want {
				t.Fatal("query mutated retained evidence")
			}
			restored.Effective.Icon = ""
			if statusEvidence(&restored) != nil {
				t.Fatal("idle borrowed old evidence")
			}
		})
	}
	if statusEvidence(&state.WindowState{}) != nil {
		t.Fatal("legacy state invented evidence")
	}
}
