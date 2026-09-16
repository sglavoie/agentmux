package overview

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

type footerKey struct {
	text      string
	available bool
}

// footerKeys renders paired keys independently: PgUp can be unavailable while
// PgDn still works. Labels and separators retain the dashboard's dim style.
func footerKeys(label string, keys ...footerKey) string {
	parts := make([]string, len(keys))
	for i, key := range keys {
		style := styleFooterUnavailableKey
		if key.available {
			style = styleFooterKey
		}
		parts[i] = style.Render(key.text)
	}
	return strings.Join(parts, styleDim.Render("/")) + " " + styleDim.Render(label)
}

// renderFooter derives availability from the same navigation and attention
// rules as the key handlers. The stable order keeps controls in familiar places.
func renderFooter(in Input, g Geometry, selected int) string {
	count := len(in.Sessions)
	canMove := func(move Move) bool {
		return count > 0 && Navigate(g, count, selected, move) != selected
	}
	needsYou := false
	for _, s := range in.Sessions {
		if dashboard.NeedsYou(s, in.LastActivity, in.IdleThreshold) {
			needsYou = true
			break
		}
	}
	mode := "compact"
	if in.Compact {
		mode = "normal"
	}
	spatial := canMove(MoveLeft) || canMove(MoveRight) || canMove(MoveUp) || canMove(MoveDown)
	return strings.Join([]string{
		footerKeys("focus", footerKey{"Enter", count > 0 && !in.FocusPending}),
		footerKeys("close", footerKey{"q", true}),
		footerKeys("jump", footerKey{"1–0", count > 0 && !in.FocusPending}),
		footerKeys(mode, footerKey{"c", !in.Closing}),
		footerKeys("needs you", footerKey{"n", needsYou}, footerKey{"N", needsYou}),
		footerKeys("move", footerKey{"←↑↓→/hjkl", spatial}),
		footerKeys("next", footerKey{"Tab", canMove(MoveNext)}),
		footerKeys("page", footerKey{"PgUp", canMove(MovePageUp)}, footerKey{"PgDn", canMove(MovePageDown)}),
		footerKeys("ends", footerKey{"Home", canMove(MoveFirst)}, footerKey{"End", canMove(MoveLast)}),
	}, styleDim.Render(" · "))
}
