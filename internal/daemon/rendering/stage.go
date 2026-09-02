package rendering

import "github.com/sglavoie/agentmux/internal/daemon/protocol"

// The stage vocabulary a render generation moves through. It is a consumer
// contract: these strings are the render_outcome values carried by exported
// session events and the daemon.renderer.<stage> journal record names. Only
// the stages a caller can ask for are exported; "dirty" is the seed this
// package stamps on a generation nobody has presented yet.
const (
	stageDirty     = "dirty"
	StageQueued    = "queued"
	StageAttempted = "attempted"
	StagePersisted = "persisted"
	StageRendered  = "rendered"
	StageFailed    = "failed"
)

// stageTerminal reports whether a generation's trace has reached a stage that
// may no longer be coalesced into a newer one.
func stageTerminal(trace protocol.RenderGenerationTrace) bool {
	// Failed generations remain retryable. Their durable failure row stays in
	// the journal, while the in-memory generation can advance through a later
	// queued/attempted/successful retry without creating a duplicate generation.
	return trace.Stage == StageRendered
}

// StageCompletes reports whether a stage ends the generation's render pass, so
// the causal rows it coalesced can be stamped with its outcome.
func StageCompletes(stage string) bool {
	return stage == StageRendered || stage == StageFailed
}
