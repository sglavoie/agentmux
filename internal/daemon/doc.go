// Package daemon is the orchestration root of the Agent Mux daemon: daemon
// construction and lifecycle, the sweep, probe, and render schedules,
// reconciliation coordination, and the public entry points Run,
// NewOperationalLogger, and NewHandler/Handler.
//
// It owns the one thing none of its children may: the Handler itself, and
// with it stateMu, every reducer mutation taken under it, the single renderer
// writer, and every Kitty and IPC call the daemon makes. Its children hold
// vocabulary, pure projections, and caller-locked trackers; this package
// decides what an observation does to a window. It imports all twelve daemon
// subpackages and none of them imports it back, which is what keeps the graph
// acyclic.
//
// The companion hub (companion_hub.go) follows the same off-lock rule as
// git_state.go. Publish paths ring companionDirty, a non-blocking send on a
// one-slot channel that is safe under stateMu; the hub debounces it at 100ms
// (with a 5s safety tick for clock-driven facts), takes one short copy under
// stateMu through copyStateLocked — the same copy the state query publishes —
// and projects, hashes, and fans out with no daemon lock held, skipping a
// publish whose digest is unchanged. The Handler implements
// ipc.SubscriptionHandler: a subscribe registers the sink, replays the latest
// snapshot, and is acknowledged with a 15s heartbeat the hub then sends, and
// health reports the subscribers as they identified themselves.
//
// The companion's focus_window request (handler_companion.go) is the one
// companion request that dials Kitty. It accepts only a socket the daemon
// learned itself (a kittySockets value, lastKittyListenOn, or the snapshot's
// socket), copies that allowlist under stateMu, and runs the list, verify,
// tab, and window steps through notification.FocusKittyWindow — the same
// helper click-to-focus uses — with the lock released. The
// notification_snooze request sets, clears, or reads the notifier's in-memory
// snooze and rings companionDirty on every change; a timed snooze that simply
// runs out reaches subscribers on the 5s safety tick.
//
// The hub is also the notifier's AppBackend (companion_backend.go). The role
// goes to the NEWEST subscriber that claimed notification_backend with
// notifications_authorized, and passes to the newest remaining claimant when
// that one leaves; a delivery stays bound to the subscriber it was sent to and
// ends when that subscriber does. backendMu is a leaf lock — safe under
// stateMu and hub.mu, holding nothing but a send into a buffer that cannot
// fill — and frames are written after it is released.
//
// The notification_disposition request settles a delivery idempotently: a
// repeated delivered is ignored, the first terminal disposition wins, a
// terminal one other than failed implies delivery, and an unknown
// notification ID is answered ok. notification_test offers one test notice to
// the app and never falls back; the CLI tests Alerter itself when no app
// confirmed one.
package daemon
