// Package chrome is the one spelling of a footer key binding: how a key and
// its description are styled, how an unavailable key is muted, how paired keys
// that are available independently ("pgup/pgdn") are drawn, and how rendered
// bindings are joined into a row. Every footer in the TUI — the dashboard's
// wrapping palette, the Past Sessions and command modals, the standalone
// customization overlays, and the overview grid — draws its bindings here, so
// a key reads the same wherever it is offered.
//
// It decides styling only. Which bindings a footer offers, in what order, and
// how a row wraps or sheds entries to fit stay with the view that owns the
// footer. Its only in-repo dependency is tui/theme; it names no model,
// session, or action.
package chrome

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/theme"
)

var (
	// FooterLabel is the dim style of a binding's description and of the
	// separators between bindings.
	FooterLabel = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	// FooterKey is the accent style of an available key.
	FooterKey = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	// FooterUnavailableKey keeps a key that would do nothing right now visible
	// while signalling that pressing it is a no-op.
	FooterUnavailableKey = lipgloss.NewStyle().Foreground(theme.Color(theme.Disabled)).Bold(true)
)

// Binding renders an available key followed by its dim description. The two
// segments are styled independently so the key carries the shared accent
// without changing the description's styling.
func Binding(key, description string) string {
	return BindingWithLabel(key, " ", description, FooterLabel)
}

// UnavailableBinding renders a key that does not apply right now in the muted
// key style, with the same dim description as an available one.
func UnavailableBinding(key, description string) string {
	return FooterUnavailableKey.Render(key) + " " + FooterLabel.Render(description)
}

// BindingWithLabel is the variant for a description with semantic styling (a
// destructive red label) or a compact prompt that needs no gap between the key
// and its label ("[y]es").
func BindingWithLabel(key, gap, label string, labelStyle lipgloss.Style) string {
	return FooterKey.Render(key) + gap + labelStyle.Render(label)
}

// Key is one key of a paired binding, with whether it would do anything now.
type Key struct {
	Text      string
	Available bool
}

// Keys renders paired keys that are available independently — PgUp can be
// unavailable while PgDn still works — joined by a dim "/", followed by the
// dim label when there is one.
func Keys(label string, keys ...Key) string {
	parts := make([]string, len(keys))
	for i, key := range keys {
		style := FooterUnavailableKey
		if key.Available {
			style = FooterKey
		}
		parts[i] = style.Render(key.Text)
	}
	joined := strings.Join(parts, FooterLabel.Render("/"))
	if label == "" {
		return joined
	}
	return joined + " " + FooterLabel.Render(label)
}

// JoinBindings joins already-rendered bindings with a dim separator, skipping
// empty ones, without indenting or wrapping. A footer that must wrap or shed
// entries to fit does that itself before or after joining.
func JoinBindings(separator string, rendered ...string) string {
	kept := make([]string, 0, len(rendered))
	for _, item := range rendered {
		if item != "" {
			kept = append(kept, item)
		}
	}
	return strings.Join(kept, FooterLabel.Render(separator))
}
