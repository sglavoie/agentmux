// Package worktrees owns the TUI's managed-worktree selection and display
// domain: the one-line row a worktree renders as, the state classification and
// the badges that say what kind of worktree it is, the compact age its
// creation time reads as, the source label, the filter query that decides
// whether it stays on screen, the stable ordering the list is kept in, and the
// sentences a completed durable mutation is summarized with.
//
// The package holds worktree vocabulary and pure projections only. It knows
// nothing about the dashboard Model, performs no Git or filesystem work of
// its own, and must not import the root tui package: deciding what a
// keystroke does to the worktree cursor, and running the actions it dispatches,
// stays with the model that owns them.
package worktrees
