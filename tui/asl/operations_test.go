package asl

import (
	"fmt"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// The root held this window on its Bubble Tea model, so the test that guarded
// the bound used to drive it through the model's tracking method. The window
// itself is now this package's, so it is driven directly; what it asserts —
// that the window stops growing and evicts oldest-first — is unchanged.
func TestASLOperationHistoryStaysBounded(t *testing.T) {
	var history []OperationView
	for i := 0; i < OperationHistory+3; i++ {
		history = TrackOperation(history, OperationView{
			OperationID: fmt.Sprintf("op-%d", i),
			Method:      aslbridge.MethodQuestionReply,
			State:       OpSubmitting,
		})
	}
	if len(history) != OperationHistory {
		t.Fatalf("tracked %d operations, want %d", len(history), OperationHistory)
	}
	if history[0].OperationID != "op-3" {
		t.Fatalf("oldest retained operation = %q, want op-3", history[0].OperationID)
	}
}
