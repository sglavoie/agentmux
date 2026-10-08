// Package dashboard owns the live-session list: what a running session looks
// like as a row, which rows the active filter keeps, which state group each
// row belongs to and the state gloss that names it, the group borders, how
// many rows fit the current viewport and how the list paginates, where the
// cursor lands after the list is rebuilt by a refresh, and how the resulting
// list, its removable filter chips, and the filter DSL's completion menu
// paint.
//
// It also owns the session inspector: its fixed metadata block, its five
// output-content states, its family-card form shown when the selection is a
// family summary rather than a session, and the pure output viewport that
// holds a snapshot while the operator is scrolled back. The compact tile
// header the live session overview draws is the inspector's small sibling and
// lives here too.
//
// Profile and harness color assignment lives here as well, because it is read
// by more than this package: tui/history, tui/launch, and the root tui package
// all color by it.
//
// It is a domain leaf. It holds no Bubble Tea model and no program state of
// its own: every entry point takes the facts it needs explicitly, so the root
// package keeps ownership of the Model and adapts it at the call site. The
// package must not import the root tui package.
package dashboard
