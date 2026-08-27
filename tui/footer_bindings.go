package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderFooterBinding renders an available key separately from its dim
// description. Keeping the two segments independent lets keys carry the
// shared accent without changing the description's established styling.
func renderFooterBinding(key, description string) string {
	return renderFooterBindingWithLabel(key, " ", description, styleFooter)
}

// renderUnavailableFooterBinding keeps an unavailable key visible in the
// muted palette while its description retains the same dim style used by
// available bindings.
func renderUnavailableFooterBinding(key, description string) string {
	return styleFooterUnavailableKey.Render(key) + " " + styleFooter.Render(description)
}

// renderFooterBindingWithLabel is the custom-label variant used when a
// description has semantic styling (for example, a destructive red label) or
// a compact prompt needs no gap between the key and label (for example [y]es).
func renderFooterBindingWithLabel(key, gap, label string, labelStyle lipgloss.Style) string {
	return styleFooterKey.Render(key) + gap + labelStyle.Render(label)
}

func footerBindingToken(key, description string) footerToken {
	return footerRenderedToken(renderFooterBinding(key, description))
}

func footerUnavailableBindingToken(key, description string) footerToken {
	return footerRenderedToken(renderUnavailableFooterBinding(key, description))
}

func footerCustomBindingToken(key, gap, label string, labelStyle lipgloss.Style) footerToken {
	return footerRenderedToken(renderFooterBindingWithLabel(key, gap, label, labelStyle))
}

// joinFooterBindings joins already-rendered binding segments without adding
// an indent or wrapping. Fixed-width modal and standalone footer rows use it;
// root footers continue through footerLineFromTokens and wrapFooterLine.
func joinFooterBindings(separator string, rendered ...string) string {
	kept := make([]string, 0, len(rendered))
	for _, item := range rendered {
		if item != "" {
			kept = append(kept, item)
		}
	}
	return strings.Join(kept, styleFooter.Render(separator))
}
