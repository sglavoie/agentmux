package dashboard

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/layout"
)

// chipKind classifies a removable filter chip rendered above the list.
type chipKind int

const (
	chipSubstring chipKind = iota
	chipAccount
	chipHarness
	chipState
)

// Chip is one removable component extracted from a Filter. The label
// includes a leading sigil (`/`, `@`, `%`, `!`) so chips read like the DSL
// the user typed.
type Chip struct {
	kind  chipKind
	label string // sigil + value, e.g. "/api", "@work", "!key"
	raw   string // value without the sigil, e.g. "api", "work", "key"
}

// Label returns the chip's rendered text including its leading sigil.
func (c Chip) Label() string { return c.label }

// Components returns the ordered chip list backing the filter: substrings
// first (in token order), then account, then harness, then state. Removal
// keys (1..N) map onto this order so the indices are stable as long as
// Components and RemoveComponent are kept in sync.
func (f Filter) Components() []Chip {
	var out []Chip
	for _, s := range f.Substrings {
		out = append(out, Chip{kind: chipSubstring, label: "/" + s, raw: s})
	}
	if f.Account != "" {
		out = append(out, Chip{kind: chipAccount, label: "@" + f.Account, raw: f.Account})
	}
	if f.Harness != "" {
		out = append(out, Chip{kind: chipHarness, label: "%" + f.Harness, raw: f.Harness})
	}
	if f.State != "" {
		out = append(out, Chip{kind: chipState, label: "!" + f.State, raw: f.State})
	}
	return out
}

// RemoveComponent returns a new Filter with the component at idx removed.
// Indices match the order returned by Components. An out-of-range idx is a
// no-op (returns the original filter unchanged).
func (f Filter) RemoveComponent(idx int) Filter {
	comps := f.Components()
	if idx < 0 || idx >= len(comps) {
		return f
	}
	out := Filter{
		Substrings: append([]string(nil), f.Substrings...),
		Account:    f.Account,
		Harness:    f.Harness,
		State:      f.State,
	}
	target := comps[idx]
	switch target.kind {
	case chipSubstring:
		// Substrings sit at positions 0..len(Substrings)-1 in Components,
		// so idx is a direct slice index.
		out.Substrings = append(out.Substrings[:idx], out.Substrings[idx+1:]...)
	case chipAccount:
		out.Account = ""
	case chipHarness:
		out.Harness = ""
	case chipState:
		out.State = ""
	}
	out.Raw = SerializeFilter(out)
	return out
}

// RenderChips renders the filter chip row(s) for f, or "" when the filter is
// empty (so the area collapses with no blank line). Chips wrap to a second
// line when the running width would exceed width. focused is the index of the
// chip carrying keyboard focus, or -1 for none.
func RenderChips(f Filter, acctColor map[string]string, focused, width int) string {
	if f.IsEmpty() {
		return ""
	}
	comps := f.Components()

	const chipSep = "  "
	sepW := layout.StyledWidth(chipSep)

	var lines []string
	var line strings.Builder
	lineW := 0
	for i, c := range comps {
		chip := RenderChip(c, acctColor, i == focused)
		// A single chip whose label exceeds the terminal is never split by the
		// wrap loop below, so clamp it to width first.
		if width > 0 {
			chip = layout.StyledTruncate(chip, width, "…")
		}
		chipW := layout.StyledWidth(chip)
		add := chipW
		if line.Len() > 0 {
			add += sepW
		}
		if width > 0 && line.Len() > 0 && lineW+add > width {
			lines = append(lines, line.String())
			line.Reset()
			lineW = 0
			add = chipW
		}
		if line.Len() > 0 {
			line.WriteString(chipSep)
		}
		line.WriteString(chip)
		lineW += add
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n") + "\n"
}

// RenderChip renders a single chip body. The leading ▌ glyph and trailing
// ✕ remove icon share the chip's color so the entire chip reads as one unit.
// Focused chips wrap the ✕ in brackets — Postel's Law: chip focus is also
// signaled by the underline overlay in chipStyle, but the bracketed glyph
// makes the affordance unmistakable.
func RenderChip(c Chip, acctColor map[string]string, focused bool) string {
	suffix := " ✕"
	if focused {
		suffix = " [✕]"
	}
	return chipStyle(c, acctColor, focused).Render("▌" + c.label + suffix)
}

// chipStyle returns the rendering style for a chip, layering the focus
// underline on top of the kind-specific base color when applicable.
func chipStyle(c Chip, acctColor map[string]string, focused bool) lipgloss.Style {
	var base lipgloss.Style
	switch c.kind {
	case chipAccount:
		base = accountStyle(acctColor[c.raw])
	case chipState:
		switch c.raw {
		case "key", "stop":
			base = styleAttentionRow
		case "warning":
			base = styleStateWarning
		case "error":
			base = styleStateError
		default:
			base = styleChip
		}
	default:
		base = styleChip
	}
	if focused {
		base = base.Underline(true)
	}
	return base
}
