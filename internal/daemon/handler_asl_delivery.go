package daemon

// Lifecycle-hook delivery observability.
//
// The bridge tells an operator whether the daemon can reach ASL. It says
// nothing about the other direction, which is the one that actually carries
// state: lifecycle hooks arrive on stdin from processes the daemon never
// spawned, and the only correct response to one it cannot place is to
// quarantine it and answer OK. That silence is deliberate — a hook producer
// must never be blocked on the daemon's opinion — but it means a pairing that
// is quietly refusing every event looks exactly like an idle one.
//
// These counters are what separate the two. They are delivery facts only: how
// many lifecycle payloads were believed, how many were refused and under which
// daemon-authored reason, and when each last happened. Nothing a payload chose
// is recorded here — not its offered version, not its contract identity, not a
// plan path — because a quarantined payload is precisely one whose self-
// description the daemon has declined to believe.

import "time"

// aslHookDeliveryMetrics accumulates lifecycle-hook ingestion outcomes. Guarded
// by stateMu.
type aslHookDeliveryMetrics struct {
	accepted       uint64
	lastAcceptedAt time.Time

	quarantined       uint64
	byReason          map[string]uint64
	lastQuarantinedAt time.Time
	// lastReason is the most recent refusal reason. It is a daemon constant
	// from the isQuarantineReason set, never producer-supplied text.
	lastReason string
}

// ASLHookDeliverySnapshot is the health-response view of hook ingestion.
type ASLHookDeliverySnapshot struct {
	Accepted    uint64 `json:"accepted"`
	Quarantined uint64 `json:"quarantined"`
	// QuarantinedByReason is keyed by the stable refusal reasons. It is omitted
	// when empty rather than published as an empty object, so a consumer can
	// tell "no refusals" from "refusals this build does not classify".
	QuarantinedByReason map[string]uint64 `json:"quarantined_by_reason,omitempty"`

	// AcceptedAgeSeconds and QuarantinedAgeSeconds are ages rather than
	// timestamps, computed at read time. A daemon that stopped ingesting also
	// stopped writing, so the useful signal is the one a stalled writer could
	// not have produced.
	AcceptedAgeSeconds    *float64 `json:"accepted_age_seconds,omitempty"`
	QuarantinedAgeSeconds *float64 `json:"quarantined_age_seconds,omitempty"`

	LastQuarantineReason string `json:"last_quarantine_reason,omitempty"`
}

// recordASLHookAccepted counts one lifecycle payload the daemon believed and
// acted on.
func (h *Handler) recordASLHookAccepted(now time.Time) {
	h.stateMu.Lock()
	h.aslDelivery.accepted++
	h.aslDelivery.lastAcceptedAt = now
	h.stateMu.Unlock()
}

// recordASLHookQuarantined counts one lifecycle payload refused as unusable.
// reason is a daemon-authored constant; anything else is dropped rather than
// keyed, so the reason map cannot be grown by a producer.
func (h *Handler) recordASLHookQuarantined(now time.Time, reason string) {
	if !isQuarantineReason(reason) {
		return
	}
	h.stateMu.Lock()
	h.aslDelivery.quarantined++
	h.aslDelivery.lastQuarantinedAt = now
	h.aslDelivery.lastReason = reason
	if h.aslDelivery.byReason == nil {
		h.aslDelivery.byReason = make(map[string]uint64, 3)
	}
	h.aslDelivery.byReason[reason]++
	h.stateMu.Unlock()
}

// ASLHookDeliveryMetrics returns the current ingestion snapshot.
func (h *Handler) ASLHookDeliveryMetrics() ASLHookDeliverySnapshot {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	snapshot := ASLHookDeliverySnapshot{
		Accepted:             h.aslDelivery.accepted,
		Quarantined:          h.aslDelivery.quarantined,
		LastQuarantineReason: h.aslDelivery.lastReason,
	}
	if len(h.aslDelivery.byReason) > 0 {
		snapshot.QuarantinedByReason = make(map[string]uint64, len(h.aslDelivery.byReason))
		for reason, count := range h.aslDelivery.byReason {
			snapshot.QuarantinedByReason[reason] = count
		}
	}
	now := time.Now()
	if !h.aslDelivery.lastAcceptedAt.IsZero() {
		age := now.Sub(h.aslDelivery.lastAcceptedAt).Seconds()
		snapshot.AcceptedAgeSeconds = &age
	}
	if !h.aslDelivery.lastQuarantinedAt.IsZero() {
		age := now.Sub(h.aslDelivery.lastQuarantinedAt).Seconds()
		snapshot.QuarantinedAgeSeconds = &age
	}
	return snapshot
}
