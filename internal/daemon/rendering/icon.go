package rendering

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// DesiredIcon reports the icon the renderer paints for a window, resolving the
// reducer's decision first and falling back to the legacy scalar only when no
// decision has been recorded yet.
func DesiredIcon(st *state.WindowState) string {
	if st == nil {
		return ""
	}
	// Once a state decision has been recorded (StateSet), DesiredIcon is
	// authoritative even when empty — the reducer may legitimately clear an
	// icon while the legacy LastAction scalar still holds a stale value.
	if st.StateSet || st.DesiredIcon != "" || st.LastAction == "" {
		return st.DesiredIcon
	}
	return protocol.IconState(st.LastAction)
}
