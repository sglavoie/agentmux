package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
)

func (h *Handler) enableConversationThreads() {
	store, err := conversation.DefaultThreadStore()
	if err != nil {
		slog.Warn("conversation history unavailable", "error", err)
		return
	}
	h.conversationThreads = &store
}

// Capture only identities accepted by the reducer's interactive ownership
// rules. Disk work happens after releasing stateMu. Observation is independent
// of Flight Log preferences and survives closing a session or resetting stats.
func (h *Handler) persistConversationBindings() {
	if h.conversationThreads == nil {
		return
	}
	bindings := map[string]conversation.Identity{}
	h.stateMu.Lock()
	now := time.Now().Unix()
	for slot, state := range h.store {
		identity, ok := state.CurrentInteractiveConversation(now)
		if !ok {
			continue
		}
		if route, ok := h.runtimeRoutes.bySlot[slot]; ok {
			bindings[route.runtime] = identity
			continue
		}
	}
	h.stateMu.Unlock()
	h.conversationThreadsMu.Lock()
	defer h.conversationThreadsMu.Unlock()
	if h.conversationThreadBindings == nil {
		h.conversationThreadBindings = map[string]bool{}
	}
	for key := range bindings {
		if h.conversationThreadBindings[key] {
			delete(bindings, key)
		}
	}
	if err := h.conversationThreads.Observe(bindings); err != nil {
		slog.Warn("could not retain conversation identity", "error", err)
		return
	}
	for key := range bindings {
		h.conversationThreadBindings[key] = true
	}
}
