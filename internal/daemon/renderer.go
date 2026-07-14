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
//   - runScreenProbes (this file): Codex prompt, Claude context-limit, Claude
//     idle stop screen probes, and the Copilot transcript stop probe, reading
//     the cached snapshot and doing their own GetText/transcript round-trips off
//     both loops above.
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

// runScreenProbes drives the Codex prompt, Claude context-limit, Claude idle
// stop, and Copilot transcript stop probes on their own loop. The probes read
// the cached snapshot (published by
// the maintenance loop and the render worker) and perform GetText/transcript
// round-trips that must never delay snapshot maintenance or rendering; a wedged
// screen read or transcript read simply stalls this loop until it returns.
func (h *Handler) runScreenProbes(ctx context.Context) {
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			snapshot, ready := h.cachedScreenProbeSnapshot()
			if !ready {
				continue
			}
			h.runScreenProbesWithSnapshot(now, snapshot)
		}
	}
}

func (h *Handler) runScreenProbesWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	h.probeCodexScreenPromptsWithSnapshot(now, snapshot)
	// Context-limit probing runs before the idle stop probe so a
	// context-limited Claude tab is settled to the error state first; the idle
	// classifier additionally bails on the context-limit marker, so the two
	// never fight over the same tab.
	h.probeClaudeContextLimitWithSnapshot(now, snapshot)
	h.probeClaudeScreenStopWithSnapshot(now, snapshot)
	// The Copilot transcript stop probe reads no screen: it consults the
	// interactive session transcript's latest state-bearing event to recover a
	// stuck ⚡ whose native agentStop/sessionEnd hook was lost.
	h.probeCopilotTranscriptStopWithSnapshot(now, snapshot)
}

// initialSnapshotBudget bounds the one-shot startup snapshot+restore taken
// before the public socket is bound. A slow or wedged Kitty must not delay the
// daemon coming up: past this budget the daemon starts degraded and the
// maintenance loop reconciles on its next successful tick.
// Keep this below the public socket-readiness budget. A snapshot that misses
// this deadline is reconciled by the maintenance loop after degraded startup.
const initialSnapshotBudget = 400 * time.Millisecond

// restoreStateFromKittyBounded runs the initial snapshot+restore under a
// timeout. It returns (true, "") only when the initial Kitty snapshot actually
// succeeded and restore completed within timeout; it returns (false, reason)
// both when the restore timed out AND when it finished fast because the initial
// ListWindows failed. Reporting a fast list failure as degraded (rather than
// ready over a cold cache and empty store) is essential: MarkReady would
// otherwise stop the maintenance loop from ever restoring durable amx_state, so
// a transient startup error would become permanent reducer-state loss for the
// daemon lifetime. On timeout the restore goroutine keeps running and
// reconciles late; it only ever writes mutex-guarded state (snapshot cache,
// per-window store, renderer bookkeeping), so it is safe for it to outlive this
// call and overlap the maintenance loop.
func (h *Handler) restoreStateFromKittyBounded(now time.Time, timeout time.Duration) (bool, string) {
	type restoreResult struct {
		ok  bool
		err error
	}
	done := make(chan restoreResult, 1)
	go func() {
		ok, err := h.restoreStateFromKitty(now)
		done <- restoreResult{ok: ok, err: err}
	}()
	select {
	case res := <-done:
		if res.ok {
			return true, ""
		}
		if res.err != nil {
			return false, "initial kitty snapshot failed: " + res.err.Error()
		}
		return false, "initial kitty snapshot unavailable"
	case <-time.After(timeout):
		return false, "initial kitty snapshot exceeded " + timeout.String()
	}
}

// restoreStateFromKitty takes the initial Kitty snapshot and restores durable
// state from it. It returns (true, nil) only when ListWindows succeeded and the
// snapshot was published/restored; (false, err) when the initial list failed;
// and (false, nil) when no Kitty client is configured. A false return must be
// treated as a degraded startup so the maintenance loop still owns the durable
// restore.
func (h *Handler) restoreStateFromKitty(now time.Time) (bool, error) {
	client := h.kittyClientForList()
	if client == nil {
		return false, nil
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("renderer: startup list windows failed", "err", err)
		return false, err
	}
	// Publish before restore so the hook path can route against the initial
	// snapshot the moment restore finishes.
	h.publishSnapshotWithReceipts(windows, reducerReceipts, now)
	h.restoreFromUserVars(windows, now)
	h.renderSnapshot(windows)
	return true, nil
}

// maintenanceTick lists windows once and runs the maintenance pass over the
// result. It is the periodic entry point of runMaintenance.
func (h *Handler) maintenanceTick(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("maintenance: list windows failed", "err", err)
		return
	}
	// A daemon whose bounded startup snapshot timed out is only temporarily
	// degraded. Restore the durable amx_state this first successful list carries
	// BEFORE advertising ready, so a health check or startup smoke never observes
	// ready over an empty or partial store. For a window that already holds
	// in-process state (a hook event that landed during the degraded window, or a
	// late-completing startup restore), restoreFromUserVars keeps that accepted
	// state authoritative and merges in only the disjoint durable facts the
	// persisted payload still carries — never overwriting a same-source in-process
	// fact — so no accepted hook state is rolled back while a live ASL lease, pipe
	// child, subagent, or attention/error contribution is not silently dropped.
	// runSnapshotTickWithReceipts then enqueues the freshly restored windows for rendering.
	recovering := false
	if ready, _ := h.Readiness(); !ready {
		recovering = true
		h.restoreFromUserVars(windows, now)
	}
	h.runSnapshotTickWithReceipts(now, windows, reducerReceipts)
	if recovering {
		h.MarkReady()
	}
}

func (h *Handler) runSnapshotTickWithReceipts(now time.Time, windows []kitty.OSWindow, reducerReceipts map[int]uint64) {
	// The maintenance loop is the periodic Kitty lister: publish the fresh
	// snapshot so hook routing reads it instead of calling ListWindows itself.
	// Staleness evaluation deliberately does not live here: the independent
	// watchdog loop owns it, measuring idleness with in-process elapsed time
	// instead of rewriting activity timestamps after tick gaps.
	h.publishSnapshotWithReceipts(windows, reducerReceipts, now)
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
	h.refreshMutedFromSnapshotLocked(windows)
}

// refreshMutedFromSnapshotLocked refreshes the window mute cache from one
// immutable Kitty snapshot. Callers hold stateMu.
func (h *Handler) refreshMutedFromSnapshotLocked(windows []kitty.OSWindow) {
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
