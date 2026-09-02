// Package rendering owns the daemon's status presentation: the icon the
// reducer's effective state paints, the tab title text that carries it, the
// window that owns a tab's title, the latest-generation render queue that
// orders presentation work, and the bounded render-generation traces exported
// as the renderer's aggregate display model.
//
// It is deliberately not a generic formatting package. Every identifier here
// describes daemon status presentation; nothing in it knows how to talk to
// Kitty, how to reduce facts, or how to route events. The daemon root keeps
// ownership of the render loops, the Kitty writes, and the journal I/O, and
// drives this package for the decisions those loops present.
package rendering

import "time"

const (
	// Interval is the daemon's presentation cadence: the period of the
	// maintenance lister and the screen-probe loop, and the staleness bound
	// other subsystems reuse when they cache something for exactly one
	// presentation tick.
	Interval = 2 * time.Second
	// Debounce coalesces a burst of queue wakes into a single drain.
	Debounce = 50 * time.Millisecond
	// WarnAfterFailures is how many consecutive failed Kitty writes a window
	// accumulates before the renderer warns, and the period at which it
	// repeats the warning afterwards.
	WarnAfterFailures = 3
)
