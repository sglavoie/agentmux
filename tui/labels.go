package tui

import (
	"fmt"
	"time"
)

// The helpers below are the root's shared count and timestamp labels. They
// used to live alongside the Past Sessions list; that view moved into
// tui/history, which carries its own private copies, so what remains here is
// what the plan search, the plan viewer, and the status bar still need.

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func matchPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// maxRunes floors a preview budget so a very narrow terminal still leaves room
// for an ellipsis plus a few cells of content.
func maxRunes(n int) int {
	if n < 8 {
		return 8
	}
	return n
}

// formatSessionWhen returns a short human-readable representation of a
// timestamp. Entries newer than 24h show as "Xh", older up to 7d as "Xd", and
// anything older falls back to YYYY-MM-DD.
func formatSessionWhen(t time.Time) string {
	if t.IsZero() {
		return "          "
	}
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Hour:
		mins := int(delta.Minutes())
		if mins < 1 && delta > 0 {
			mins = 1
		}
		return fmt.Sprintf("%2dm   ago", mins)
	case delta < 24*time.Hour:
		return fmt.Sprintf("%2dh   ago", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%2dd   ago", int(delta.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
