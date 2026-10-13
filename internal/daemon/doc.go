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
// health reports the subscribers as they identified themselves. Each publish
// projects one copy into two snapshot versions: v1, the Kitty-only contract
// every existing client reads, and v2, which a subscriber gets only by asking
// for the runtime_sessions feature and which adds runtime-session identity and
// the sessions on other backends — never with a synthesized Kitty window ID.
// Such a session has no tab to name its project and profile, so the root
// labels it (runtime_labels.go): the project from its registry record as soon
// as it claims a slot, and the profile from its pane metadata, read off
// stateMu once per binding — after the checkpoint restore, before the socket
// binds, and on maintenance for sessions that arrived since.
//
// Reducer state of runtime-session slots is checkpointed backend-neutrally
// (runtime_checkpoint.go) beside the runtime-session registry: a Kitty slot
// records only its binding, since amx_state keeps its state, while a tmux slot
// records the same amx_state payload in the file. Startup restores the
// checkpoint after the Kitty restore and before the socket binds; sessions the
// registry no longer lists live at the same target are dropped, entries it
// cannot answer for are retried on maintenance, a corrupt file is quarantined,
// and state a fresh event created first only gains disjoint facts.
//
// Nothing tells the daemon when a tmux agent stops, so on maintenance, and
// once at startup before the checkpoint is read, it has the registry
// reconciled with what the private servers hold (runtime_reconcile.go,
// internal/tmux.Reconciler): an agent whose pane is dead is recorded as
// exited with how it ended, one whose pane or server is gone as lost, an owner
// whose terminal is gone is released, and a terminal that attached beside a
// session's owner is detached. The registry stays the one authority: retiring
// a slot, dropping a session from the snapshot, and what `amx attach` answers
// all follow from it. Reconciling starts no server and launches no agent.
// A pass is one tmux client per server however many agents it holds, runs on
// every maintenance tick, and is spaced out to thirty seconds while passes
// fail, so a server that does not answer cannot hold the loop.
//
// A tmux session has no tab to render its state into, so the root renders it
// into two options on the agent's pane (runtime_status.go), which the private
// server's configuration builds the attached terminal's title and status line
// from. It is written once the checkpoint holds the state, in one tmux client
// per server for everything a pass has to write, and not at all by a pass
// with nothing new. The root is still the only writer of rendered state.
//
// What that work looks like from outside is runtime_health.go: a tally of the
// passes, the sessions and how many have a terminal attached, the screen
// reads, and the tmux clients, published through health as
// protocol.RuntimeHealth, with a daemon.runtime_reconcile journal record for
// each edge (passes start failing, they recover, a pass found an agent
// stopped). It holds counts, durations, and failure classes only, never what
// a pane or an error said. A slot of a non-Kitty backend is never queued for
// the Kitty writer: the runtime checkpoint is its durable state and is what
// ends its dirty stretch (runtime_checkpoint.go).
//
// A session on a backend other than Kitty is probed by the bodies a Kitty
// window is (runtime_screen_probes.go): the Claude context-limit banner,
// busy-title recovery, and idle screen, the Codex prompt, idle, and cleared
// screens, and the Copilot transcript probe, each of which settles a stuck
// turn or confirms a held stop as it does for a window. Its binding comes
// from the slot table, not a window tree, and its screen, the title read with
// it, and its history are read from the registered pane through
// RuntimeScreenReader and accepted only for that pane under the registered
// server generation while its agent runs. A pane that cannot be read is no
// evidence, as a failed get-text is: a held stop then waits for its deadline.
// A stop of such a session is held for confirmation only when its pane can
// confirm it (terminalStopHoldArmableLocked).
//
// Such a session has no focus to read either. What clears its unseen bit is
// the reconciliation pass finding a terminal that attached to it, or was
// used in it, no earlier than the session came to rest (observeRuntimeViewers
// in runtime_reconcile.go); a terminal that is merely still attached clears
// nothing, since the desktop workspace keeps one on every session.
//
// The companion's focus_window request (handler_companion.go) is the one
// companion request that dials Kitty. It accepts only a socket the daemon
// learned itself (a kittySockets value, lastKittyListenOn, or the snapshot's
// socket), copies that allowlist under stateMu, and runs the list, verify,
// tab, and window steps through notification.FocusKittyWindow — the same
// helper click-to-focus uses — with the lock released. A session with no
// Kitty window is shown by the focus_session request instead
// (focus_session.go), which names a runtime session: a connected workspace is
// sent a raised session view, and with none the terminal the operator used
// last is switched to it (tmux.Switcher), off stateMu. A clicked notification
// about such a session is the same request, and its target carries the
// runtime session, never a slot as a window ID. The
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
//
// The workspace_view request (workspace_view.go) is how a surface the desktop
// workspace embeds hands a launch or a focus to the workspace's native views.
// The daemon validates it — a known kind, a live session on a backend a
// workspace embeds, canonical absolute paths, never a command line — and
// forwards it as a workspace_view frame to the subscribers that named the
// workspace_views feature. With none connected it answers
// workspace_unavailable, and the surface shows the view in its own terminal.
package daemon
