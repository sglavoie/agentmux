// Package activity decides whether a window is working or resting, and holds
// the evidence that settles the question: the legacy action guard, the
// stabilization-sample and screen-decline trackers with their
// per-(harness, tier) sample policy, the Claude title and Codex prompt
// classifiers, the per-sweep screen-text cache, authoritative-idle naming and
// probe eligibility, foreground harness classification, and the stale and
// hook-silence thresholds.
//
// The sample and decline trackers are deliberately CALLER-LOCKED and
// zero-value ready: the daemon mutates a streak in the same critical section
// as the reducer state it is about to settle, so an internal mutex would hide
// an atomicity requirement rather than satisfy it.
//
// It must never import the daemon root, and it performs no Kitty, IPC, or
// journal I/O. Deciding what an idle observation DOES to a window stays in the
// root, which owns the reducer, the renderer, and the notifier.
package activity
