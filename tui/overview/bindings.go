// What a key does on the grid, declared once.
//
// This table is the grid's keymap, and it is the ONLY statement of it. The
// dispatcher in update.go walks it, the footer in footer.go projects it, and the
// help overlay in help.go prints it. Before it existed the three disagreed by
// construction: the dispatcher was a switch, the footer was a hand-written list
// of eleven rendered strings beside it, and there was no help at all — so a key
// could be advertised without being bound, or bound without being mentioned, and
// nothing would fail.
//
// The table is deliberately NOT shared with the dashboard's own binding set.
// The two surfaces answer different keys on purpose — the dashboard has a
// filter, a command palette, and a launcher the grid does not, and the grid has
// digits, paging, and a compact toggle the dashboard does not — so a single set
// would have to carry a per-surface visibility flag on nearly every entry, which
// is two sets wearing one name. What they do share is the vocabulary a session
// action is declared in (tui/sessionactions), which is where the two surfaces
// must not be allowed to disagree.
//
// Three fields need saying out loud:
//
//   - keys is every msg.String() the dispatcher answers with run. A binding may
//     answer several: esc, q, and ctrl+c all leave, and the four arrows and four
//     hjkl letters are one movement binding rather than eight.
//   - parts is how the FOOTER spells the binding, one part per independently
//     available key group. PgUp can be dead while PgDn still works, so they are
//     two parts of one entry rather than one part of one entry.
//   - helpKeys is how the HELP spells it, which is not always the same string.
//     The footer prints `q exit` because the row has eleven entries competing for
//     one line; the help prints `esc / q / ctrl+c` because it has a column and
//     the other two keys are real.
package overview

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// Footer priority bands: which entries width pressure gives up, and in which
// order. They restate the root dashboard's ladder rather than importing it,
// because that package is the one this may not import; the numbers agreeing is
// cosmetic, and what matters is that 0 is never dropped.
const (
	footerPriorityEssential = 0
	footerPriorityPrimary   = 1
	footerPrioritySecondary = 2
	footerPriorityExtra     = 3
	footerPriorityNav       = 4
)

// The help overlay's sections, in the order it prints them. They are strings
// rather than an enum because nothing branches on them: they are headings.
const (
	sectionAct   = "Act on the selected session"
	sectionMove  = "Move the selection"
	sectionView  = "Change what a tile shows"
	sectionLeave = "Help and exit"
)

// helpSectionOrder is the order sections appear in the overlay. A binding whose
// section is not listed here would be invisible, which TestHelpCoversEveryBinding
// is what catches.
var helpSectionOrder = []string{sectionAct, sectionMove, sectionView, sectionLeave}

// bindingPart is one independently-available key group inside a footer entry.
//
// display is what the footer prints; short is the bare-key spelling the
// last-ditch row falls back to once every droppable entry is gone. An empty
// short means "the same as display", which is true of every single-key part.
type bindingPart struct {
	display   string
	short     string
	available func(footerFacts) bool
}

func (p bindingPart) shortText() string {
	if p.short == "" {
		return p.display
	}
	return p.short
}

// gridBinding is one row of the keymap.
type gridBinding struct {
	keys  []string
	parts []bindingPart
	// labelFor is the footer's word for what the binding does. It is a function
	// because exactly one label is not a constant: `c` names its DESTINATION,
	// so it reads "compact" from a normal grid and "normal" from a compact one,
	// which is what makes the hint a statement about what pressing it produces
	// rather than about which key it is.
	labelFor func(footerFacts) string
	// helpKeys overrides the key column in the help overlay. Empty means the
	// footer's parts, joined.
	helpKeys string
	// help is the overlay's sentence. It is the only place a binding explains
	// itself, so it says what the key does AND what bounds it.
	help    string
	section string
	// priority decides when width pressure drops the footer entry; onFooter
	// decides whether it is ever on the row at all. `u` is the one binding that
	// is real, documented, and deliberately off the footer — see help.go.
	priority int
	onFooter bool
	run      func(model, string) (tea.Model, tea.Cmd)
}

// fixedLabel is the footer label of a binding whose wording does not depend on
// anything.
func fixedLabel(s string) func(footerFacts) string {
	return func(footerFacts) string { return s }
}

// footerKeysText is how the help spells a binding when it has nothing more
// precise to say: the footer's own parts, joined the way the footer joins them.
func (b gridBinding) footerKeysText() string {
	if b.helpKeys != "" {
		return b.helpKeys
	}
	parts := make([]string, len(b.parts))
	for i, p := range b.parts {
		parts[i] = p.display
	}
	return strings.Join(parts, "/")
}

// footerFacts is everything the availability rules read.
//
// It is the rendered Input rather than the model on purpose: the footer is drawn
// from Input, and an availability rule that could reach the model would be able
// to answer from state the frame was not rendered against.
type footerFacts struct {
	in       Input
	g        Geometry
	selected int
}

func (f footerFacts) count() int { return len(f.in.Sessions) }

func (f footerFacts) hasRow() bool { return f.count() > 0 }

// canFocus mirrors requestFocus's own gate: there has to be a row, and a focus
// must not already be out.
func (f footerFacts) canFocus() bool { return f.hasRow() && !f.in.FocusPending }

func (f footerFacts) canMove(move Move) bool {
	return f.hasRow() && Navigate(f.g, f.count(), f.selected, move) != f.selected
}

// spatial is the availability of the arrows-and-hjkl entry, which is offered
// when ANY of its four directions would move the cursor.
func (f footerFacts) spatial() bool {
	return f.canMove(MoveLeft) || f.canMove(MoveRight) || f.canMove(MoveUp) || f.canMove(MoveDown)
}

func (f footerFacts) needsYou() bool {
	for _, s := range f.in.Sessions {
		if dashboard.NeedsYou(s, f.in.LastActivity, f.in.IdleThreshold) {
			return true
		}
	}
	return false
}

func (f footerFacts) canToggleCompact() bool { return !f.in.Closing }

func footerAlways(footerFacts) bool { return true }

// gridBindings is the keymap.
//
// Declaration order is FOOTER order, and the footer order is a contract rather
// than a preference: an operator who has learned where `a` sits must find it in
// the same place on the next terminal. The row leads with what acts on the
// selected session (Enter, a), then the two keys that are always true of the
// surface itself (help, exit), then everything that only moves the cursor or
// changes how a tile is drawn. Width pressure eats it from the bottom.
//
// It is built per call rather than held in a package variable because the
// runners close over model methods that read this table back — openHelp measures
// the help body, which is generated from these very entries — and a package
// variable would be an initialization cycle. The table is twelve entries and the
// callers are a keypress and a frame, so the allocation is not worth a
// once-value to avoid.
func gridBindings() []gridBinding {
	return []gridBinding{{
		keys:     []string{"enter"},
		parts:    []bindingPart{{display: "Enter", short: "↵", available: footerFacts.canFocus}},
		labelFor: fixedLabel("focus"),
		help:     "Focus the selected session's Kitty tab and leave the overview. Nothing happens while a focus is already out.",
		section:  sectionAct,
		priority: footerPriorityEssential,
		onFooter: true,
		run:      func(m model, _ string) (tea.Model, tea.Cmd) { return m.requestFocus() },
	},
		{
			keys:     []string{"a"},
			parts:    []bindingPart{{display: "a", available: footerFacts.hasRow}},
			labelFor: fixedLabel("actions"),
			help:     "Open the actions menu for the selected session — the same menu the dashboard offers on a row.",
			section:  sectionAct,
			priority: footerPriorityEssential,
			onFooter: true,
			run: func(m model, _ string) (tea.Model, tea.Cmd) {
				m.openActionsMenu()
				return m, nil
			},
		},
		{
			keys:     []string{"?"},
			parts:    []bindingPart{{display: "?", available: footerAlways}},
			labelFor: fixedLabel("help"),
			help:     "Show this list. Press ?, esc, or q to go back to the grid with the same session selected and the same page on screen.",
			section:  sectionLeave,
			priority: footerPriorityEssential,
			onFooter: true,
			run: func(m model, _ string) (tea.Model, tea.Cmd) {
				m.openHelp()
				return m, nil
			},
		},
		{
			keys:     []string{"esc", "q", "ctrl+c"},
			parts:    []bindingPart{{display: "q", available: footerAlways}},
			labelFor: fixedLabel("exit"),
			helpKeys: "esc/q/ctrl+c",
			help:     "Leave the overview. It leaves while a refresh or a focus is still out rather than waiting for one.",
			section:  sectionLeave,
			priority: footerPriorityEssential,
			onFooter: true,
			run:      func(m model, _ string) (tea.Model, tea.Cmd) { return m.dismiss() },
		},
		{
			keys:     digitKeys(),
			parts:    []bindingPart{{display: "1–0", available: footerFacts.canFocus}},
			labelFor: fixedLabel("jump"),
			help:     "Select and focus one of the first ten sessions in grid order, from whichever page is on screen. The digit is printed on the tile it addresses.",
			section:  sectionAct,
			priority: footerPriorityPrimary,
			onFooter: true,
			run: func(m model, key string) (tea.Model, tea.Cmd) {
				index := strings.Index(focusDigits, key)
				if index < 0 {
					return m, nil
				}
				return m.focusIndex(index)
			},
		},
		{
			keys: []string{"n", "N"},
			parts: []bindingPart{
				{display: "n", available: footerFacts.needsYou},
				{display: "N", available: footerFacts.needsYou},
			},
			labelFor: fixedLabel("needs you"),
			help:     "Jump to the next (n) or previous (N) session waiting on you, wrapping around the whole list. It moves the cursor only: nothing is focused and nothing is acknowledged.",
			section:  sectionMove,
			priority: footerPriorityPrimary,
			onFooter: true,
			run: func(m model, key string) (tea.Model, tea.Cmd) {
				if key == "N" {
					return m.jumpNeedsYou(-1)
				}
				return m.jumpNeedsYou(1)
			},
		},
		{
			keys:  []string{"c"},
			parts: []bindingPart{{display: "c", available: footerFacts.canToggleCompact}},
			labelFor: func(f footerFacts) string {
				if f.in.Compact {
					return "normal"
				}
				return "compact"
			},
			help:     "Switch every tile between the compact summary and the full preview. The footer names the mode the key switches TO. The choice is a view preference and is not written to your configuration.",
			section:  sectionView,
			priority: footerPriorityPrimary,
			onFooter: true,
			run:      func(m model, _ string) (tea.Model, tea.Cmd) { return m.toggleCompact() },
		},
		{
			keys:     []string{"left", "h", "right", "l", "up", "k", "down", "j"},
			parts:    []bindingPart{{display: "←↑↓→/hjkl", short: "←↑↓→", available: footerFacts.spatial}},
			labelFor: fixedLabel("move"),
			help:     "Move the selection one tile left, right, up, or down inside the page.",
			section:  sectionMove,
			priority: footerPrioritySecondary,
			onFooter: true,
			run:      runMove,
		},
		{
			keys:     []string{"tab", "shift+tab"},
			parts:    []bindingPart{{display: "Tab", available: moveAvailable(MoveNext)}},
			labelFor: fixedLabel("next"),
			helpKeys: "Tab/shift+Tab",
			help:     "Select the next or previous session in grid order, crossing onto the next page when the current one runs out.",
			section:  sectionMove,
			priority: footerPriorityExtra,
			onFooter: true,
			run:      runMove,
		},
		{
			keys: []string{"pgup", "pgdown"},
			parts: []bindingPart{
				{display: "PgUp", available: moveAvailable(MovePageUp)},
				{display: "PgDn", available: moveAvailable(MovePageDown)},
			},
			labelFor: fixedLabel("page"),
			help:     "Show the previous or next page of tiles. How many fit on a page is what the compact toggle changes.",
			section:  sectionMove,
			priority: footerPriorityExtra,
			onFooter: true,
			run:      runMove,
		},
		{
			keys: []string{"home", "end"},
			parts: []bindingPart{
				{display: "Home", available: moveAvailable(MoveFirst)},
				{display: "End", available: moveAvailable(MoveLast)},
			},
			labelFor: fixedLabel("ends"),
			help:     "Select the first or the last session in grid order.",
			section:  sectionMove,
			priority: footerPriorityNav,
			onFooter: true,
			run:      runMove,
		},
		{
			// u is the one binding that is real and deliberately NOT on the footer.
			// A close is offered back for five seconds and only after a CONFIRMED
			// close, so a permanent `u undo close` entry would spend a footer slot
			// advertising a key that is dead almost all of the time. The offer is
			// made where it is true instead — on the feedback row, by recoveryOffer,
			// which appears when a recovery exists and expires with it.
			keys:     []string{"u"},
			helpKeys: "u",
			help:     "Relaunch the session you just closed. It is offered for five seconds after a confirmed close and is named on the feedback row for exactly that long.",
			section:  sectionAct,
			run:      func(m model, _ string) (tea.Model, tea.Cmd) { return m, m.undoLastClose() },
		}}
}

// digitKeys is the ten focus digits as individual key strings, derived from the
// same constant the tiles print so the two cannot name different digits.
func digitKeys() []string {
	keys := make([]string, 0, len(focusDigits))
	for i := range len(focusDigits) {
		keys = append(keys, focusDigits[i:i+1])
	}
	return keys
}

// moveAvailable is the availability of a footer part that stands for exactly one
// Move: it is offered when that move would change the selection.
func moveAvailable(move Move) func(footerFacts) bool {
	return func(f footerFacts) bool { return f.canMove(move) }
}

// runMove is the dispatch shared by every movement binding. The key-to-Move
// table stays in update.go beside the geometry it addresses; this is only the
// adapter that lets four footer entries share one runner.
func runMove(m model, key string) (tea.Model, tea.Cmd) {
	move, ok := moveFor(key)
	if !ok {
		return m, nil
	}
	return m.applyMove(move)
}
