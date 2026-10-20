// The one row at the bottom of the grid.
//
// It is a PROJECTION of the keymap in bindings.go, never a second list beside
// it. Every entry on the row is an entry in that table, spelled by that table,
// and available exactly when that table's rule says the key would do something —
// which is what makes "the footer never advertises a key the grid does not bind"
// a property of the composition instead of a thing to remember.
//
// There is exactly one row, always, and that is the constraint everything else
// here follows from. The chrome reserves one line for hints and keeps it
// reserved, so a footer that wrapped would either steal a grid row or be clipped
// mid-binding. So when the entries do not fit, the row gives up WHOLE entries,
// lowest priority first and last-declared among ties, until they do. It never
// truncates an entry: half of `←↑↓→/hjkl move` is not a shorter hint, it is a
// key that does not exist.
//
// Two things it deliberately does not do:
//
//   - It appends no "? more" marker. The dashboard's footer does, because `?` is
//     the one binding it can point at when something was dropped. Here `?` is
//     already on the row and never droppable, so a marker would be the same key
//     printed twice on a line that is short of room by construction.
//   - It never offers `u`. A close is undoable for thirty seconds and only after
//     it is confirmed, so a permanent entry would be dim almost always; the
//     offer is made on the feedback row, where it appears with the recovery and
//     expires with it. See the last entry of gridBindings.
//
// The row belongs to the BASE VIEW. While a modal is up, none of these keys mean
// what the row says they mean — `q` leaves the modal rather than the program, and
// Enter, `a`, and the digits answer nothing at all — so the row is not drawn.
// What replaces it is the modal's own hint, which is "" for the three that print
// their keys inside their own box and confirmHints for the one that does not.

package overview

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/layout"
)

// footerSeparator is what sits between two entries on the row.
const footerSeparator = " · "

// footerEntry is one rendered entry plus what the drop ladder needs to know
// about it: how it is spelled without its label, and when it may be given up.
type footerEntry struct {
	full     string
	short    string
	priority int
}

// renderFooter projects the keymap onto the row, at the width it has.
func renderFooter(in Input, g Geometry, selected int) string {
	f := footerFacts{in: in, g: g, selected: selected}
	entries := make([]footerEntry, 0, len(gridBindings()))
	for _, b := range gridBindings() {
		if !b.onFooter {
			continue
		}
		entries = append(entries, footerEntry{
			full:     chrome.Keys(b.labelFor(f), footerParts(b, f, false)...),
			short:    chrome.Keys("", footerParts(b, f, true)...),
			priority: b.priority,
		})
	}
	return fitFooterRow(entries, in.Width)
}

// confirmHints is the row a destructive confirmation shows instead of the base
// row.
//
// The confirmation is the one modal on this surface that carries no hint of its
// own: the menu, the profile picker, and the keymap each print their keys inside
// their own box, and a footer repeating them would be the same instructions
// twice. The prompt prints a question and a consequence and stops, so the keys
// that answer it are said here — once, and only while it is up.
func confirmHints() string {
	return strings.Join([]string{
		chrome.Keys("confirm", chrome.Key{Text: "y", Available: true}),
		chrome.Keys("cancel", chrome.Key{Text: "n", Available: true}, chrome.Key{Text: "esc", Available: true}, chrome.Key{Text: "q", Available: true}),
	}, styleDim.Render(footerSeparator))
}

// footerParts renders one binding's key groups, each with its own availability.
// bare selects the short spelling, which is what the last-ditch row uses.
func footerParts(b gridBinding, f footerFacts, bare bool) []chrome.Key {
	keys := make([]chrome.Key, len(b.parts))
	for i, p := range b.parts {
		text := p.display
		if bare {
			text = p.shortText()
		}
		available := true
		if p.available != nil {
			available = p.available(f)
		}
		keys[i] = chrome.Key{Text: text, Available: available}
	}
	return keys
}

// fitFooterRow drops entries until the row fits, then falls back to bare keys.
//
// The two stages are different answers to different amounts of pressure. While
// anything is droppable, dropping is the right trade: the entries that go are
// the ones an operator is likeliest to already know (paging, ends, arrows), and
// what survives still reads as English. Once only the essentials are left there
// is nothing further to give up, and respelling them as bare keys is the last
// thing that can be done without lying — `Enter · a · ? · q` names four real
// bindings and claims nothing about what they do.
//
// A width of zero or less is "unconstrained", which is what a natural-width
// probe wants; the caller's own fitCell is what finally clips a row too wide for
// even the bare keys.
func fitFooterRow(entries []footerEntry, width int) string {
	kept := make([]footerEntry, len(entries))
	copy(kept, entries)

	for {
		row := joinFooterEntries(kept, false)
		if width <= 0 || layout.StyledWidth(row) <= width {
			return row
		}
		drop := footerDropIndex(kept)
		if drop < 0 {
			return joinFooterEntries(kept, true)
		}
		kept = append(kept[:drop], kept[drop+1:]...)
	}
}

// joinFooterEntries concatenates the surviving entries, in declaration order.
func joinFooterEntries(entries []footerEntry, bare bool) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.full
		if bare {
			parts[i] = e.short
		}
	}
	return strings.Join(parts, styleDim.Render(footerSeparator))
}

// footerDropIndex is the next entry to give up: the highest priority number, and
// among equal priorities the last declared. It returns -1 when only essentials
// remain, which is what ends the loop above.
func footerDropIndex(entries []footerEntry) int {
	drop := -1
	for i, e := range entries {
		if e.priority <= footerPriorityEssential {
			continue
		}
		if drop < 0 || e.priority >= entries[drop].priority {
			drop = i
		}
	}
	return drop
}
