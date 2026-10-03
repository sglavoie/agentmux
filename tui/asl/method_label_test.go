package asl

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// TestEveryRowActionHasAnOperatorLabel keeps a row action the bridge can offer
// from reaching the Runs view as a raw method name. The row-action vocabulary
// is the mutation methods plus workbench.ensure.
func TestEveryRowActionHasAnOperatorLabel(t *testing.T) {
	actions := append(append([]string(nil), aslbridge.MutationMethods...), aslbridge.MethodWorkbenchEnsure)
	for _, method := range actions {
		if label := MethodLabel(method); label == method {
			t.Errorf("MethodLabel(%q) falls back to the raw method name", method)
		}
	}
}
