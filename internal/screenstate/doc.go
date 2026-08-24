// Package screenstate answers exactly one question about a harness pane: what
// does the screen show? It never answers which icon wins.
//
// That boundary is the package's whole doctrine and it is load-bearing. The
// daemon reducer's precedence is the only arbiter of visible state: an ASL
// lease, a pipe-mode child, a subagent, an attention prompt, and a terminal
// error all outrank a probe's reading of a pane, and they do so in one place.
// A helper here that returned "so the tab should be ⚡" — or that ranked two
// observations against each other — would be a second precedence system
// competing with that one, and the two would disagree the first time either
// changed. So screenstate exports observations and evidence about screen text
// only; every caller hands what it learned to the reducer and lets the reducer
// decide.
//
// The package is deliberately harness-agnostic at its base. Normalization and
// fingerprinting depend on no harness's UI copy, which is why they keep working
// when a harness rewrites its status line — the failure mode that produced the
// bug this package was created to close. Harness-specific classification is
// layered on top as separate adapters, never folded into the base.
package screenstate
