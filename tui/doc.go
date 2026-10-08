// Package tui is the Bubble Tea program behind the live-session dashboard: the
// Model, the mode dispatcher, every key handler, the update loop that consumes
// whatever an effect reports back, the styles it builds from tui/theme, and the
// deterministic in-process uitest harness that cmd/tuitest/tuitest.go drives.
//
// Domain leaves under tui/ own the vocabulary the model renders — terminal
// geometry in tui/layout, the palette in tui/theme, session rows, the filter
// DSL, and the inspector in tui/dashboard, event and transcript history in
// tui/history, the launcher in tui/launch, plan rows in tui/plans, worktree
// rows in tui/worktrees, the ASL Runs surface in tui/asl, command and
// key-action definitions with the effect layer in tui/commands, and the
// session-action declarations in tui/sessionactions. This is the only package
// in the TUI tree that names Model: the leaves take explicit Input values and
// return strings, so vocabulary and pure rendering move out while what a
// keypress DOES to the model stays in. No leaf imports this package. Each leaf
// has one projection seam file here that adapts the model onto it —
// dashboard_view.go, history_view.go, launch_view.go, plans_view.go,
// worktrees_view.go, asl_view.go, and command_palette.go, which carries the
// palette's controller and its projection seam in one file.
//
// tui/customization and tui/overview are siblings rather than leaves: cmd
// launches the color, icon, and keybinding overlays and the live session
// overview as their own programs, and this package imports neither.
//
// Command and message production is divided rather than owned outright. The
// effects the dashboard starts away from the update loop — the live-topology
// refresh, daemon queries, harness lifecycle, clipboard, directory and editor
// launches, health probes, agent-session-loop discovery — live in tui/commands
// (session.go and its siblings) together with the shared result messages they
// report back with (message.go). This package keeps the command and message
// pairs that close over its own model state: the transcript and plan cache
// loads and debounced searches, worktree loads and mutations, the ASL
// snapshot/question/action round trips, the preview and toast tickers, and
// launch progress. Visibility is the line to read: every message type in
// tui/commands is exported because it crosses back into this package's update
// loop, and every message type declared here is unexported because it never
// leaves the loop that raised it.
//
// The uitest files cannot become a child package because cmd reaches them
// through this one.
//
// The whole TUI tree — this package included — depends on
// internal/daemon/ipc, internal/daemon/process, and internal/daemon/protocol
// only; internal/daemon itself is not in its transitive imports at all.
package tui
