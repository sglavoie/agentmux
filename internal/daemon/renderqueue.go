package daemon

// The reducer records generations for diagnostics. Runtime sessions settle
// through the tmux checkpoint and status writer.

import (
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// enqueueRenderLocked marks a generation ready for the runtime checkpoint.
func (h *Handler) enqueueRenderLocked(windowID int, st *state.WindowState) {
	if st == nil {
		return
	}
	if st.Seq > st.QueuedGen {
		st.QueuedGen = st.Seq
	}
	h.markRenderStageLocked(windowID, st.Seq, rendering.StageQueued, "pending", "latest_generation", true)
}
