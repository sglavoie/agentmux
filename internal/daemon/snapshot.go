package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/harness"
)

// screenProbeBindingCurrentLocked revalidates every causal component of a
// binding. Callers hold stateMu and keep it held through any ensuing mutation.
func (h *Handler) screenProbeBindingCurrentLocked(binding probes.Binding) bool {
	if !binding.OnRuntimeBackend() {
		return false
	}
	if !binding.Valid() ||
		!h.windowReceiptCurrentLocked(binding.SourceSlot(), binding.SourceReceipt) ||
		!h.windowReceiptCurrentLocked(binding.TargetSlot(), binding.TargetReceipt) {
		return false
	}
	if binding.StoppedCodexRecovery {
		if binding.SourceSlot() != binding.TargetSlot() {
			return false
		}
		st, ok := h.windowStateLocked(binding.TargetSlot())
		return ok && st != nil && st.Reducer != nil && st.Effective.Icon == "stop" &&
			harness.NormalizeKnown(st.LastHarness) == harness.Codex
	}
	st, ok := h.windowStateLocked(binding.TargetSlot())
	if !ok || st == nil || st.Reducer == nil {
		return false
	}
	if binding.RecoverySource.Kind != "" {
		identity, sourceWindowID, ok := st.Reducer.ProbeStopRecoveryRoute(
			binding.RecoverySource, binding.Producer.Harness, binding.TargetSlot(), binding.RecoveryAt,
		)
		return ok && identity == binding.Producer && sourceWindowID == binding.SourceSlot()
	}
	if binding.TerminalProducer {
		return st.Reducer.HasTerminalProducerRoute(
			binding.Producer, binding.SourceSlot(), binding.TargetSlot(), binding.TerminalAt,
		)
	}
	return st.Reducer.HasLiveProducerRoute(binding.Producer, binding.SourceSlot(), binding.TargetSlot())
}

// screenProbeBindingEligibilityCurrentLocked adds the target's current reduced
// icon to exact binding validation.
func (h *Handler) screenProbeBindingEligibilityCurrentLocked(
	binding probes.Binding,
	eligible func(string) bool,
) bool {
	if !h.screenProbeBindingCurrentLocked(binding) {
		return false
	}
	st, ok := h.windowStateLocked(binding.TargetSlot())
	if !ok || st == nil {
		return false
	}
	return eligible(st.Effective.Icon)
}
