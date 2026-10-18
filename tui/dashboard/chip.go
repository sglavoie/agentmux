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
	chipProject
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
// first (in token order), then project, account, harness, and state. A chip's
// index in this order is what a click resolves to, so the indices are stable as
// long as Components and RemoveComponent are kept in sync.
func (f Filter) Components() []Chip {
	var out []Chip
	for _, s := range f.Substrings {
		out = append(out, Chip{kind: chipSubstring, label: "/" + s, raw: s})
	}
	if f.Project != "" {
		out = append(out, Chip{kind: chipProject, label: "#" + f.Project, raw: f.Project})
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
		Project:    f.Project,
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
	case chipProject:
		out.Project = ""
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
// line when the running width would exceed width.
//
// A chip has no focused form. The keyboard ring that gave it one is retired —
// filter editing happens inside `/` and a chip is removed by clicking it — so
// every chip is drawn the same way and the hit rectangles below are the whole of
// what a chip offers a pointer.
func RenderChips(f Filter, acctColor map[string]string, width int) string {
	return RenderChipsWithHits(f, acctColor, width).Text
}

// RenderChipsWithHits renders chips and returns their wrapped terminal-cell
// rectangles from the same layout loop.
func RenderChipsWithHits(f Filter, acctColor map[string]string, width int) ChipRender {
	if f.IsEmpty() {
		return ChipRender{Text: RenderFilterHint(f.Raw, width)}
	}
	comps := f.Components()

	const chipSep = "  "
	sepW := layout.StyledWidth(chipSep)

	var lines []string
	var hits []ChipHit
	var line strings.Builder
	lineW := 0
	y := 0
	for i, c := range comps {
		chip := renderChip(c, acctColor)
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
			y++
		}
		x := lineW
		if line.Len() > 0 {
			line.WriteString(chipSep)
			x += sepW
		}
		line.WriteString(chip)
		hits = append(hits, ChipHit{Rect: HitRect{X: x, Y: y, Width: chipW, Height: 1}, Index: i, Label: c.Label()})
		lineW += add
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return ChipRender{Text: strings.Join(lines, "\n") + "\n" + RenderFilterHint(f.Raw, width), Hits: hits}
}

// renderChip renders a single chip body. The leading ▌ glyph and trailing
// ✕ remove icon share the chip's color so the entire chip reads as one unit,
// and the ✕ is the affordance: clicking the chip removes that component.
func renderChip(c Chip, acctColor map[string]string) string {
	return chipStyle(c, acctColor).Render("▌" + c.label + " ✕")
}

// chipStyle returns the rendering style for a chip: the kind-specific base
// color, and nothing layered on top of it.
func chipStyle(c Chip, acctColor map[string]string) lipgloss.Style {
	var base lipgloss.Style
	switch c.kind {
	case chipAccount:
		base = accountStyle(acctColor[c.raw])
	case chipState:
		switch strings.ToLower(c.raw) {
		case "attention", "key", "stop":
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
	return base
}

// RenderFilterHint reserves one bounded row beneath the filter. Chip hit
// rectangles remain unchanged because this row has no action of its own.
func RenderFilterHint(input string, width int) string {
	hint := FilterHint(input)
	if hint == "" {
		return ""
	}
	return styleStateWarning.Render(layout.ClampLine(hint, width)) + "\n"
}
