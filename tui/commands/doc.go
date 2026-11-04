// Package commands owns the dashboard's command catalog and the effects it
// starts outside the Bubble Tea update loop.
//
// Command IDs, availability checks, palette rows, and per-session actions live
// here so the dashboard and overview use the same choices. The root model
// supplies closures for model changes and uses the tea.Cmd builders here for
// session refresh, daemon queries, harness actions, clipboard, editor and
// shell launches, health checks, and ASL requests. The resulting tea.Msg types
// return to the root update loop.
//
// Live sessions come from the registered tmux backend and the daemon's state
// snapshot. A refresh may report an unavailable backend while retaining the
// last usable rows. The overview uses the same read path for previews and
// navigation. Effects expose narrow seams that let tests drive them without
// contacting a live daemon or tmux server.
package commands
