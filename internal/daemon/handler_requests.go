package daemon

import (
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// The Handler request surface: dispatch, health, and the notification helpers
// the dispatched calls reach for. The Handler itself and its construction are
// in handler.go.

// Handle processes a Request and returns a Response.
func (h *Handler) Handle(req protocol.Request) protocol.Response {
	req = events.NormalizeProducerFields(req)
	req = events.EnsureRequestTraceID(req)
	var resp protocol.Response
	switch req.Type {
	case protocol.TypeHookEvent:
		h.captureHookPayload(req)
		translated, rejection := h.translateHookOrigin(req)
		if rejection != nil {
			resp = h.rejectHookOrigin(translated, rejection)
			break
		}
		resp = h.handleHookEvent(translated)
		resp = h.attachHookDeliveryAcknowledgement(translated, resp)
	case protocol.TypeDiagnosticProbe:
		resp = h.handleDiagnosticProbe(req)
	case protocol.TypeStateUpdate:
		if req.RuntimeSessionID != "" {
			// An operator action on a session the dashboard knows only by its
			// runtime session: the daemon, not the client, names the slot.
			resolved, ok := h.operatorRuntimeUpdate(req)
			if !ok {
				resp = protocol.Response{OK: false, Error: protocol.OriginReasonRuntimeSessionUnknown}
				break
			}
			resp = h.handleStateUpdate(resolved)
			break
		}
		if isRuntimeSyntheticSlot(req.WindowID) {
			// Runtime slots are assigned by hook translation, never named by a
			// client; a direct update could otherwise write into a session.
			resp = protocol.Response{OK: false, Error: protocol.OriginReasonTargetConflict}
			break
		}
		resp = h.handleStateUpdate(req)
	case protocol.TypeStateQuery:
		resp = h.handleStateQuery()
	case protocol.TypeOverviewPreferences:
		resp = h.handleOverviewPreferences(req)
	case protocol.TypeSessionDetail:
		if req.RuntimeSessionID != "" {
			req.WindowID = h.runtimeSessionSlot(req.RuntimeSessionID)
		}
		resp = h.handleSessionDetail(req)
	case protocol.TypeEventsExport:
		resp = h.handleEventsExport(req)
	case protocol.TypeASLSnapshot:
		resp = asl.HandleSnapshot(h.aslBridge.Current())
	case protocol.TypeASLQuestion:
		resp = asl.HandleQuestion(h.aslBridge.Current(), req)
	case protocol.TypeASLAction:
		resp = asl.HandleAction(h.aslBridge.Current(), req)
	case protocol.TypeASLWorkbench:
		resp = asl.HandleWorkbench(h.aslBridge.Current(), req)
	case protocol.TypeFocusWindow:
		resp = h.handleFocusWindow(req)
	case protocol.TypeNotificationSnooze:
		resp = h.handleNotificationSnooze(req)
	case protocol.TypeNotificationDisposition:
		resp = h.handleNotificationDisposition(req)
	case protocol.TypeNotificationTest:
		resp = h.handleNotificationTest()
	case protocol.TypeWorkspaceView:
		resp = h.handleWorkspaceView(req)
	case protocol.TypeHealth:
		resp = h.handleHealth()
	case protocol.TypeShutdown:
		resp = h.handleShutdown()
	default:
		resp = protocol.Response{OK: false, Error: "unknown request type: " + string(req.Type)}
	}
	return resp
}

func (h *Handler) handleOverviewPreferences(req protocol.Request) protocol.Response {
	h.overviewMu.Lock()
	defer h.overviewMu.Unlock()
	if req.OverviewCompact != nil {
		h.overviewCompact = *req.OverviewCompact
		h.overviewCompactSet = true
	}
	compact := h.overviewCompact
	return protocol.Response{OK: true, OverviewCompact: &compact, OverviewCompactExplicit: h.overviewCompactSet}
}

func (h *Handler) handleHealth() protocol.Response {
	var uptime int64
	if !h.startedAt.IsZero() {
		uptime = int64(time.Since(h.startedAt).Seconds())
	}
	ready, degraded := h.Readiness()
	resp := protocol.Response{OK: true, PID: os.Getpid(), UptimeSeconds: uptime, Ready: ready, DegradedReason: degraded}
	if h.buildInfo != nil {
		build := *h.buildInfo
		resp.Build = &build
	}
	if h.metricsFn != nil {
		metrics := h.metricsFn()
		resp.Metrics = &metrics
	}
	if h.notifier != nil {
		capability := h.notifier.Capability()
		resp.Notification = &capability
	}
	if bridge := h.aslBridge.Current(); bridge != nil {
		status := bridge.Status()
		resp.ASLCapability = &status
	}
	reconcile := h.ASLReconcileMetrics()
	resp.ASLReconcile = &reconcile
	// Published unconditionally, like reconciliation and unlike the capability
	// block: the counters describe what arrived on the hook path, which has no
	// bridge and exists whether or not one was ever configured.
	delivery := h.ASLHookDeliveryMetrics()
	resp.ASLHookDelivery = &delivery
	// Published only when the startup load actually ran, so a client can tell a
	// daemon that predates the block (or a hand-built Handler) apart from one
	// that loaded no patterns at all.
	if h.screenPatterns != nil {
		patterns := *h.screenPatterns
		resp.ScreenPatterns = &patterns
	}
	// Status only, never the window tree: health is a probe with an
	// acknowledgement budget, and the tree belongs to the state query. Published
	// unconditionally, so a cold daemon still says so.
	//
	// Deliberately computed after Readiness above and never folded into it: a
	// stale or cold snapshot means Kitty stopped answering, which is a different
	// fault with a different remedy from a daemon that failed its bounded
	// restore. Letting it move Ready or DegradedReason would make every wedged
	// `kitten @ ls` look like a broken daemon, and would erase the real
	// degraded reason a daemon already had.
	resp.KittySnapshot = h.kittySnapshotStatus(time.Now())
	resp.Companion = h.companionHealth()
	return resp
}

// SetBuildInfo stamps the daemon's artifact identity for TypeHealth. Called
// once at startup, before any request is served.
func (h *Handler) SetBuildInfo(info buildinfo.Info) {
	h.buildInfo = &info
}

// SetASLBridge installs the optional agent-session-loop integration. It is
// called once at startup, after the daemon socket is published, so a missing or
// slow ASL cannot delay the core daemon.
func (h *Handler) SetASLBridge(bridge asl.Bridge) {
	h.aslBridge.Install(bridge)
}

// SetMetricsSource installs the server's metrics snapshot function so health
// responses carry request-outcome counters. Called by ipc.NewServer before the
// socket accepts connections; it is part of the ipc.RequestHandler contract,
// which is why it is exported.
func (h *Handler) SetMetricsSource(fn func() protocol.MetricsSnapshot) {
	h.metricsFn = fn
}

func (h *Handler) SetReducerConfig(cfg state.ReducerConfig) {
	h.stateMu.Lock()
	h.reducerCfg = cfg
	for _, st := range h.store {
		if st.Reducer != nil {
			st.Reducer.SetConfig(cfg)
		}
	}
	h.stateMu.Unlock()
}

// IsMuted reports whether the given window is currently marked as muted. The
// daemon refreshes this cache from Kitty snapshots; sweep paths that lack a
// request consult it via the Notifier closure installed in daemon.Run.
func (h *Handler) IsMuted(windowID int) bool {
	h.stateMu.Lock()
	muted := false
	if st, ok := h.windowStateLocked(windowID); ok {
		muted = st.Muted
	}
	h.stateMu.Unlock()
	return muted
}

func (h *Handler) cancelPendingNotification(windowID int) {
	if h.notifier == nil {
		return
	}
	h.notifier.CancelPending(windowID)
}

func (h *Handler) maybeNotify(windowID int, sessionTitle, account, action string, pipeMode bool) {
	if h.notifier == nil {
		return
	}
	h.notifier.MaybeNotifyTarget(h.notificationTarget(windowID, account), sessionTitle, action, pipeMode)
}

func (h *Handler) maybeNotifyTraced(windowID int, sessionTitle, account, action string, pipeMode bool, trace notification.NotificationTrace) {
	if h.notifier == nil {
		return
	}
	h.notifier.MaybeNotifyTracedTarget(h.notificationTarget(windowID, account), sessionTitle, action, pipeMode, trace)
}

// notificationTarget names what a notification for slot is about and what a
// click on it may focus. The runtime session holding the slot travels with the
// target so a notice can be traced, and acted on, by session identity. Only a
// Kitty pane is navigable: its click focuses the exact window through the
// socket the daemon learned for it. A session on another backend has no Kitty
// window behind its slot, so its notification is delivered all the same but
// carries no focus target, and never a slot number dressed as a window ID.
func (h *Handler) notificationTarget(windowID int, account string) notification.NotificationTarget {
	target := notification.NotificationTarget{WindowID: windowID, Account: account, Navigable: windowID > 0}
	if windowID <= 0 {
		return target
	}
	h.stateMu.Lock()
	target.KittyListenOn = h.kittySockets[windowID]
	if binding, bound := h.runtimeRoutes.binding(windowID); bound {
		target.RuntimeSessionID = binding.runtime
		target.Backend = binding.target.backend
	}
	h.stateMu.Unlock()
	if isRuntimeSyntheticSlot(windowID) {
		target.Navigable = false
		target.KittyListenOn = ""
		return target
	}
	if !isReusableKittySocket(target.KittyListenOn) {
		target.KittyListenOn = ""
	}
	return target
}

func (h *Handler) handleShutdown() protocol.Response {
	if h.onShutdown != nil {
		h.shutdownOnce.Do(func() { go h.onShutdown() })
	}
	return protocol.Response{OK: true}
}
