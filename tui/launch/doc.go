// Package launch owns the TUI's launch workflow: the launcher project list
// and its preview pane, the launcher's filter/profile-cycle/pin controls, the
// profile-override overlay that decides which profile and harness a session
// starts under, the persisted launcher preferences, the ConfigPickerPurpose
// vocabulary that says why that overlay is open, and the launch batch's target
// selection and progress wording.
//
// The leaf reads its facts from explicit input values rather than from the
// root Bubble Tea model, so it never imports the root tui package. Deciding
// what a keypress DOES — mutating model state, dispatching tea.Cmds, driving
// the toast list — stays in the root, which owns the program model and keeps
// the tea.Cmd builders that actually launch.
package launch
