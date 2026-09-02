package daemon

import (
	"context"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func (h *Handler) setDecisionJournal(journal *diagnostics.Journal) {
	h.decisionJournal = journal
}

// recordDecisionEvent persists one finalized row into the causal journal. The
// record shape is eventlog's; the fail-open bounded append is the daemon's, so
// a slow or unwritable journal can never hold up the hook path.
func (h *Handler) recordDecisionEvent(event protocol.SessionEvent) {
	if h == nil || h.decisionJournal == nil {
		return
	}
	envelope := eventlog.DecisionEnvelope(event)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}
