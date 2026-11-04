package daemon

import (
	"context"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

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

// handleNotificationDisposition records what became of a notification the
// companion was asked to show. It is idempotent by construction — see
// companionHub.settleDisposition — so an app may resend a report it is unsure
// was received. Only a malformed request is refused; an unknown notification
// ID is answered ok, because the delivery it names has already been settled
// or forgotten and the app has nothing to do about that.
func (h *Handler) handleNotificationDisposition(req protocol.Request) protocol.Response {
	params := req.Disposition
	if params == nil || params.NotificationID == "" || params.Disposition == "" {
		return protocol.Response{OK: false, Error: "notification_disposition requires a notification_id and a disposition"}
	}
	if h.companion == nil {
		return protocol.Response{OK: true}
	}
	if err := h.companion.settleDisposition(params.NotificationID, params.Disposition, clampClientField(params.Error)); err != nil {
		return protocol.Response{OK: false, Error: "notification_disposition: " + err.Error()}
	}
	return protocol.Response{OK: true}
}

// handleNotificationTest offers one test notification to the companion app
// that holds the backend role and reports whether it confirmed delivery. It
// never falls back to Alerter: `amx notifications test` runs that test from
// its own process when no app confirmed one, so the daemon is not asked to
// spawn a notification subprocess on a client's behalf.
func (h *Handler) handleNotificationTest() protocol.Response {
	if h.notifier == nil {
		return protocol.Response{OK: false, Error: "notification_test is unavailable: no notifier"}
	}
	result := h.notifier.TestAppDelivery(context.Background())
	return protocol.Response{OK: true, NotificationTest: &result}
}
