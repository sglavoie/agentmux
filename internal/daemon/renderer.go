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

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type socketScopedKittyClient interface {
	WithSocket(socket string) *kitty.SubprocessClient
}

func (h *Handler) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(rendering.Interval)
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
	ticker := time.NewTicker(rendering.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.runScreenProbeTick(now)
		}
	}
}

// runScreenProbeTick is one pass of every screen probe: the Kitty windows of
// the cached snapshot when there is one, then the sessions on other backends,
// which need no Kitty at all. Both halves share the tick's screen cache.
func (h *Handler) runScreenProbeTick(now time.Time) {
	screens := h.screenTextCacheFor()
	if snapshot, ready := h.cachedScreenProbeSnapshot(); ready {
		h.runScreenProbesWithScreens(now, snapshot, screens)
	}
	h.probeRuntimeScreens(now, screens)
}

func (h *Handler) runScreenProbesWithSnapshot(now time.Time, snapshot probes.Snapshot) {
	h.runScreenProbesWithScreens(now, snapshot, h.screenTextCacheFor())
}

// runScreenProbesWithScreens runs the Kitty half of a tick over screens.
//
// One visible-screen observation per source pane for the whole tick. Beyond
// halving Kitty round-trips for a Claude pane (two probes) and a bolted
// Codex pane (prompt then idle), this is what makes the probes below
// classify the SAME frame — closing the race where the context-limit veto
// inside the idle classifier was evaluated against a different capture than
// the one that decided the banner.
func (h *Handler) runScreenProbesWithScreens(now time.Time, snapshot probes.Snapshot, screens *activity.ScreenTextCache) {
	h.probeCodexScreenPromptsWithSnapshot(now, snapshot, screens)
	// Context-limit probing runs before the idle stop probe so a
	// context-limited Claude tab is settled to the error state first; the idle
	// classifier additionally bails on the context-limit marker, so the two
	// never fight over the same tab.
	h.probeClaudeContextLimitWithSnapshot(now, snapshot, screens)
	h.probeClaudeScreenStopWithSnapshot(now, snapshot, screens)
	// The Copilot transcript stop probe SETTLES from the interactive session
	// transcript's latest state-bearing event, recovering a stuck ⚡ whose native
	// agentStop/sessionEnd hook was lost. It takes the same per-tick cache
	// because the screen is its REFUSAL: a Copilot pane sitting on a permission
	// prompt has stopped writing records, so the transcript tail can read
	// terminal while a human is being waited on, and only the pane says so. The
	// Copilot adapter can never report Terminal, so handing it this cache can
	// only ever remove settles, never add one.
	h.probeCopilotTranscriptStopWithSnapshot(now, snapshot, screens)
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
	client, socket := h.kittyListTarget()
	if client == nil {
		return false, nil
	}
	capture, err := h.captureKittySnapshot(client, socket)
	if err != nil {
		slog.Debug("renderer: startup list windows failed", "err", err)
		return false, err
	}
	windows := capture.windows
	// Publish before restore so the hook path can route against the initial
	// snapshot the moment restore finishes. The snapshot carries the list's own
	// completion time; restore keeps the daemon's start time, because a restored
	// durable fact is dated by the event it replays, not by how long the startup
	// list took to answer.
	h.publishCapture(capture)
	h.restoreFromUserVars(windows, now)
	// ASL reconciliation deliberately does NOT run here. Restore rebuilds leases
	// from Kitty user vars, which record what was true when the daemon stopped,
	// so the first render can be stale by up to one maintenance tick — but a
	// sweep costs one full parse of every plan's state document, work that grows
	// with the operator's plan history and has been measured in the hundreds of
	// milliseconds. Inside this bounded budget that turns a long history into a
	// permanently degraded startup, which loses far more than two seconds of
	// staleness: MarkReady never fires and the durable restore never lands. The
	// first maintenance tick reconciles instead, within rendering.Interval of
	// here.
	h.renderSnapshot(windows)
	return true, nil
}

// maintenanceTick lists windows once and runs the maintenance pass over the
// result. It is the periodic entry point of runMaintenance.
func (h *Handler) maintenanceTick(now time.Time) {
	h.maintainCaptureStore(now)
	client, socket := h.kittyListTarget()
	if client == nil {
		// Runtime sessions on other backends still need retiring and
		// checkpointing when there is no Kitty to list, and their ASL runs
		// still need reconciling and their supervisor questions polling.
		h.maintainRuntimeSessions(now)
		h.maintainASLWithoutSnapshot(now)
		return
	}
	capture, err := h.captureKittySnapshot(client, socket)
	if err != nil {
		slog.Debug("maintenance: list windows failed", "err", err)
		h.maintainRuntimeSessions(now)
		h.maintainASLWithoutSnapshot(now)
		return
	}
	windows := capture.windows
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
	// runSnapshotTickWithCapture then enqueues the freshly restored windows for rendering.
	recovering := false
	if ready, _ := h.Readiness(); !ready {
		recovering = true
		h.restoreFromUserVars(windows, now)
	}
	h.runSnapshotTickWithCapture(now, capture)
	if recovering {
		h.MarkReady()
	}
}

// maintainASLWithoutSnapshot is the ASL half of a maintenance tick that has no
// Kitty snapshot. Reconciliation without windows touches only runtime sessions
// on other backends and never judges a Kitty window's lease; the supervisor
// refresh reads only the registered lease records, which no terminal is needed
// for. Together they keep a run's loop marker, question, and notification
// current while no viewer — and no Kitty — is running.
func (h *Handler) maintainASLWithoutSnapshot(now time.Time) {
	h.stateMu.Lock()
	runtimeState := h.hasRuntimeASLStateLocked()
	h.stateMu.Unlock()
	if runtimeState {
		// Gated on there being something to reconcile: a skipped sweep would
		// still restart the interval and delay the first real one after Kitty
		// answers again.
		h.maybeReconcileASLLeases(now, nil)
	}
	h.refreshASLSupervisorQuestions(now)
}

// runSnapshotTickWithCapture runs one maintenance pass over a completed
// capture. The two times it works with are deliberately distinct: the snapshot
// is published under the capture's completion time (freshness is a property of
// the read), while pruning and ASL reconciliation keep the tick time they were
// scheduled for (expiry is a property of the schedule, and must not drift by
// however long Kitty took to answer).
func (h *Handler) runSnapshotTickWithCapture(now time.Time, capture kittySnapshotCapture) {
	windows := capture.windows
	// The maintenance loop is the periodic Kitty lister: publish the fresh
	// snapshot so hook routing reads it instead of calling ListWindows itself.
	// Staleness evaluation deliberately does not live here: the independent
	// watchdog loop owns it, measuring idleness with in-process elapsed time
	// instead of rewriting activity timestamps after tick gaps.
	h.publishCapture(capture)
	h.pruneClosedWindowsFor(now, liveWindowIDs(windows), capture.socket)
	h.pruneForeignWindows(now, capture.socket)
	h.maintainRuntimeSessions(now)
	// This is the startup reconciliation as well as the periodic one: the
	// interval gate has never run when the daemon comes up, so the first tick
	// always sweeps, and every aslReconcileInterval thereafter. Startup restore
	// stays out of it so a large plan history cannot delay readiness.
	h.maybeReconcileASLLeases(now, windows)
	// Registered ASL runs expose a fixed-size, text-free supervisor projection.
	// Refresh it every maintenance tick so a mailbox question and a supervisor
	// Stop correlate within the existing two-second render cadence.
	h.refreshASLSupervisorQuestions(now)
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
					muted := tabMuted || win.UserVars.Mute != ""
					if st.Muted != muted {
						h.companionDirty()
					}
					st.Muted = muted
				}
			}
		}
	}
}

// refreshFocusFromSnapshotLocked clears the unseen bit on every window the given
// immutable Kitty snapshot shows the user actually looking at. Focus is the
// observation that a finished turn has been read, so this is where "seen"
// happens: no key, no command, just the pane being on screen at snapshot time.
// The same observation time becomes PresentationChangedAt, rather than a later
// renderer clock sample, so the durable transition retains its real provenance.
//
// Unlike refreshMutedFromSnapshotLocked, which recomputes the mute cache in both
// directions from each snapshot, this is deliberately ONE-DIRECTIONAL: it only
// ever clears Unseen and never sets it. Raising the bit belongs to
// publishEffectiveLocked, which is the only place that knows a window just
// transitioned into rest. A snapshot that merely fails to observe focus is not
// evidence the user looked away — it is the normal state of every window but one,
// and reasserting the bit from it would re-raise every row the user has ever
// read, two seconds at a time.
//
// The clear is a DURABLE mutation and goes through markWindowDirtyLocked like any
// other, which is what the true→false guard below exists for. Two properties hang
// on it. First, the bit lives only in amx_state — no title carries it, so
// enqueueSnapshotCandidates' title-drift leg can never rediscover it and a clear
// that skipped the dirty mark would be lost on the next daemon restart, restoring
// the row the user just read straight back into Needs Attention. Second, Seq is
// the renderer's generation: advancing it is what makes a render candidate built
// before this observation stale, so a pass already in flight with the pre-focus
// payload requeues instead of persisting the unseen bit over the cleared one.
//
// The guard also bounds the cost to exactly one generation per transition. Every
// subsequent snapshot — one every rendering.Interval for as long as the pane keeps
// focus — finds the bit already clear and marks nothing, so looking at a window
// does not rewrite its amx_state twice a second for as long as you look at it.
// Callers hold stateMu.
func (h *Handler) refreshFocusFromSnapshotLocked(windows []kitty.OSWindow, observedAt time.Time) {
	for i := range windows {
		for j := range windows[i].Tabs {
			for k := range windows[i].Tabs[j].Windows {
				if !snapshotPaneFocused(&windows[i], &windows[i].Tabs[j], &windows[i].Tabs[j].Windows[k]) {
					continue
				}
				windowID := windows[i].Tabs[j].Windows[k].ID
				st, ok := h.windowStateLocked(windowID)
				if !ok || !st.Unseen {
					continue
				}
				st.Unseen = false
				st.PresentationChangedAt = observedAt
				h.markWindowDirtyLocked(windowID)
				h.companionDirty()
			}
		}
	}
}

// snapshotPaneFocused reports whether a pane is the one the user is looking at:
// the active pane, of the active tab, of the focused OS window. All three are
// required — Kitty marks the active pane of every tab and the active tab of every
// OS window, so either flag alone is true for panes nobody can see.
func snapshotPaneFocused(osWindow *kitty.OSWindow, tab *kitty.Tab, win *kitty.Window) bool {
	return osWindow.IsFocused && tab.IsActive && win.IsActive
}

// windowFocusedFromSnapshotLocked answers the same question for one window ID
// from the last published snapshot, which is what the publish path has instead of
// a tree. A cold or stale cache answers "not focused", and that is the safe
// direction: the worst case is one row raised as unseen that the next snapshot's
// focus observation clears within the render cadence. Callers hold stateMu.
func (h *Handler) windowFocusedFromSnapshotLocked(windowID int) bool {
	windows, _, ready := h.snapshots.Load()
	if !ready {
		return false
	}
	osWindow, tab, win, ok := kitty.FindWindowLocationByID(windows, windowID)
	if !ok {
		return false
	}
	return snapshotPaneFocused(osWindow, tab, win)
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
			if rendering.DesiredTabTitle(tab, st) != tab.Title {
				h.enqueueRenderLocked(owner.ID, st)
			}
		}
	}
}

// renderTitleOwnerLocked resolves the tab's title owner against the windows
// this daemon actually tracks. Callers hold stateMu.
func (h *Handler) renderTitleOwnerLocked(tab *kitty.Tab) (*kitty.Window, bool) {
	return rendering.TitleOwner(tab, func(windowID int) bool {
		_, ok := h.windowStateLocked(windowID)
		return ok
	})
}

// kittyListTarget returns the client every daemon list path reads through,
// together with the socket it was scoped to. The socket comes back rather than
// staying inside because a published snapshot has to record which Kitty
// instance answered it: h.lastKittyListenOn moves as soon as a hook arrives
// from another instance, so re-reading it after the read would attribute the
// tree to whichever Kitty spoke last instead of to the one that produced it.
func (h *Handler) kittyListTarget() (kittyTitleClient, string) {
	h.stateMu.Lock()
	socket := h.lastKittyListenOn
	client := h.kittyClient
	h.stateMu.Unlock()
	return scopedKittyClient(client, socket), socket
}

// currentKittyListenOn returns the daemon's effective Kitty socket right now.
// It is a point-in-time read and is never a substitute for the socket a
// specific capture recorded.
func (h *Handler) currentKittyListenOn() string {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.lastKittyListenOn
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
func (h *Handler) hookKittyClient(req protocol.Request) kittyTitleClient {
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
