package daemon

import (
	"errors"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// SetFocusClient installs the Kitty focus seam focus_window acts through. Call
// it on the startup goroutine before the server dispatches any request.
func (h *Handler) SetFocusClient(client notification.FocusClient) {
	h.focusClient = client
}

// handleFocusWindow focuses one window in one Kitty instance on behalf of a
// companion.
//
// The socket is the security boundary. A client names the instance, but the
// daemon dials it only when it is an endpoint the daemon already learned on
// its own — a per-window hook socket, the effective listen address, or the
// socket the published snapshot was read through — so a request can never
// make the daemon open an arbitrary address. Anything else is unknown_socket,
// and nothing is dialed.
//
// The allowlist is copied under stateMu; the list, verify, and both focus
// writes run after it is released, because they are Kitty subprocesses and
// every hook acknowledgement waits on stateMu. A window the listed tree no
// longer holds is stale_target, and nothing is written.
func (h *Handler) handleFocusWindow(req protocol.Request) protocol.Response {
	params := req.FocusWindow
	if params == nil || params.WindowID <= 0 {
		return protocol.Response{OK: false, Error: "focus_window requires a kitty_socket and a positive window_id"}
	}
	if h.focusClient == nil {
		return protocol.Response{OK: false, Error: "focus_window is unavailable: no Kitty client"}
	}
	if params.KittySocket == "" || !h.knownKittySocket(params.KittySocket) {
		return protocol.Response{OK: false, Error: protocol.FocusErrorUnknownSocket}
	}
	loc, err := notification.FocusKittyWindow(h.focusClient, params.KittySocket, params.WindowID)
	if errors.Is(err, notification.ErrFocusTargetStale) {
		return protocol.Response{OK: false, Error: protocol.FocusErrorStaleTarget}
	}
	if err != nil {
		slog.Debug("daemon: focus_window failed", "err", err, "window_id", params.WindowID)
		return protocol.Response{OK: false, Error: "focus_window failed: " + err.Error()}
	}
	return protocol.Response{OK: true, Focused: &protocol.FocusWindowResult{
		WindowID:   loc.WindowID,
		TabID:      loc.TabID,
		OSWindowID: loc.OSWindowID,
	}}
}

// knownKittySocket reports whether socket is a Kitty endpoint the daemon
// learned itself. It takes stateMu only to read; the caller dials off-lock.
func (h *Handler) knownKittySocket(socket string) bool {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if socket == h.lastKittyListenOn || socket == h.snapshots.View().Socket() {
		return true
	}
	for _, known := range h.kittySockets {
		if known == socket {
			return true
		}
	}
	return false
}

// handleNotificationSnooze sets, clears, or reads back the global notification
// snooze. The snooze lives in the notifier's memory only — a companion's
// request never rewrites config.toml — and every successful change rings the
// companion hub so subscribers see it without waiting for the safety tick. A
// timed snooze that simply runs out reaches subscribers on that tick instead,
// because nothing else happens at the moment it ends.
//
// An empty mode is a read: it answers the snooze in force and changes nothing.
func (h *Handler) handleNotificationSnooze(req protocol.Request) protocol.Response {
	if h.notifier == nil {
		return protocol.Response{OK: false, Error: "notification_snooze is unavailable: no notifier"}
	}
	params := req.Snooze
	if params == nil || params.Mode == "" {
		state := h.notifier.Snooze()
		return protocol.Response{OK: true, Snooze: &state}
	}
	state, err := h.notifier.SetSnooze(params.Mode, time.Unix(params.Until, 0))
	if err != nil {
		return protocol.Response{OK: false, Error: "notification_snooze: " + err.Error()}
	}
	h.companionDirty()
	return protocol.Response{OK: true, Snooze: &state}
}
