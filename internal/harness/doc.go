// Package harness holds the per-harness adapters (Claude, Copilot, Codex).
// Each adapter owns the argv for a fresh, resumed, and forked conversation and
// the capability metadata that says which of those operations it supports, so
// callers ask the adapter rather than assembling harness command lines
// themselves.
package harness
