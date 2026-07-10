package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type hookEventContext struct {
	req     Request
	windows []kitty.OSWindow
	tab     *kitty.Tab
	win     *kitty.Window
}

func (h *Handler) handleHookEvent(req Request) Response {
	eventOrAction := strings.TrimSpace(req.Event)
	if eventOrAction == "" {
		slog.Debug("hook event ignored: missing event")
		return Response{OK: true}
	}

	payload, err := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
	if err != nil {
		slog.Warn("hook event payload unreadable; continuing with empty payload", "event", eventOrAction, "err", err)
		payload = hookevent.HookPayload{}
	}

	ctx := h.resolveHookEventContext(req)
	if ctx.req.WindowID <= 0 {
		slog.Debug("hook event ignored: missing window ID", "event", eventOrAction)
		return Response{OK: true}
	}

	if hookevent.IsIgnoredEvent(eventOrAction, payload) {
		ctx.req.Event = eventOrAction
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonIgnoredEvent)
		slog.Debug("hook event intentionally ignored",
			"window_id", ctx.req.WindowID,
			"event", eventOrAction,
			"notification_type", payload.EffectiveNotificationType(),
		)
		return Response{OK: true}
	}

	action, ok := hookevent.ResolveAction(eventOrAction, payload)
	if !ok {
		ctx.req.Event = eventOrAction
		h.appendSuppressedHookEvent(ctx.req, "", suppressionReasonUnknownEvent)
		slog.Debug("hook event unknown, ignoring", "window_id", ctx.req.WindowID, "event", eventOrAction)
		return Response{OK: true}
	}

	if isAgentSessionLoopEvent(eventOrAction) {
		if handled := h.handleLoopLifecycleHookEvent(eventOrAction, action, payload, ctx.req, ctx.windows); handled {
			return Response{OK: true}
		}
	}

	if shouldNoOpForTabTitleOwner(eventOrAction, ctx.tab, ctx.req.WindowID) {
		ctx.req.Event = eventOrAction
		h.appendSuppressedHookEvent(ctx.req, action, suppressionReasonOwnerMismatch)
		if owner, ok := kitty.FindTabTitleOwner(ctx.tab); ok {
			slog.Debug("hook event ignored: window does not own tab title",
				"window_id", ctx.req.WindowID,
				"owner_window_id", owner.ID,
				"event", eventOrAction,
			)
		}
		return Response{OK: true}
	}

	preservesIcon := hookevent.PreservesCurrent(eventOrAction) && action == "active"
	contextLimitDetected := hookevent.PayloadHasContextLimit(payload)
	if !contextLimitDetected && hookevent.ShouldProbeWindowForContextLimit(eventOrAction, action) && h.kittyClient != nil {
		text, textErr := h.kittyClient.GetText(kitty.MatchWindowByID(ctx.req.WindowID), "screen")
		if textErr != nil {
			slog.Debug("hook event context-limit probe failed",
				"window_id", ctx.req.WindowID,
				"event", eventOrAction,
				"err", textErr,
			)
		} else if contextlimit.ScreenShowsMarker(text) {
			action = "stop_failure"
			preservesIcon = false
			contextLimitDetected = true
		}
	}

	resetFlowDetected := hookevent.PayloadHasResetFlow(ctx.req.Harness, eventOrAction, payload)
	stateReq := buildHookStateUpdateRequest(
		ctx.req.WindowID,
		action,
		eventOrAction,
		payload,
		ctx.req.SessionTitle,
		ctx.req.Account,
		ctx.req.Harness,
		ctx.req.currentIconState,
		ctx.req.PipeMode,
		preservesIcon,
		contextLimitDetected,
		resetFlowDetected,
		ctx.req.KittyListenOn,
	)
	if h.deferResetSensitiveStop(stateReq, payload) {
		return Response{OK: true}
	}
	h.handleStateUpdate(stateReq)
	return Response{OK: true}
}

func (h *Handler) resolveHookEventContext(req Request) hookEventContext {
	ctx := hookEventContext{req: req}
	if ctx.req.WindowID <= 0 {
		return ctx
	}
	if h.kittyClient == nil {
		return ctx
	}
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("hook event kitty lookup failed", "window_id", ctx.req.WindowID, "err", err)
		return ctx
	}
	ctx.windows = windows
	tab, win, found := kitty.FindTabByWindowID(windows, ctx.req.WindowID)
	if !found {
		return ctx
	}
	ctx.tab = tab
	ctx.win = win
	parts := palette.ParseTitleParts(tab.Title)
	ctx.req.SessionTitle = parts.Base
	ctx.req.currentIconState = parts.State
	ctx.req.Account = win.UserVars.EffectiveProfile()
	ctx.req.Harness = win.UserVars.EffectiveHarness()
	h.setMutedFromSnapshot(ctx.req.WindowID, parts.Muted || win.UserVars.Mute != "")
	return ctx
}

func shouldNoOpForTabTitleOwner(eventOrAction string, tab *kitty.Tab, windowID int) bool {
	if isAgentSessionLoopEvent(eventOrAction) {
		return false
	}
	owner, ok := kitty.FindTabTitleOwner(tab)
	return ok && owner.ID != windowID
}

func buildHookStateUpdateRequest(
	windowID int,
	action string,
	eventOrAction string,
	payload hookevent.HookPayload,
	sessionTitle string,
	profile string,
	harnessName string,
	currentIconState string,
	pipeMode bool,
	preservesIcon bool,
	contextLimitDetected bool,
	resetFlowDetected bool,
	kittyListenOn string,
) Request {
	return Request{
		Type:             TypeStateUpdate,
		WindowID:         windowID,
		Action:           action,
		Event:            eventOrAction,
		ToolName:         payload.EffectiveToolName(),
		Source:           payload.Source,
		IsPrompt:         hookevent.IsPromptEvent(eventOrAction),
		PreservesIcon:    preservesIcon,
		PipeMode:         pipeMode,
		KittyListenOn:    strings.TrimSpace(kittyListenOn),
		SessionTitle:     sessionTitle,
		Account:          profile,
		Harness:          harnessName,
		currentIconState: currentIconState,
		NotificationType: payload.EffectiveNotificationType(),
		StopReason:       payload.EffectiveStopReason(),
		Reason:           payload.Reason,
		ContextLimit:     contextLimitDetected,
		ResetFlow:        resetFlowDetected,
	}
}

func (h *Handler) appendSuppressedHookEvent(req Request, action, suppressionReason string) {
	if req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.touchWindowMetadataLocked(req)
	h.rememberKittySocketLocked(req.WindowID, req.KittyListenOn)
	st := h.ensureWindowStateLocked(req.WindowID)
	if req.Account != "" {
		st.LastAccount = req.Account
		accountKey := strings.ToLower(req.Account)
		h.lastSeenEventAt[accountKey] = time.Now()
		delete(h.notifiedHookSilence, accountKey)
	}
	h.appendEventLocked(req.WindowID, suppressedSessionEvent(req, action, suppressionReason, desiredIconForState(st)))
	h.stateMu.Unlock()
}
