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

// legacyStateUpdate is nil in production. Tests install the old window-ID
// request path for historical reducer fixtures.
var legacyStateUpdate func(*Handler, protocol.Request) protocol.Response

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
		if legacyStateUpdate == nil {
			resp = protocol.Response{OK: false, Error: protocol.OriginReasonInvalidRuntimeSession}
		} else {
			resp = legacyStateUpdate(h, req)
		}
	case protocol.TypeSessionReview:
		resp = h.handleSessionReview(req)
	case protocol.TypeStateQuery:
		resp = h.handleStateQuery()
	case protocol.TypeOverviewPreferences:
		resp = h.handleOverviewPreferences(req)
	case protocol.TypeSessionDetail:
		resp = h.handleSessionDetail(req)
	case protocol.TypeEventsExport:
		resp = h.handleEventsExport(req)
	case protocol.TypeASLSnapshot:
		resp = asl.HandleSnapshot(h.aslBridge.Current())
	case protocol.TypeASLQuestion:
		resp = asl.HandleQuestion(h.aslBridge.Current(), req)
	case protocol.TypeASLAction:
		resp = asl.HandleAction(h.aslBridge.Current(), req)
	case protocol.TypeNotificationSnooze:
		resp = h.handleNotificationSnooze(req)
	case protocol.TypeNotificationDisposition:
		resp = h.handleNotificationDisposition(req)
	case protocol.TypeNotificationTest:
		resp = h.handleNotificationTest()
	case protocol.TypeWorkspaceView:
		resp = h.handleWorkspaceView(req)
	case protocol.TypeFocusSession:
		resp = h.handleFocusSession(req)
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
	if h.daemonSettings != nil {
		resp.DaemonSettings = make(map[string]string, len(h.daemonSettings))
		for key, value := range h.daemonSettings {
			resp.DaemonSettings[key] = value
		}
	}
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
	resp.Companion = h.companionHealth()
	// Published unconditionally: a daemon with no
	// runtime session still says that its reconciliation runs.
	resp.Runtime = h.runtimeHealthSnapshot(time.Now())
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
// daemon refreshes this cache from tmux observations; sweep paths that lack a
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
	h.notifier.MaybeNotifyTarget(h.notificationTarget(windowID, account), h.notificationTitle(windowID, sessionTitle), action, pipeMode)
}

func (h *Handler) maybeNotifyTraced(windowID int, sessionTitle, account, action string, pipeMode bool, trace notification.NotificationTrace) {
	if h.notifier == nil {
		return
	}
	h.notifier.MaybeNotifyTracedTarget(h.notificationTarget(windowID, account), h.notificationTitle(windowID, sessionTitle), action, pipeMode, trace)
}

// notificationTitle names the session a notification for slot is about. A
// session is named by its supplied title or by the project in its runtime
// label. With neither, the notifier falls back to the product name.
func (h *Handler) notificationTitle(windowID int, sessionTitle string) string {
	if sessionTitle != "" || windowID <= 0 {
		return sessionTitle
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	binding, bound := h.runtimeRoutes.binding(windowID)
	if !bound {
		return ""
	}
	return h.runtimeLabels[binding.runtime].project
}

// notificationTarget names what a notification for slot is about and what a
// click on it may focus. The runtime session holding the slot travels with the
// target so a notice can be traced and acted on by session identity.
func (h *Handler) notificationTarget(windowID int, account string) notification.NotificationTarget {
	target := notification.NotificationTarget{WindowID: windowID, Account: account}
	if windowID <= 0 {
		return target
	}
	h.stateMu.Lock()
	if binding, bound := h.runtimeRoutes.binding(windowID); bound {
		target.RuntimeSessionID = binding.runtime
		target.Backend = binding.target.backend
		target.Navigable = target.RuntimeSessionID != "" && target.Backend == "tmux"
	}
	h.stateMu.Unlock()
	return target
}

func (h *Handler) handleShutdown() protocol.Response {
	if h.onShutdown != nil {
		h.shutdownOnce.Do(func() { go h.onShutdown() })
	}
	return protocol.Response{OK: true}
}
