package daemon

// The companion hub as the notifier's app backend.
//
// The notifier decides whether a notification goes to the app and records
// every disposition (notification/app_backend.go); this file is the transport
// half: which subscriber holds the backend role, the notification and
// notification_retract frames, and matching the app's notification_disposition
// requests back to the delivery waiting on them.
//
// The role goes to the NEWEST subscriber that claimed notification_backend
// with notifications_authorized, so an app relaunched while its predecessor's
// connection is still draining takes over at once; when the holder leaves, the
// newest remaining claimant inherits it. A delivery stays bound to the
// subscriber it was sent to: when that subscriber leaves, the delivery ends
// with notification.AppGone rather than moving, because the new holder never
// showed it.
//
// backendMu guards the role and the deliveries. It is a leaf lock: nothing is
// taken while it is held except a channel send into a buffer sized so the send
// can never block. It may therefore be taken with stateMu or hub.mu held —
// Retract runs under stateMu on the process-exit and Codex clear paths, and
// add/remove run under hub.mu — and frames are written only after it is
// released.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// appDelivery is one notice the backend subscriber was sent and has not
// settled. interactions has room for the one delivered and the one terminal
// value it can ever receive.
type appDelivery struct {
	sink         *ipc.StreamSink
	interactions chan notification.AppInteraction
	delivered    bool
}

// claimBackend records sink as a backend claimant and gives it the role.
func (hub *companionHub) claimBackend(sink *ipc.StreamSink) {
	hub.backendMu.Lock()
	defer hub.backendMu.Unlock()
	hub.claimSeq++
	hub.claimants[sink] = hub.claimSeq
	hub.backend = sink
}

// releaseBackend forgets sink as a claimant, hands the role to the newest
// remaining one, and ends every delivery that was bound to sink.
func (hub *companionHub) releaseBackend(sink *ipc.StreamSink) {
	hub.backendMu.Lock()
	defer hub.backendMu.Unlock()
	delete(hub.claimants, sink)
	if hub.backend == sink {
		hub.backend = nil
		var newest uint64
		for candidate, seq := range hub.claimants {
			if seq > newest {
				newest, hub.backend = seq, candidate
			}
		}
	}
	for id, d := range hub.deliveries {
		if d.sink == sink {
			delete(hub.deliveries, id)
			d.interactions <- notification.AppInteraction{Disposition: notification.AppGone}
		}
	}
}

// Ready implements notification.AppBackend.
func (hub *companionHub) Ready() bool {
	hub.backendMu.Lock()
	defer hub.backendMu.Unlock()
	return hub.backend != nil
}

// Deliver implements notification.AppBackend. The delivery is registered
// before its frame is written, so a disposition that races the frame is still
// matched.
func (hub *companionHub) Deliver(ctx context.Context, notice notification.AppNotice) (<-chan notification.AppInteraction, error) {
	data, err := json.Marshal(notice)
	if err != nil {
		return nil, fmt.Errorf("encode notification frame: %w", err)
	}
	hub.backendMu.Lock()
	sink := hub.backend
	if sink == nil {
		hub.backendMu.Unlock()
		return nil, notification.ErrAppBackendUnavailable
	}
	d := &appDelivery{sink: sink, interactions: make(chan notification.AppInteraction, 2)}
	hub.deliveries[notice.NotificationID] = d
	hub.backendMu.Unlock()

	if !sink.Send(protocol.FrameNotification, data) {
		hub.forgetDelivery(notice.NotificationID)
		return nil, notification.ErrAppBackendUnavailable
	}
	go func() {
		<-ctx.Done()
		hub.forgetDelivery(notice.NotificationID)
	}()
	return d.interactions, nil
}

// forgetDelivery drops a delivery nobody is waiting on any more.
func (hub *companionHub) forgetDelivery(id string) {
	hub.backendMu.Lock()
	delete(hub.deliveries, id)
	hub.backendMu.Unlock()
}

// Retract implements notification.AppBackend.
func (hub *companionHub) Retract(id, reason string) {
	hub.backendMu.Lock()
	d, ok := hub.deliveries[id]
	if ok {
		delete(hub.deliveries, id)
		d.interactions <- notification.AppInteraction{Disposition: notification.AppRetracted}
	}
	hub.backendMu.Unlock()
	if !ok {
		return
	}
	data, err := json.Marshal(protocol.NotificationRetract{NotificationID: id, Reason: reason})
	if err != nil {
		return
	}
	d.sink.Send(protocol.FrameNotificationRetract, data)
}

// errUnknownDisposition refuses a disposition outside the protocol vocabulary.
var errUnknownDisposition = fmt.Errorf("unknown disposition (want one of %v)", protocol.CompanionDispositions)

// settleDisposition applies one notification_disposition report. It is
// idempotent: a repeated delivered is ignored, the first terminal disposition
// settles the delivery and forgets it, and anything reported about a
// notification the daemon is not waiting on — already settled, retracted,
// timed out, or from before a restart — is accepted and changes nothing,
// because the app cannot tell those apart and retrying would not help it.
//
// A terminal disposition other than failed implies delivery: an app may click
// through a banner before its delivered report lands, and the click must still
// count as the app having shown it.
func (hub *companionHub) settleDisposition(id, disposition, errText string) error {
	if !slices.Contains(protocol.CompanionDispositions, disposition) {
		return errUnknownDisposition
	}
	hub.backendMu.Lock()
	defer hub.backendMu.Unlock()
	d, ok := hub.deliveries[id]
	if !ok {
		return nil
	}
	if disposition == protocol.DispositionDelivered {
		if !d.delivered {
			d.delivered = true
			d.interactions <- notification.AppInteraction{Disposition: disposition}
		}
		return nil
	}
	if !d.delivered && disposition != protocol.DispositionFailed {
		d.delivered = true
		d.interactions <- notification.AppInteraction{Disposition: protocol.DispositionDelivered}
	}
	delete(hub.deliveries, id)
	d.interactions <- notification.AppInteraction{Disposition: disposition, Error: errText}
	return nil
}

// pendingDeliveries reports how many deliveries are unsettled, for tests.
func (hub *companionHub) pendingDeliveries() int {
	hub.backendMu.Lock()
	defer hub.backendMu.Unlock()
	return len(hub.deliveries)
}

var _ notification.AppBackend = (*companionHub)(nil)
