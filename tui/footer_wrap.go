package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
)

type footerEntry struct {
	key      string
	label    string
	section  string
	availFn  func(Model) bool
	priority int
}

// Section identifiers for footer command rows.
const (
	footerSectionRow    = "row"
	footerSectionLaunch = "launch"
	footerSectionApp    = "app"
)

// hasRow reports whether a row is selected; any row-targeted action requires a
// cursor sitting on a session.
func hasRow(m Model) bool {
	return len(m.filtered) > 0 && m.cursor < len(m.filtered)
}

// listFooterPalette is the fixed-order set of keys shown in the ModeList footer
// command rows. Order is invariant — entries never move between states. Each
// entry's availability function decides whether to render with the available
// or unavailable style.
// Every entry carries priority 0: the ModeList footer is budgeted by
// listFooterBudgetLineCount rather than truncated, so no entry is ever dropped.
var listFooterPalette = []footerEntry{
	{"e", "peek", footerSectionRow, func(m Model) bool {
		return hasRow(m) && m.filtered[m.cursor].State == "bolt"
	}, 0},
	{"T", "timeline", footerSectionRow, hasRow, 0},
	{"r", "restart", footerSectionRow, hasRow, 0},
	{"c", "profile", footerSectionRow, hasRow, 0},
	{"C", "launch", footerSectionRow, hasRow, 0},
	{"s", "project sessions", footerSectionRow, hasRow, 0},
	{"x", "clear", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		return stateGroup(m.filtered[m.cursor], m.lastActivity, m.idleThreshold) == 0
	}, 0},
	{"X", "close", footerSectionRow, hasRow, 0},
	{"o", "open dir", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		s := m.filtered[m.cursor]
		_, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return ok
	}, 0},
	{"O", "open in editor", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		s := m.filtered[m.cursor]
		_, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return ok
	}, 0},
	{"f", "profile", footerSectionLaunch, func(Model) bool { return true }, 0},
	{"l", "launch", footerSectionLaunch, func(Model) bool { return true }, 0},
	{":", "command palette", footerSectionApp, func(Model) bool { return true }, 0},
	{"S", "sessions", footerSectionApp, func(Model) bool { return true }, 0},
	{"P", "plans", footerSectionApp, func(Model) bool { return true }, 0},
	{"R", "resort", footerSectionApp, func(Model) bool { return true }, 0},
}

func listFooterActions(m Model) string {
	return listFooterSectionsLine(m, []string{
		footerSectionRow,
		footerSectionLaunch,
		footerSectionApp,
	})
}

func listFooterSectionsLine(m Model, sections []string) string {
	const indent = "  "
	innerSep := styleFooter.Render(" · ")
	innerSepW := layout.StyledWidth(innerSep)

	var tokens []footerToken
	for _, sec := range sections {
		entries := footerEntriesForSection(sec)
		if len(entries) == 0 {
			continue
		}
		for i, e := range entries {
			var tok footerToken
			if e.availFn(m) {
				tok = footerBindingToken(e.key, e.label)
			} else {
				tok = footerUnavailableBindingToken(e.key, e.label)
			}
			if i < len(entries)-1 || hasLaterEntries(sections, sec) {
				tok.sep = innerSep
				tok.sepWidth = innerSepW
			}
			tokens = append(tokens, tok)
		}
	}
	return wrapFooterLine(indent, indent, tokens, m.width)
}

func hasLaterEntries(sections []string, current string) bool {
	foundCurrent := false
	for _, sec := range sections {
		if !foundCurrent {
			if sec == current {
				foundCurrent = true
			}
			continue
		}
		if len(footerEntriesForSection(sec)) > 0 {
			return true
		}
	}
	return false
}

// footerToken pairs a styled rendered string with its visible width and an
// optional separator that follows it within a section. Pre-measuring with
// layout.StyledWidth lets us account for ANSI styling correctly.
type footerToken struct {
	rendered string
	width    int
	sep      string
	sepWidth int
}

// wrapFooterLine renders tokens onto one or more lines, breaking when the
// next token's render width would exceed maxWidth. firstLineLead is the lead
// rendered before the first token of the first line; continuationIndent is
// the left margin applied to every subsequent line. The trailing separator
// of the last token on each line is dropped so wrap points stay clean.
//
// When maxWidth <= 0 (uninitialized width before the first WindowSizeMsg),
// the helper emits a single unwrapped line — matching pre-wrap behavior.
// A token whose width alone exceeds maxWidth is still placed on its own
// line; we never split mid-token, mirroring renderChips.
func wrapFooterLine(firstLineLead, continuationIndent string, tokens []footerToken, maxWidth int) string {
	if len(tokens) == 0 {
		return firstLineLead
	}
	if maxWidth <= 0 {
		var sb strings.Builder
		sb.WriteString(firstLineLead)
		for i, t := range tokens {
			sb.WriteString(t.rendered)
			if i < len(tokens)-1 {
				sb.WriteString(t.sep)
			}
		}
		return sb.String()
	}

	leadW := layout.StyledWidth(firstLineLead)
	contW := layout.StyledWidth(continuationIndent)

	var lines []string
	var cur strings.Builder
	cur.WriteString(firstLineLead)

	lineW := leadW
	lineHasToken := false
	pendingSep := ""
	pendingSepW := 0
	for _, t := range tokens {
		// Cost to extend the current line with this token: the prior
		// token's separator (if any) plus the token itself.
		cost := t.width
		if lineHasToken {
			cost += pendingSepW
		}
		if lineHasToken && lineW+cost > maxWidth {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(continuationIndent)
			lineW = contW
			lineHasToken = false
			cost = t.width
		}
		if lineHasToken {
			cur.WriteString(pendingSep)
		}
		cur.WriteString(t.rendered)
		lineW += cost
		lineHasToken = true
		pendingSep = t.sep
		pendingSepW = t.sepWidth
	}
	lines = append(lines, cur.String())

	// Final per-line hard clamp: a lead that alone exceeds maxWidth (the
	// ModeList state gloss, #1) or a single token wider than the terminal
	// (the legend entry #3 or the picker DSL hint #4) is never split by the
	// wrap loop above, so truncate every emitted display line to maxWidth.
	// Mirrors the per-row/per-line safety clamp in list_rows.go and picker.go.
	for i, line := range lines {
		lines[i] = layout.StyledTruncate(line, maxWidth, "…")
	}
	return strings.Join(lines, "\n")
}

// footerEntriesForSection returns the palette entries whose section matches
// the given identifier, preserving declaration order.
func footerEntriesForSection(section string) []footerEntry {
	var out []footerEntry
	for _, e := range listFooterPalette {
		if e.section == section {
			out = append(out, e)
		}
	}
	return out
}
