// Package gitstate answers exactly one question about a directory: what does
// the checkout sitting there say about itself? It reports a branch name and a
// dirty bit as data and decides nothing — not which row is interesting, not
// whether a row should be shown, and not what any of it means.
//
// # The line against internal/worktree
//
// This is the first thing to ask about, because internal/worktree already
// produces a branch and a dirty bit. It is a different tool for a different
// job, and the two must not be confused.
//
// worktree.probeInventoryEntry is AUTHORITATIVE and EXPENSIVE. It is scoped to
// the managed worktree root, canonicalizes symlinks, refuses a checkout that
// resolves outside that root, requires Git to have the checkout registered as a
// worktree, filters unowned checkouts down to the amx/ branch prefix, and
// reconciles what it read against the sidecar metadata. It spends four or more
// git invocations per entry before it reports anything, and it accumulates
// user-visible Warnings on every failure path, because the questions it answers
// — may I remove this worktree, has this branch merged, is this record stale —
// are questions where a wrong answer costs the user work.
//
// gitstate is CHEAP, UNSCOPED, and ADVISORY. It takes any directory at all,
// asks git two questions with a short timeout, and reports what came back. It
// canonicalizes nothing, verifies no ownership, and produces no warnings: every
// failure — a directory that is not a repository, a git that is not installed,
// a probe that timed out, a detached HEAD — is reported as an empty branch and
// nothing else. That is the fail-open-and-silent contract, and it is deliberate.
// This state decorates a dashboard row that refreshes on a sweep; a row that
// grew an error message because a directory was not a repository would be
// worse than a row that simply said nothing.
//
// So: worktree for worktree management, gitstate for a per-sweep dashboard
// column. Do not reach for gitstate to decide anything destructive, and do not
// reach for worktree to paint a column.
//
// # Ahead/behind is deliberately out of scope
//
// This package does not report how far a branch has diverged from its upstream,
// and that is a decision on the record rather than an omission. Ahead/behind is
// not one extra git call: worktree's probeMergeFacts needs a rev-parse to
// verify the ref exists, a rev-list --left-right --count, and a second rev-list
// for reachability — two more failure modes and two more subprocesses per row,
// for a number that changes only when someone fetches or commits and that
// almost never decides which session needs attention right now. The branch name
// and the dirty bit carry the "which worktree am I looking at" signal that the
// dashboard actually needs. If ahead/behind is ever wanted, it belongs behind
// its own explicit opt-in with its own cost, not folded into a probe that runs
// on every sweep.
package gitstate
