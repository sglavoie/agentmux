package daemon

// The daemon's periodic work is split into independent loops so no single
// slow dependency can stall the others:
//
//   - runMaintenance (this file): the single periodic Kitty lister. Publishes
//     the immutable snapshot, refreshes mute state, prunes closed windows,
//     runs ASL lease reconciliation, and flushes batched persistence into the
//     render queue. It performs no Kitty writes and no screen reads.
//   - runWatchdog (watchdog_loop.go): the 1-second staleness evaluator. It
//     measures idleness by accumulating in-process elapsed time (never by
//     rewriting activity timestamps), so delayed rendering, wedged Kitty
//     writes, and sleep-like scheduling gaps cannot distort expiry.
//   - runRenderWorker (renderqueue.go): the single Kitty writer, driven by the
//     latest-generation per-window render queue.
//   - runScreenProbes (this file): Codex prompt and Claude context-limit
//     screen probes, reading the cached snapshot and doing their own GetText
//     round-trips off both loops above.
//   - Event reduction is already independent: the serialized ingress consumer
//     (ingress.go) acknowledges after reduction and never waits on rendering.

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	renderInterval          = 2 * time.Second
	renderDebounce          = 50 * time.Millisecond
	renderWarnAfterFailures = 3
)

type socketScopedKittyClient interface {
	WithSocket(socket string) *kitty.SubprocessClient
}

func (h *Handler) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.maintenanceTick(now)
		}
	}
}

// runScreenProbes drives the Codex prompt and Claude context-limit screen
// probes on their own loop. The probes read the cached snapshot (published by
// the maintenance loop and the render worker) and perform GetText round-trips
// that must never delay snapshot maintenance or rendering; a wedged screen
// read simply stalls this loop until it returns.
func (h *Handler) runScreenProbes(ctx context.Context) {
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			windows, _, ready := h.cachedSnapshot()
			if !ready {
				continue
			}
			h.ProbeCodexScreenPromptsWithSnapshot(now, windows)
			h.ProbeClaudeContextLimitWithSnapshot(now, windows)
		}
	}
}

// initialSnapshotBudget bounds the one-shot startup snapshot+restore taken
// before the public socket is bound. A slow or wedged Kitty must not delay the
// daemon coming up: past this budget the daemon starts degraded and the
// maintenance loop reconciles on its next successful tick.
const initialSnapshotBudget = 750 * time.Millisecond

// restoreStateFromKittyBounded runs the initial snapshot+restore under a
// timeout. It returns (true, "") when the restore completed within timeout, and
// (false, reason) when it timed out. On timeout the restore goroutine keeps
// running and reconciles late; it only ever writes mutex-guarded state
// (snapshot cache, per-window store, renderer bookkeeping), so it is safe for it
// to outlive this call and overlap the maintenance loop.
func (h *Handler) restoreStateFromKittyBounded(now time.Time, timeout time.Duration) (bool, string) {
	done := make(chan struct{})
	go func() {
		h.restoreStateFromKitty(now)
		close(done)
	}()
	select {
	case <-done:
		return true, ""
	case <-time.After(timeout):
		return false, "initial kitty snapshot exceeded " + timeout.String()
	}
}

func (h *Handler) restoreStateFromKitty(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("renderer: startup list windows failed", "err", err)
		return
	}
	// Publish before restore so the hook path can route against the initial
	// snapshot the moment restore finishes.
	h.publishSnapshot(windows, now)
	h.restoreFromUserVars(windows, now)
	h.renderSnapshot(windows)
}

// maintenanceTick lists windows once and runs the maintenance pass over the
// result. It is the periodic entry point of runMaintenance.
func (h *Handler) maintenanceTick(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("maintenance: list windows failed", "err", err)
		return
	}
	h.runSnapshotTick(now, windows)
}

func (h *Handler) runSnapshotTick(now time.Time, windows []kitty.OSWindow) {
	// The maintenance loop is the periodic Kitty lister: publish the fresh
	// snapshot so hook routing reads it instead of calling ListWindows itself.
	// Staleness evaluation deliberately does not live here: the independent
	// watchdog loop owns it, measuring idleness with in-process elapsed time
	// instead of rewriting activity timestamps after tick gaps.
	h.publishSnapshot(windows, now)
	h.refreshMutedFromSnapshot(windows)
	h.pruneClosedWindows(now, liveWindowIDs(windows))
	// First tick doubles as the startup reconciliation; afterwards the interval
	// gate keeps it to every aslReconcileInterval.
	h.maybeReconcileASLLeases(now, windows)
	// Batched-persistence flush and drift convergence: hand every window with
	// pending durable state, failed writes to retry, or a drifted title to the
	// render worker. The maintenance loop itself never writes to Kitty.
	h.enqueueSnapshotCandidates(windows)
}

func (h *Handler) refreshMutedFromSnapshot(windows []kitty.OSWindow) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for i := range windows {
		for j := range windows[i].Tabs {
			tabMuted := palette.ParseTitleParts(windows[i].Tabs[j].Title).Muted
			for k := range windows[i].Tabs[j].Windows {
				win := windows[i].Tabs[j].Windows[k]
				if st, ok := h.windowStateLocked(win.ID); ok {
					st.Muted = tabMuted || win.UserVars.Mute != ""
				}
			}
		}
	}
}

// setMutedFromSnapshot records the cc_mute/title mute state read from a live
// Kitty snapshot. Hook requests may carry the legacy Muted wire field, but the
// daemon deliberately never trusts that client-provided value.
func (h *Handler) setMutedFromSnapshot(windowID int, muted bool) {
	if windowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.ensureWindowStateLocked(windowID).Muted = muted
	h.stateMu.Unlock()
}

// renderSnapshot synchronously renders the given snapshot: it queues every
// window needing work and drains the queue in the calling goroutine. The
// queue's in-flight exclusion keeps this safe to run beside the render worker;
// each window is still rendered by exactly one writer at a time. Startup
// restore, sweeps, probes, and tests use this; production hook flow goes
// through the queue and worker instead.
func (h *Handler) renderSnapshot(windows []kitty.OSWindow) {
	h.enqueueSnapshotCandidates(windows)
	h.drainRenderQueueWith(windows)
}

// enqueueSnapshotCandidates queues every window the given snapshot shows work
// for: pending durable state (batched activity persistence), failed writes to
// retry, and drifted tab titles to converge.
func (h *Handler) enqueueSnapshotCandidates(windows []kitty.OSWindow) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	live := liveWindowIDs(windows)
	for id, st := range h.store {
		if (st.Dirty || st.RenderFailures > 0) && live[id] {
			h.enqueueRenderLocked(id, st)
		}
	}
	for i := range windows {
		for j := range windows[i].Tabs {
			tab := &windows[i].Tabs[j]
			owner, ok := h.renderTitleOwnerLocked(tab)
			if !ok {
				continue
			}
			st, ok := h.windowStateLocked(owner.ID)
			if !ok {
				continue
			}
			if desiredTabTitleLocked(tab, st) != tab.Title {
				h.enqueueRenderLocked(owner.ID, st)
			}
		}
	}
}

func (h *Handler) renderTitleOwnerLocked(tab *kitty.Tab) (*kitty.Window, bool) {
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		return owner, true
	}
	if tab == nil {
		return nil, false
	}
	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if _, ok := h.windowStateLocked(win.ID); !ok {
			continue
		}
		if fallback == nil {
			fallback = win
		}
		if win.IsActive {
			return win, true
		}
	}
	if fallback == nil {
		return nil, false
	}
	return fallback, true
}

func desiredIconForState(st *WindowState) string {
	if st == nil {
		return ""
	}
	// Once a state decision has been recorded (StateSet), DesiredIcon is
	// authoritative even when empty — the reducer may legitimately clear an
	// icon while the legacy LastAction scalar still holds a stale value.
	if st.StateSet || st.DesiredIcon != "" || st.LastAction == "" {
		return st.DesiredIcon
	}
	return IconState(st.LastAction)
}

func (h *Handler) kittyClientForList() kittyTitleClient {
	h.stateMu.Lock()
	socket := h.lastKittyListenOn
	client := h.kittyClient
	h.stateMu.Unlock()
	return scopedKittyClient(client, socket)
}

func (h *Handler) kittyClientForWindow(windowID int) kittyTitleClient {
	h.stateMu.Lock()
	socket := h.kittySockets[windowID]
	if socket == "" {
		socket = h.lastKittyListenOn
	}
	client := h.kittyClient
	h.stateMu.Unlock()
	return scopedKittyClient(client, socket)
}

// hookKittyClient returns the Kitty client used for hook-time ListWindows and
// GetText calls, scoped to the stable socket carried by the request. It falls
// back to the daemon's established client only when the request has no reusable
// (transferable) address, so a stale indicator forwarding an "fd:" descriptor
// cannot force a dial through a process-local endpoint.
func (h *Handler) hookKittyClient(req Request) kittyTitleClient {
	if isReusableKittySocket(req.KittyListenOn) {
		return scopedKittyClient(h.kittyClient, strings.TrimSpace(req.KittyListenOn))
	}
	return h.kittyClient
}

func scopedKittyClient(client kittyTitleClient, socket string) kittyTitleClient {
	if socket == "" {
		return client
	}
	if scoped, ok := client.(socketScopedKittyClient); ok {
		return scoped.WithSocket(socket)
	}
	return client
}

func liveWindowIDs(windows []kitty.OSWindow) map[int]bool {
	live := make(map[int]bool)
	for _, ow := range windows {
		for _, tab := range ow.Tabs {
			for _, win := range tab.Windows {
				live[win.ID] = true
			}
		}
	}
	return live
}
