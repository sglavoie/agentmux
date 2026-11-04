// Package customization owns the session color picker, harness icon picker,
// and keybinding reference.
//
// Each picker is a standalone Bubble Tea program with a model, update, and
// view. Callers provide the operation that applies its result.
//
// It is a sibling of the root tui package rather than one of its leaves:
// cmd/dashboard launches each overlay directly, and the root does not import
// this package. It must not import the root either: the dashboard opens these
// pickers as separate programs rather than compositing them into its own view.
package customization
