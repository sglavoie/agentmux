package daemon

import (
	"strings"
	"unicode"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// This file holds the Claude pane-TITLE helpers. Claude SCREEN classification
// now lives in internal/screenstate, next to the normalization and fingerprint
// primitives it shares a failure mode with; only the thin adapter lookup below
// remains on this side.
//
// The split is along the axis that matters: a screen scrape is authoritative
// evidence about a running turn, while a pane title is stale-tolerant hearsay
// Claude does not reliably revert. Only the second kind stays here, because it
// is consumed exclusively by recoverClaudeBusyTitle's one-way promotion.

// classifyClaudeScreen resolves the Claude adapter and classifies one capture.
// A missing adapter is impossible in practice — the registry is a package-level
// literal — but the fail-closed zero Observation is the right answer anyway,
// and it is never terminal.
func classifyClaudeScreen(screen string) screenstate.Observation {
	adapter, ok := screenstate.For(harness.Claude)
	if !ok {
		return screenstate.Observation{}
	}
	return adapter.Classify(screen)
}

// claudeRestingTitleText is the generic body Claude Code publishes in its pane
// title when no turn is running.
const claudeRestingTitleText = "Claude Code"

// claudeTitleMarkerBody splits a Claude pane title into its work-marker rune and
// trimmed body, reporting whether a recognized marker is present. Claude cycles
// its star, its Braille spinner block, and its quadrant spinner block through
// this position.
func claudeTitleMarkerBody(title string) (string, bool) {
	runes := []rune(strings.TrimLeftFunc(title, unicode.IsSpace))
	if len(runes) < 2 || !unicode.IsSpace(runes[1]) {
		return "", false
	}
	if !claudeTitleMarkerRune(runes[0]) {
		return "", false
	}
	return strings.TrimSpace(string(runes[2:])), true
}

func claudeTitleMarkerRune(r rune) bool {
	switch {
	case r == '\u2733':
		return true
	case r >= '\u2800' && r <= '\u28ff':
		return true
	case r >= '\u25d0' && r <= '\u25d3':
		return true
	}
	return false
}

// claudeTitleHasMarker reports whether Claude's pane title carries a work marker
// at all, over a description body or over the generic resting body alike.
func claudeTitleHasMarker(title string) bool {
	_, ok := claudeTitleMarkerBody(title)
	return ok
}

// claudeWindowTitleHasDescriptionMarker reports whether Claude's pane title
// carries a work marker over a turn description rather than over its generic
// resting body.
//
// A description body is stale-tolerant evidence, not proof of active work.
// Claude reverts neither the marker rune nor the description reliably when a
// turn ends, so an idle pane can hold the same description title indefinitely.
// The screen scrape is authoritative for whether a turn is running; this
// classifier only gates positive promotion of an empty tab to ⚡, where a marker
// that outlived its turn costs one recoverable promotion rather than a stuck
// icon.
func claudeWindowTitleHasDescriptionMarker(title string) bool {
	body, ok := claudeTitleMarkerBody(title)
	return ok && body != claudeRestingTitleText
}
