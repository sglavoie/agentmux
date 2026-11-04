// Package rendering owns the daemon's status presentation: the icon the
// reducer's effective state paints, the title text that carries it, the
// presentation cadence, and the bounded
// render-generation traces exported as the renderer's aggregate display model.
//
// It is deliberately domain-specific, not a generic formatting utility
// package. Every identifier here describes daemon status presentation; nothing
// in it knows how to talk to tmux, how to reduce facts, or how to route
// events. It must never import the daemon root, and it performs no terminal, IPC,
// or journal I/O: the daemon root keeps ownership of the render loops and
// each checkpoint and status write, and drives this package for the decisions those loops
// present.
package rendering

import "time"

const (
	// Interval is the daemon's presentation cadence: the period of the
	// maintenance lister and the screen-probe loop, and the staleness bound
	// other subsystems reuse when they cache something for exactly one
	// presentation tick.
	Interval = 2 * time.Second
)
