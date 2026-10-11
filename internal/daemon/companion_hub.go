package daemon

// The companion hub, seen from the daemon side.
//
// The projection itself lives in internal/daemon/companion and knows nothing
// about locks; the stream transport lives in internal/daemon/ipc and knows
// nothing about what it carries. What lives here is the part only the daemon
// root can own: noticing that something a companion shows may have changed,
// taking one coherent copy of it, and handing the result to every subscriber.
//
// THE LOCK DISCIPLINE IS THE POINT OF THIS FILE, exactly as it is in
// git_state.go. stateMu guards every reducer mutation and every hook
// acknowledgement waits on it, so the hub never projects, marshals, hashes, or
// writes while holding it. A publish is strictly three steps: signal (a
// non-blocking send on a one-slot channel, safe from any critical section),
// copy (one short critical section that clones values and nothing else), and
// build-and-fan-out (no daemon lock held). Each step is asserted by a test.

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/daemon/companion"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Hub cadence. The debounce coalesces a hook burst into one snapshot; the
// safety tick republishes facts no dirty signal announces (snapshot staleness
// and quiet hours both change with the clock alone), and is cheap because an
// unchanged digest publishes nothing. Variables so tests can shorten them.
var (
	companionDebounce   = 100 * time.Millisecond
	companionSafetyTick = 5 * time.Second
	companionHeartbeat  = 15 * time.Second
)

// companionFeatures is what a subscribe ack advertises. A client keys optional
// behavior on these names rather than on the protocol version.
var companionFeatures = []string{"snapshot", "heartbeat", "notifications"}

// companionHub holds the subscribers and the last published snapshot. Its own
// mutex orders a publication against a registration, so a new subscriber is
// replayed either the snapshot it would otherwise have missed or none at all
// — never an older one after a newer one. It is never taken while stateMu is
// held, and stateMu is never taken while it is held.
type companionHub struct {
	// dirty is the one-slot doorbell companionDirty rings.
	dirty chan struct{}

	mu    sync.Mutex
	sinks map[*ipc.StreamSink]protocol.CompanionClient
	// latest and digest hold the last published snapshot of each negotiated
	// snapshot version; a sink receives only its own version's.
	latest  map[int]json.RawMessage
	digest  map[int]string
	publish uint64

	// backendMu guards the notification backend role and the deliveries
	// awaiting a disposition (companion_backend.go). It is a leaf lock, taken
	// inside hub.mu by add and remove and never the other way round.
	backendMu  sync.Mutex
	backend    *ipc.StreamSink
	claimants  map[*ipc.StreamSink]uint64
	claimSeq   uint64
	deliveries map[string]*appDelivery

	// beforeProject runs after the copy is taken and stateMu released, before
	// the projection. Tests use it to prove the build happens off the lock.
	beforeProject func()
}

func newCompanionHub() *companionHub {
	return &companionHub{
		dirty:      make(chan struct{}, 1),
		sinks:      make(map[*ipc.StreamSink]protocol.CompanionClient),
		latest:     make(map[int]json.RawMessage),
		digest:     make(map[int]string),
		claimants:  make(map[*ipc.StreamSink]uint64),
		deliveries: make(map[string]*appDelivery),
	}
}

// companionDirty tells the hub that something a companion shows may have
// changed. It never blocks — a full doorbell already means "rebuild soon" — so
// it is safe to call with stateMu held, which is where nearly every caller is.
func (h *Handler) companionDirty() {
	if h.companion == nil {
		return
	}
	select {
	case h.companion.dirty <- struct{}{}:
	default:
	}
}

// runCompanionHub is the hub's loop: debounce dirty signals, republish on the
// safety tick, and send heartbeats. It is one of daemon.Run's owned loops.
func (h *Handler) runCompanionHub(ctx context.Context) {
	hub := h.companion
	if hub == nil {
		return
	}
	safety := time.NewTicker(companionSafetyTick)
	defer safety.Stop()
	heartbeat := time.NewTicker(companionHeartbeat)
	defer heartbeat.Stop()
	debounce := time.NewTimer(companionDebounce)
	debounce.Stop()
	defer debounce.Stop()
	armed := false

	h.publishCompanionSnapshot(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-hub.dirty:
			// Trailing debounce from the first signal: later signals inside the
			// window are already coalesced by the one-slot channel and by armed.
			if !armed {
				debounce.Reset(companionDebounce)
				armed = true
			}
		case now := <-debounce.C:
			armed = false
			h.publishCompanionSnapshot(now)
		case now := <-safety.C:
			h.publishCompanionSnapshot(now)
		case <-heartbeat.C:
			hub.heartbeat()
		}
	}
}

// copyCompanionStateLocked takes the hub's copy. Callers hold stateMu; it
// clones and does nothing else.
func (h *Handler) copyCompanionStateLocked(now time.Time) companion.Input {
	states, _ := h.copyStateLocked(now.Unix())
	view := h.snapshots.View()
	return companion.Input{
		States:  states,
		Sockets: maps.Clone(h.kittySockets),
		Kitty: companion.KittyTree{
			Ready:   view.Ready(),
			Stale:   protocol.KittySnapshotStale(view.Ready(), view.Age(now)),
			Socket:  view.Socket(),
			Windows: view.Windows(),
		},
		Others:     maps.Clone(h.otherKittyTrees),
		Runtime:    h.companionRuntimeSlotsLocked(),
		ASLTargets: h.aslSupervisors.Targets(),
		ASLPending: h.aslSupervisors.Pending(),
		Now:        now,
	}
}

// companionRuntimeSlotsLocked copies the runtime binding of every slot for the
// projection. Callers hold stateMu.
func (h *Handler) companionRuntimeSlotsLocked() map[int]companion.RuntimeSlot {
	if len(h.runtimeRoutes.bySlot) == 0 {
		return nil
	}
	out := make(map[int]companion.RuntimeSlot, len(h.runtimeRoutes.bySlot))
	for slot, b := range h.runtimeRoutes.bySlot {
		label := h.runtimeLabels[b.runtime]
		out[slot] = companion.RuntimeSlot{
			SessionID:  b.runtime,
			Backend:    b.target.backend,
			Instance:   b.target.instance,
			Generation: b.target.generation,
			Pane:       b.target.pane,
			Project:    label.project,
			Profile:    label.profile,
			Color:      h.runtimeColorLocked(label),
		}
	}
	return out
}

// companionSnapshotVersions are the snapshot shapes the hub publishes, oldest
// first.
var companionSnapshotVersions = []int{protocol.CompanionSnapshotVersion, protocol.CompanionSnapshotVersionRuntime}

// buildCompanionSnapshot builds the v1 snapshot every pre-runtime client reads.
func (h *Handler) buildCompanionSnapshot(now time.Time) protocol.CompanionSnapshot {
	return h.buildCompanionSnapshots(now)[protocol.CompanionSnapshotVersion]
}

// buildCompanionSnapshots copies under stateMu once and projects every
// snapshot version from that one copy outside it, so the versions a hub
// publishes together always describe the same moment.
func (h *Handler) buildCompanionSnapshots(now time.Time) map[int]protocol.CompanionSnapshot {
	h.stateMu.Lock()
	in := h.copyCompanionStateLocked(now)
	h.stateMu.Unlock()

	if h.companion != nil && h.companion.beforeProject != nil {
		h.companion.beforeProject()
	}
	// Everything below reads state with its own synchronization; none of it
	// takes stateMu.
	ready, degraded := h.Readiness()
	in.Daemon = protocol.CompanionDaemon{PID: os.Getpid(), Ready: ready, DegradedReason: degraded}
	if !h.startedAt.IsZero() {
		in.Daemon.StartedAt = h.startedAt.Unix()
	}
	if h.buildInfo != nil {
		build := *h.buildInfo
		in.Daemon.Build = &build
	}
	if h.notifier != nil {
		in.Notification.Backend = h.notifier.Capability().Backend
		in.Notification.QuietHours = h.notifier.QuietHoursSpec()
		in.Notification.Snooze = h.notifier.Snooze()
	}
	if h.companion != nil {
		in.Notification.AppConnected = h.companion.health().NotificationBackendConnected
	}
	out := make(map[int]protocol.CompanionSnapshot, len(companionSnapshotVersions))
	for _, version := range companionSnapshotVersions {
		in.SnapshotVersion = version
		out[version] = companion.Project(in)
	}
	return out
}

// publishCompanionSnapshot builds a snapshot and hands it to every subscriber
// unless it is byte-identical to the last one published.
func (h *Handler) publishCompanionSnapshot(now time.Time) {
	hub := h.companion
	if hub == nil {
		return
	}
	publications := make(map[int]companionPublication, len(companionSnapshotVersions))
	for version, snapshot := range h.buildCompanionSnapshots(now) {
		data, err := json.Marshal(snapshot)
		if err != nil {
			slog.Warn("daemon: companion snapshot marshal failed", "err", err, "version", version)
			return
		}
		publications[version] = companionPublication{data: data, digest: companion.Digest(snapshot)}
	}
	hub.store(publications)
}

// companionPublication is one encoded snapshot version and its digest.
type companionPublication struct {
	data   json.RawMessage
	digest string
}

// store records each version's snapshot as its latest and fans it out to that
// version's subscribers, skipping a version whose digest matches what was last
// published. publish counts moments that changed anything, not versions.
func (hub *companionHub) store(publications map[int]companionPublication) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	changed := false
	for version, p := range publications {
		if hub.latest[version] != nil && p.digest == hub.digest[version] {
			continue
		}
		changed = true
		hub.latest[version] = p.data
		hub.digest[version] = p.digest
		for sink, client := range hub.sinks {
			if sinkSnapshotVersion(client) == version {
				sink.SetSnapshot(p.data)
			}
		}
	}
	if changed {
		hub.publish++
	}
}

// sinkSnapshotVersion is the snapshot version a subscriber negotiated; a
// client recorded without one is a v1 client.
func sinkSnapshotVersion(client protocol.CompanionClient) int {
	if client.SnapshotVersion == 0 {
		return protocol.CompanionSnapshotVersion
	}
	return client.SnapshotVersion
}

// heartbeat sends one heartbeat frame to every subscriber. A full queue ends
// that subscriber inside Send, and its Done watcher removes it.
func (hub *companionHub) heartbeat() {
	hub.mu.Lock()
	sinks := make([]*ipc.StreamSink, 0, len(hub.sinks))
	for sink := range hub.sinks {
		sinks = append(sinks, sink)
	}
	hub.mu.Unlock()
	for _, sink := range sinks {
		sink.Send(protocol.FrameHeartbeat, nil)
	}
}

// add registers sink and replays the latest snapshot to it. A subscriber that
// is a notification backend takes the backend role.
func (hub *companionHub) add(sink *ipc.StreamSink, client protocol.CompanionClient) {
	hub.mu.Lock()
	hub.sinks[sink] = client
	if latest := hub.latest[sinkSnapshotVersion(client)]; latest != nil {
		sink.SetSnapshot(latest)
	}
	if client.NotificationBackend {
		hub.claimBackend(sink)
	}
	hub.mu.Unlock()
}

// remove forgets sink, and with it any backend role and deliveries it held.
func (hub *companionHub) remove(sink *ipc.StreamSink) {
	hub.mu.Lock()
	delete(hub.sinks, sink)
	hub.releaseBackend(sink)
	hub.mu.Unlock()
}

// health reports the connected subscribers in subscriber-ID order.
func (hub *companionHub) health() protocol.CompanionHealth {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	out := protocol.CompanionHealth{Subscribers: len(hub.sinks)}
	for _, client := range hub.sinks {
		if client.NotificationBackend {
			out.NotificationBackendConnected = true
		}
		if client.AMXBuild != nil {
			build := *client.AMXBuild
			client.AMXBuild = &build
		}
		out.Clients = append(out.Clients, client)
	}
	sort.Slice(out.Clients, func(i, j int) bool { return out.Clients[i].SubscriberID < out.Clients[j].SubscriberID })
	return out
}

// latestSnapshot returns the last published v1 snapshot bytes, for tests.
func (hub *companionHub) latestSnapshot() json.RawMessage {
	return hub.latestSnapshotVersion(protocol.CompanionSnapshotVersion)
}

// latestSnapshotVersion returns the last published snapshot bytes of version.
func (hub *companionHub) latestSnapshotVersion(version int) json.RawMessage {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.latest[version]
}

// Subscribe implements ipc.SubscriptionHandler. It registers the sink, replays
// the latest snapshot, and acknowledges. It takes no daemon lock and never
// blocks: the sink's lifetime is watched from its own goroutine.
func (h *Handler) Subscribe(req protocol.Request, sink *ipc.StreamSink) protocol.Response {
	hub := h.companion
	if hub == nil {
		return protocol.Response{OK: false, Error: "unknown request type: " + string(req.Type)}
	}
	var sub protocol.SubscribeRequest
	if req.Subscribe != nil {
		sub = *req.Subscribe
	}
	negotiated := protocol.CompanionProtocolVersion
	if sub.Protocol > 0 && sub.Protocol < negotiated {
		negotiated = sub.Protocol
	}
	// The mixed-backend snapshot is opt-in: a client that did not ask for it
	// was written against v1 and keeps receiving v1, Kitty sessions only.
	snapshotVersion := protocol.CompanionSnapshotVersion
	features := append([]string(nil), companionFeatures...)
	if slices.Contains(sub.Features, protocol.CompanionFeatureRuntimeSessions) {
		snapshotVersion = protocol.CompanionSnapshotVersionRuntime
		features = append(features, protocol.CompanionFeatureRuntimeSessions)
	}
	// Workspace view requests go only to a client that said it hosts them.
	workspaceViews := slices.Contains(sub.Features, protocol.CompanionFeatureWorkspaceViews)
	if workspaceViews {
		features = append(features, protocol.CompanionFeatureWorkspaceViews)
	}
	client := protocol.CompanionClient{
		SubscriberID:  sink.ID(),
		Client:        clampClientField(sub.Client),
		ClientVersion: clampClientField(sub.ClientVersion),
		// A claim without operating-system authorization is not a backend.
		NotificationBackend: sub.NotificationBackend && sub.NotificationsAuthorized,
		ConnectedAt:         time.Now().Unix(),
		SnapshotVersion:     snapshotVersion,
		WorkspaceViews:      workspaceViews,
	}
	if sub.AMXBuild != nil {
		build := *sub.AMXBuild
		client.AMXBuild = &build
	}
	hub.add(sink, client)
	go func() {
		<-sink.Done()
		hub.remove(sink)
		if client.NotificationBackend {
			h.companionDirty()
		}
	}()
	if client.NotificationBackend {
		h.companionDirty()
	}
	return protocol.Response{OK: true, Subscription: &protocol.SubscriptionAck{
		Protocol:          negotiated,
		ServerProtocolMax: protocol.CompanionProtocolVersion,
		SubscriberID:      sink.ID(),
		HeartbeatMS:       companionHeartbeat.Milliseconds(),
		Features:          features,
	}}
}

// clampClientField bounds a self-reported client string: it is shown in
// health and doctor and must not let a subscriber inflate either.
func clampClientField(s string) string {
	const limit = 128
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// companionHealth is the TypeHealth companion block.
func (h *Handler) companionHealth() *protocol.CompanionHealth {
	if h.companion == nil {
		return nil
	}
	out := h.companion.health()
	return &out
}

var _ ipc.SubscriptionHandler = (*Handler)(nil)
