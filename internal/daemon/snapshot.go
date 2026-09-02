package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file holds the Handler's seams onto the immutable Kitty snapshot cache
// in internal/daemon/probes. The cache itself, its freshness epochs, and the
// probe-facing projection live there; what stays here is everything that needs
// the Handler: sampling reducer receipts under stateMu, republishing the mute
// cache in the same critical section as a publication, and the one best-effort
// ListWindows the daemon performs at client-injection time.

// captureSnapshotReducerReceipts samples every in-process window receipt
// before a potentially slow ListWindows call. The returned map is immutable
// once published with the resulting Kitty evidence.
func (h *Handler) captureSnapshotReducerReceipts() map[int]uint64 {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	receipts := make(map[int]uint64, len(h.store))
	for windowID, st := range h.store {
		if st != nil {
			receipts[windowID] = st.ShadowSeq
		}
	}
	return receipts
}

// publishSnapshot records windows as the current immutable snapshot. It is the
// single seam the render/restore loop uses; the hook path only ever reads via
// cachedSnapshot.
func (h *Handler) publishSnapshot(windows []kitty.OSWindow, now time.Time) {
	h.publishSnapshotWithReceipts(windows, h.captureSnapshotReducerReceipts(), now)
}

// publishSnapshotWithReceipts publishes windows with reducer receipts captured
// before the ListWindows call that returned them. Production listers use this
// seam; publishSnapshot remains a convenient atomic-now wrapper for tests and
// callers that already hold an in-memory tree.
func (h *Handler) publishSnapshotWithReceipts(windows []kitty.OSWindow, reducerReceipts map[int]uint64, now time.Time) {
	// Publish the new generation and its mute cache in one state critical
	// section. Probe apply paths hold the same lock while checking gen, so an
	// older observation cannot pass its generation check and then overwrite a
	// mute from a snapshot published in between the check and mutation.
	h.stateMu.Lock()
	h.refreshMutedFromSnapshotLocked(windows)
	h.snapshots.PublishWithReceipts(windows, reducerReceipts, now)
	h.stateMu.Unlock()
}

// cachedSnapshot returns the last published Kitty snapshot without any Kitty
// I/O. ready is false until the initial restore has published once.
func (h *Handler) cachedSnapshot() (windows []kitty.OSWindow, gen uint64, ready bool) {
	return h.snapshots.Load()
}

// cachedScreenProbeSnapshot returns the last published external evidence and
// its causally earlier reducer receipts without any Kitty I/O.
func (h *Handler) cachedScreenProbeSnapshot() (probes.Snapshot, bool) {
	return h.snapshots.ScreenProbeSnapshot()
}

// screenProbeSnapshotForWindows binds an already-available window tree to the
// current reducer receipts. Direct probe wrappers and tests use it when they
// supply their own ListWindows result instead of consuming the shared cache.
func (h *Handler) screenProbeSnapshotForWindows(windows []kitty.OSWindow) probes.Snapshot {
	return probes.NewSnapshot(windows, h.captureSnapshotReducerReceipts())
}

// primeSnapshot performs one best-effort ListWindows through the injected client
// and publishes the result. It is used at client injection time (and by tests)
// so the hook path has an immediate cache to route against; a list error leaves
// the cache cold and the maintenance loop reconciles on its next tick. It never
// returns an error because a cold cache is a supported degraded state, not a
// failure.
func (h *Handler) primeSnapshot(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		return
	}
	h.publishSnapshotWithReceipts(windows, reducerReceipts, now)
}
