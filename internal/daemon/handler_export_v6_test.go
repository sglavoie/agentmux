package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
)

// TestHealthCarriesBuildIdentityAndMetrics verifies TypeHealth reports the
// artifact identity stamped at startup and the server's request metrics.
func TestHealthCarriesBuildIdentityAndMetrics(t *testing.T) {
	h := NewHandler(NewStateGuard(time.Second), nil)
	build := buildinfo.Info{Version: "9.9.9", Revision: "abc", BinaryHash: "hash-x"}
	h.SetBuildInfo(build)
	h.setMetricsSource(func() MetricsSnapshot {
		return MetricsSnapshot{Accepted: 5, QueueFull: 1, QueueLastWaitNs: 250}
	})

	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.OK {
		t.Fatalf("health response not OK: %+v", resp)
	}
	if resp.Build == nil || resp.Build.BinaryHash != "hash-x" || resp.Build.Version != "9.9.9" {
		t.Fatalf("resp.Build = %+v; want the stamped identity", resp.Build)
	}
	if resp.Metrics == nil || resp.Metrics.Accepted != 5 || resp.Metrics.QueueFull != 1 || resp.Metrics.QueueLastWaitNs != 250 {
		t.Fatalf("resp.Metrics = %+v; want the server snapshot", resp.Metrics)
	}
}

func TestHealthCarriesNotificationCapabilityWithoutDegradingReadiness(t *testing.T) {
	n := NewNotifier(&config.NotificationConfig{Stop: true})
	n.capability = NotificationCapability{
		Enabled: true, Backend: "alerter", Reason: "not_found",
	}
	h := NewHandler(NewStateGuard(time.Second), n)
	h.MarkReady()

	resp := h.Handle(Request{Type: TypeHealth})
	if !resp.Ready {
		t.Fatal("missing Alerter must not degrade daemon readiness")
	}
	if resp.Notification == nil || !resp.Notification.Enabled || resp.Notification.Available || resp.Notification.Reason != "not_found" {
		t.Fatalf("notification capability = %+v", resp.Notification)
	}
}

// TestHealthWithoutBuildOrMetricsOmitsThem covers handlers running without a
// server or startup stamp (unit-test topology, legacy daemons).
func TestHealthWithoutBuildOrMetricsOmitsThem(t *testing.T) {
	h := NewHandler(NewStateGuard(time.Second), nil)
	resp := h.Handle(Request{Type: TypeHealth})
	if resp.Build != nil || resp.Metrics != nil {
		t.Fatalf("resp = build %+v metrics %+v; want both nil", resp.Build, resp.Metrics)
	}
}

// TestEventsExportStampsV6EventAndFactFields verifies the schema-v6 receipt
// stamping (daemon sequence, nanosecond receipt) and identity/route stamping
// on logged events, plus the live source facts on the shadow export section.
func TestEventsExportStampsV6EventAndFactFields(t *testing.T) {
	h := NewHandler(NewStateGuard(time.Second), nil)

	req := Request{
		Type:     TypeStateUpdate,
		WindowID: 5,
		Action:   "active",
		Event:    "PreToolUse",
	}
	req.normalized = &NormalizedEvent{
		Source: SourceID{Kind: SourceHarness, Key: "sess-1"},
		Action: "active",
		Event:  "PreToolUse",
		Seq:    9,
	}
	req.routeDecision = routeDecisionSource
	before := time.Now().UnixNano()
	if resp := h.Handle(req); !resp.OK {
		t.Fatalf("state update failed: %+v", resp)
	}

	stop := req
	stop.Action = "stop"
	stop.Event = "Stop"
	stop.normalized = &NormalizedEvent{
		Source: SourceID{Kind: SourceHarness, Key: "sess-1"},
		Action: "stop",
		Event:  "Stop",
		Seq:    10,
	}
	stop.routeDecision = routeDecisionOwner
	if resp := h.Handle(stop); !resp.OK {
		t.Fatalf("stop update failed: %+v", resp)
	}

	export := h.Handle(Request{Type: TypeEventsExport, WindowID: 5})
	events := export.EventsByWindow[5]
	if len(events) != 2 {
		t.Fatalf("len(events) = %d; want 2 (%+v)", len(events), events)
	}
	first, second := events[0], events[1]
	if first.DaemonSeq == 0 || second.DaemonSeq <= first.DaemonSeq {
		t.Fatalf("daemon sequence not monotonic: %d then %d", first.DaemonSeq, second.DaemonSeq)
	}
	if first.ReceivedAtNs < before {
		t.Fatalf("ReceivedAtNs = %d; want >= %d", first.ReceivedAtNs, before)
	}
	if first.SourceKind != string(SourceHarness) || first.SourceKey != "sess-1" || first.SourceSeq != 9 {
		t.Fatalf("first event identity = %q/%q/%d; want harness/sess-1/9", first.SourceKind, first.SourceKey, first.SourceSeq)
	}
	if first.RouteDecision != routeDecisionSource || second.RouteDecision != routeDecisionOwner {
		t.Fatalf("route decisions = %q, %q; want source, owner", first.RouteDecision, second.RouteDecision)
	}

	meta, ok := export.WindowMetadata[5]
	if !ok {
		t.Fatal("window 5 missing from export metadata")
	}
	if len(meta.Shadow.Facts) == 0 {
		t.Fatalf("shadow facts missing from export: %+v", meta.Shadow)
	}
	fact := meta.Shadow.Facts[0]
	if fact.Kind != SourceHarness || fact.Key != "sess-1" || fact.Action != "stop" || fact.Seq != 10 {
		t.Fatalf("exported fact = %+v; want the harness stop fact at seq 10", fact)
	}
}

// TestMetricsObserveQueueWait covers the queue-latency counters directly.
func TestMetricsObserveQueueWait(t *testing.T) {
	var m Metrics
	m.observeQueueWait(5 * time.Millisecond)
	m.observeQueueWait(2 * time.Millisecond)
	snap := m.Snapshot()
	if snap.QueueLastWaitNs != (2 * time.Millisecond).Nanoseconds() {
		t.Fatalf("QueueLastWaitNs = %d; want the most recent observation", snap.QueueLastWaitNs)
	}
	if snap.QueueMaxWaitNs != (5 * time.Millisecond).Nanoseconds() {
		t.Fatalf("QueueMaxWaitNs = %d; want the lifetime maximum", snap.QueueMaxWaitNs)
	}
}
