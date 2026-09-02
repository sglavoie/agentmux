package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/activity"
)

// screenTextCacheFor builds a one-shot per-tick screen capture cache over the
// handler's Kitty client, for an entry point that drives a single probe outside
// the shared tick. Those paths keep the same "one read per pane" guarantee
// without reaching for a nil check at every call site. The cache itself lives in
// internal/daemon/activity; this is the binding to the daemon's Kitty client.
func (h *Handler) screenTextCacheFor() *activity.ScreenTextCache {
	return activity.NewScreenTextCache(h.kittyClient)
}
