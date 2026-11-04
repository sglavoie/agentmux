package probes

import (
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Binding is the immutable routing and freshness evidence shared by the periodic
// screen/transcript adapters. Producer identifies the harness session whose
// external evidence is being read; SourceWindowID is its runtime slot, while
// TargetWindowID owns the reducer state that observations mutate. Both receipts
// are captured before reading the registered tmux pane. Most bindings require
// a live producer route; TerminalProducer and RecoverySource narrowly authorize
// Codex clear and Copilot self-recovery after the live route has closed.
//
// The authorization fields are exported because the daemon root's harness
// adapters are the only things that may grant them: the root decides that a
// terminal fact still buys one last read or that a stop recovery applies.
// Binding itself only records and validates that decision.
type Binding struct {
	Producer             conversation.Identity
	SourceWindowID       int
	TargetWindowID       int
	SourceReceipt        uint64
	TargetReceipt        uint64
	StoppedCodexRecovery bool
	TerminalProducer     bool
	TerminalAt           int64
	RecoverySource       protocol.SourceID
	RecoveryAt           int64
	// RuntimeInstance and RuntimePane name the registered tmux pane and server
	// generation whose screen is permitted to affect this slot.
	RuntimeInstance runtimesession.BackendInstance
	RuntimePane     backend.Ref
}

func (b Binding) SourceSlot() int {
	return b.SourceWindowID
}

func (b Binding) TargetSlot() int {
	return b.TargetWindowID
}

// OnRuntimeBackend reports a binding with a registered pane.
func (b Binding) OnRuntimeBackend() bool { return !b.RuntimePane.IsZero() }

// runtimeSlotPanePrefix marks the reference a runtime binding's pane is read
// under. tmux pane IDs start with a percent sign, so this prefix cannot
// collide with one.
const runtimeSlotPanePrefix = "slot:"

// RuntimeSlotPane is how a per-tick screen cache keys the pane behind a
// daemon slot. Pane IDs repeat across servers, so the slot is what is unique;
// whoever reads the screen maps it back to the pane (RuntimeSlotOfPane).
func RuntimeSlotPane(slot int) backend.Ref {
	return backend.PaneID(runtimeSlotPanePrefix + strconv.Itoa(slot))
}

// RuntimeSlotOfPane reports the slot a RuntimeSlotPane reference names.
func RuntimeSlotOfPane(pane backend.Ref) (int, bool) {
	raw, ok := strings.CutPrefix(pane.ID, runtimeSlotPanePrefix)
	if !ok || pane.Scope != backend.ScopePane {
		return 0, false
	}
	slot, err := strconv.Atoi(raw)
	if err != nil || slot <= 0 || strconv.Itoa(slot) != raw {
		return 0, false
	}
	return slot, true
}

// SourcePane names the producer pane as a backend reference, which is how the
// screen readers address it through the runtime screen router.
func (b Binding) SourcePane() backend.Ref {
	return RuntimeSlotPane(b.SourceWindowID)
}

// ScreenExpectation is what a screen read for this binding must describe: the
// registered producer pane on the exact backend instance generation.
func (b Binding) ScreenExpectation() ScreenExpectation {
	return ScreenExpectation{Instance: b.RuntimeInstance, Pane: b.RuntimePane}
}

// Valid reports whether the binding is internally coherent. It is the
// route-independent half of validation; the caller still revalidates freshness
// and the reducer route before acting on an observation.
func (b Binding) Valid() bool {
	if b.SourceWindowID <= 0 || b.TargetWindowID <= 0 ||
		(!b.Producer.Valid() && (!b.StoppedCodexRecovery || harness.NormalizeKnown(b.Producer.Harness) != harness.Codex)) {
		return false
	}
	if b.TerminalProducer && b.RecoverySource.Kind != "" {
		return false
	}
	return b.RecoverySource.Kind == "" || b.RecoverySource.Kind == protocol.SourceProbe
}
