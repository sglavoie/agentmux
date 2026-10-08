// Package commands owns the TUI's command surface: what the dashboard can be
// asked to do, whether it may be done right now, and how the doing is carried
// out away from the Bubble Tea update loop.
//
// Three things live here. First, the command and key-action vocabulary —
// stable command IDs and groups, the captured target context, the availability
// verdict with its inline reasons, and the single guarded entry point that
// refuses a hidden or unavailable command before it can execute. Second, the
// command palette and the per-row actions overlay: query matching, result
// ordering, display-row layout, scroll geometry, and the painting of both.
// Third, the effect layer the root hands off to — the model-free command
// builders for every tea.Cmd the dashboard starts outside the update loop
// (the live-topology refresh, Kitty enumeration, daemon queries, harness
// lifecycle, clipboard, editor and shell launches, health probes,
// agent-session-loop discovery, URL opening; session.go and its siblings) and
// every tea.Msg those effects report back with (message.go). Those message
// types are exported because they cross back into the root's update loop.
//
// The package is parameterized over the root program model rather than
// importing it: Command[M] and Action[M] carry the root's own closures, so the
// palette can ask a live model whether a command is visible and available
// without this package ever knowing what a model is. Availability is
// re-derived at render time, not frozen when the palette opens. Deciding what a
// command DOES — mutating model fields, switching modes, pushing toasts, wiring
// the update loop — stays in the root package, which owns the model and
// supplies every catalog entry's closure.
//
// It also owns the dashboard refresh command cycle and the package-level seams
// root tests swap: Registry, DaemonRequest, StateQuery, SpawnDaemonDetached,
// CopyToClipboard, and ActiveLoopDiscover.
//
// The live topology is daemon-first. A refresh takes the window tree from the
// daemon's published snapshot whenever that snapshot is present, ready, and
// attributed to the same Kitty instance the dashboard runs inside — including
// when the daemon has marked it stale — and lists Kitty itself only when the
// daemon is unreachable, rejects the query, has never completed a list,
// predates the snapshot block, or supplies a tree whose instance identity is
// missing on either side or known to differ. That same instance proof gates
// the daemon's per-window state, activity, and watchdog maps, which are keyed
// by bare window IDs that name a window only inside the instance they came
// from: a refresh that cannot prove the instance renders the state and loop
// markers parsed from live Kitty titles instead, exactly as it does with no
// daemon at all. A stale tree is still rendered but drives no Kitty write,
// because the window IDs in it may already be gone.
//
// It also owns the read-only retrieval boundary. RefreshOverview is Refresh
// minus its write budget, and the two share the unexported readLiveSessions so
// the shape of the topology cannot drift between them. The distinction is not
// an optimization. Refresh is allowed to spend a bounded number of Kitty writes
// reconciling what it found — including consuming the one-time
// amx_dashboard_select request (kitty.DashboardSelectionVar) — and only the
// dashboard may do that, because that request names a selection exactly once
// and a second surface reading it would swallow it. RefreshOverview therefore
// performs no Kitty write of any kind, and the guarantee is tested from both
// sides: the overview's recording client records a violation for every write
// method, and a dedicated test asserts that an overview retrieval leaves a
// pending selection request intact for the dashboard to consume afterwards.
package commands
