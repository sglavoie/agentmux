// Package history owns the TUI's session-history domain: everything that
// answers "what already happened" rather than "what is running now". The two
// histories that exist here are the per-session event history — the detail
// overlay, its row and footer chrome, and the phase timeline sub-view with its
// grouping, folding, and phase navigation — and Past Sessions transcript
// history: the search results list and its row formatting, the transcript
// viewer with its match scanning and cursor-tracking scroll, the full-message
// overlay, and the transcript delete prompt. There is no archive/restore flow.
//
// It is a domain leaf. It holds no Bubble Tea model and no program state of its
// own: DetailInput, SearchInput, ViewerInput, and DeleteInput each carry the
// facts one view needs, so the root package keeps ownership of the Model and
// projects it onto these values at the seam. The package must not import the
// root tui package.
package history
