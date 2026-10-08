// Package customization owns the three things a user can change about how
// Agent Mux looks in Kitty, and the overlays that let them change it: the
// color a tab is painted, the harness icon a tab title carries, and the
// keybinding reference that documents what the rest of the keys do.
//
// Each overlay is a standalone Bubble Tea program that owns its own Kitty
// overlay window, so the package holds the full model/update/view for each
// one and applies the result through the Kitty client on its context.
//
// It is a sibling of the root tui package rather than one of its leaves:
// cmd/dashboard launches each overlay directly, and the root does not import
// this package. It must not import the root either: the dashboard opens these
// overlays as separate windows rather than compositing them into its own view.
package customization
