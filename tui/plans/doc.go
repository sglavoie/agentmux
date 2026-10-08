// Package plans owns the TUI's Plans browser: the plan listing and its sort
// order (the active sort mode and its cycle), the search result rows and the
// labels they carry, such as a plan's project name, and the plan viewer — how
// a plan's logical lines become the wrapped terminal rows the viewer scrolls
// through, the mapping from each row back to its source line, which of those
// rows are separators or search matches, and how far the viewer may scroll.
//
// The package holds vocabulary and pure projections only. It knows nothing
// about the dashboard Model, performs no plan I/O of its own, and must not
// import the root tui package: deciding what a keystroke does to the viewer's
// cursor and scroll offset stays with the model that owns them.
package plans
