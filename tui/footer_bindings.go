package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func footerBindingToken(key, description string) footerToken {
	return footerRenderedToken(chrome.Binding(key, description))
}

func footerUnavailableBindingToken(key, description string) footerToken {
	return footerRenderedToken(chrome.UnavailableBinding(key, description))
}

func footerCustomBindingToken(key, gap, label string, labelStyle lipgloss.Style) footerToken {
	return footerRenderedToken(chrome.BindingWithLabel(key, gap, label, labelStyle))
}

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
// cursor sitting on a session. A cursor parked on an item that is not a session
// leaf reports false, so every row-section footer entry below renders in the
// unavailable style — which is the same thing the key handlers do when they
// fail to resolve a target.
func hasRow(m Model) bool {
	return m.hasSelectedSession()
}

// listFooterCandidate is one thing the regular and spacious ModeList footer may
// draw. Most candidates name a key, but two do not: the position fragment and
// the cursor row's state gloss describe the list rather than binding anything,
// and they compete for the same two rows against the same ladder as the keys.
// Declaring all of them in one ordered list is what makes truncation a single
// decision instead of two blocks racing each other for the space left over.
//
// The list stays authoritative when the budget omits an entry. Generated help is
// built from these same declarations, so the `? more` marker the drop appends
// always points at a keymap that is complete.
type listFooterCandidate struct {
	// footerEntry carries the canonical key, label, section, availability, and
	// priority band. A metadata candidate leaves the key empty, has no
	// availability, and never reaches generated help.
	footerEntry
	// keyFor and labelFor respell a binding for the current model without moving
	// it: enter focuses a session and folds a project summary. Generated help
	// documents the canonical spelling, which is the one true in every state.
	keyFor   func(Model) string
	labelFor func(Model) string
	// textFor renders a metadata candidate's own styled text, returning "" when
	// there is nothing to say. Metadata is never dimmed — it is either a fact
	// about the list or absent.
	textFor func(m Model, position string) string
	// shownFn omits a binding that does not exist in the current state at all:
	// the chip controls name no key until a filter is active. Generated help
	// still documents them, because the overlay is where a user who has never
	// filtered finds out the state exists.
	shownFn func(Model) bool
	// irreducibleFlag survives the last drop pass, below the essential band. See
	// prioritizedFooterToken.irreducible and listFooterRow.
	irreducibleFlag bool
}

// irreducible marks a candidate the narrowest row keeps. It is only meaningful
// on an essential-band entry: everything above that band is already gone by the
// time the pass that reads it runs.
func (c listFooterCandidate) irreducible() listFooterCandidate {
	c.irreducibleFlag = true
	return c
}

func listBinding(key, label, section string, priority int, avail func(Model) bool) listFooterCandidate {
	return listFooterCandidate{footerEntry: footerEntry{key, label, section, avail, priority}}
}

// listCommandBinding is a footer entry whose key, label, and availability all
// come from the dashboard binding table rather than being restated here. Only
// the section and the priority band are the footer's own business: they say
// where the entry is drawn and what it costs, which is a layout decision the
// binding has no opinion about.
//
// The availability is the command's own — the same question the palette asks —
// so a key rendered live is a key that will do something and a dim one will not.
// The exception is a binding the key runs unchecked: that key always acts, and
// the command reports its own refusal to the operator, so dimming it in the
// footer would contradict what pressing it does.
func listCommandBinding(bindings commands.BindingSet, id commands.ID, section string, priority int) listFooterCandidate {
	binding, ok := bindings.Of(id)
	if !ok {
		// Unreachable while the table is the only source of dashboard keys; a
		// footer entry for a command nothing binds would draw an empty key.
		return listBinding("", "", section, priority, footerNever)
	}
	avail := dashboardCommandAvailable(id)
	if binding.Gate == commands.GateUnchecked {
		avail = footerAlways
	}
	return listBinding(binding.Key(), binding.Hint, section, priority, avail)
}

// availableWhen overrides the availability a command-backed entry derives, for
// the two footer keys that mean more on the dashboard than the command they
// dispatch. It exists so the override has to be written down beside a reason
// rather than achieved by quietly declaring the entry as a literal.
func (c listFooterCandidate) availableWhen(fn func(Model) bool) listFooterCandidate {
	c.availFn = fn
	return c
}

func listMetadata(priority int, text func(m Model, position string) string) listFooterCandidate {
	return listFooterCandidate{footerEntry: footerEntry{priority: priority}, textFor: text}
}

func (c listFooterCandidate) respelled(keyFor, labelFor func(Model) string) listFooterCandidate {
	c.keyFor, c.labelFor = keyFor, labelFor
	return c
}

func (c listFooterCandidate) shownWhen(fn func(Model) bool) listFooterCandidate {
	c.shownFn = fn
	return c
}

// listFooterPalette keeps navigation in a stable order and reserves space for
// the selected session’s available answer or send action.
//
// The single row spends its budget on essentials before secondary hints.
// Omitted commands remain available through the palette and Dashboard help.
// Every binding here is documented in the generated Dashboard help
// section, and most are also reachable from the command palette.
// It is a function rather than a package variable because the command-backed
// entries read the catalog, and the catalog's preview gate measures this very
// footer. As a variable that would be an initialization cycle; as a function it
// is just the ordinary recursion the budget already resolves.
func listFooterPalette() []listFooterCandidate {
	// Read once and threaded through: the table is rebuilt per call, and asking
	// it for each of twenty entries would rebuild it twenty times per measured
	// frame.
	bindings := dashboardBindings()
	return []listFooterCandidate{
		// Essential: navigation, the selected session’s next action, and the commands
		// without which the dashboard cannot be driven
		// at all — move the cursor, act on what it holds, reach every other command,
		// and find out what those are. Dropping any of them would leave a row that
		// cannot lead anywhere, so none of them ever drop.
		listBinding("j/k", "up/down", footerSectionApp, footerPriorityEssential, footerAlways).irreducible(),
		// enter is not a row action even though it usually acts on one: on a project
		// summary it folds, which is not FocusSession. Its key and label come from
		// the binding, but its availability cannot, because the other thing it does
		// is available exactly when focusing is not.
		listCommandBinding(bindings, commands.FocusSession, footerSectionApp, footerPriorityEssential).
			availableWhen(footerAlways).
			respelled(nil, listEnterLabel).
			irreducible(),
		listCommandBinding(bindings, commands.SendToSession, footerSectionRow, footerPriorityEssential).
			shownWhen(dashboardCommandAvailable(commands.SendToSession)).
			respelled(nil, listSendLabel),
		listMetadata(footerPriorityEssential, func(m Model, _ string) string {
			row, ok := m.selectedSession()
			if commands.SendAvailability(commands.Context{Target: row, HasTarget: ok}).Reason == commands.ReasonSendASLQuestion {
				return chrome.Binding(": runs ↵", "answer run")
			}
			return ""
		}),
		listBinding("a", "actions", footerSectionRow, footerPriorityEssential, hasRow),
		listBinding(":", "commands", footerSectionApp, footerPriorityEssential, footerAlways).irreducible(),
		listCommandBinding(bindings, commands.FilterSessions, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.ShowKeyboardHelp, footerSectionApp, footerPriorityEssential).irreducible(),

		// Primary: where the cursor sits, what is filtering the list, and the rest of
		// the ways to move or leave. The position fragment leads the band — and so
		// outlives every key in it — because a list that is hiding rows and will not
		// say so is the one thing no keypress recovers.
		listMetadata(footerPriorityPrimary, listPositionText),
		// The command is hidden with no filter active and the entry is withheld in
		// exactly that state, so deriving the availability too would only ever
		// produce a dim key nothing draws.
		listCommandBinding(bindings, commands.ClearFilters, footerSectionApp, footerPriorityPrimary).
			availableWhen(footerAlways).
			shownWhen(listFilterActive),
		// Needs-you navigation is the only motion key here that can have nothing
		// to move to, so it is the only one that ever dims: on a dashboard where
		// nothing is waiting, `n` says so rather than looking like a key that
		// broke.
		listBinding("n/N", "needs you", footerSectionApp, footerPriorityPrimary, listNeedsYouReachable),
		// Tab names something only while there is a second pane to hand the
		// keyboard to. With one pane on screen the key answers nothing, and a
		// footer entry for it would be advertising a move that cannot happen.
		// A full-screen inspector is one pane as surely as a queue-only dashboard
		// is, so the entry is withheld there too.
		listBinding("tab", "switch pane", footerSectionApp, footerPriorityPrimary, footerAlways).
			shownWhen(listBothPanesShown),
		// The inspector leads the keys in this band because it is the only one
		// whose surface is already on screen: it is on before the first keypress,
		// so the row that names how to turn it off is the only thing on a
		// first-run dashboard that does. Quit follows for the same reason in
		// reverse — a row that cannot say how to leave is the one omission a
		// newcomer cannot recover from by pressing more keys.
		listCommandBinding(bindings, commands.ToggleInspector, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.Quit, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.SwitchProject, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.OpenLauncher, footerSectionLaunch, footerPriorityPrimary),
		listCommandBinding(bindings, commands.SearchAllSessions, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.SearchProject, footerSectionRow, footerPriorityPrimary),
		// Recovery is live only for the seconds after a confirmed close, so the
		// entry is shown only then rather than drawn dim the rest of the time;
		// it is still declared here, which is what puts `u` in the generated
		// Dashboard help.
		listCommandBinding(bindings, commands.UndoClose, footerSectionApp, footerPrioritySecondary).
			shownWhen(dashboardCommandAvailable(commands.UndoClose)),
	}
}

// listEnterLabel is the one respelling the footer still carries. Enter focuses a
// session on every line but a family summary, where it folds instead; the key is
// the same either way, so only the gloss changes.
//
// It used to respell the KEY as well — `enter/←/→` on a summary — because the
// arrows folded and unfolded separately. They are retired, so there is one key
// and one thing to say about it.
func listEnterLabel(m Model) string {
	if m.summarySelected() {
		return "fold/unfold"
	}
	return "open"
}

func listSendLabel(m Model) string {
	row, _ := m.selectedSession()
	if row.State == "key" || row.State == "question" {
		return "answer"
	}
	return "send prompt"
}

func listFilterActive(m Model) bool { return !m.activeFilter.IsEmpty() }

// listNeedsYouReachable reports whether `n`/`N` has anywhere to go. It asks the
// navigator itself rather than counting rows in the Needs you group, so the
// footer cannot say "nothing is waiting" about a row the keystroke would in fact
// land on — including one folded inside a family, which has no line of its own
// to count.
func listNeedsYouReachable(m Model) bool {
	_, ok := dashboard.FindNextNeedsYou(m.items, m.cursor, +1, m.lastActivity, m.idleThreshold)
	return ok
}

// listPositionText renders the `start–end of total` fragment the caller already
// measured. It takes the position rather than deriving one because the layout
// budget and the renderer must measure the same footer, and deriving it here
// would ask listVisibleHeight for a height that depends on this very answer.
func listPositionText(_ Model, position string) string {
	if position == "" {
		return ""
	}
	return styleFooter.Render(position)
}

// listFooterTokens renders the palette into prioritized tokens for the current
// model, in declaration order. Every candidate is rendered exactly once: as with
// footerHintTokens, availFn is pure over the Model snapshot, so re-running it per
// drop pass could only produce the same answer.
func listFooterTokens(m Model, position string) []prioritizedFooterToken {
	palette := listFooterPalette()
	if m.items.Len() == 0 {
		bindings := dashboardBindings()
		palette = []listFooterCandidate{
			listCommandBinding(bindings, commands.OpenLauncher, footerSectionLaunch, footerPriorityEssential).irreducible(),
			listCommandBinding(bindings, commands.SearchAllSessions, footerSectionApp, footerPriorityPrimary),
			listBinding(":", "commands", footerSectionApp, footerPriorityEssential, footerAlways).irreducible(),
			listCommandBinding(bindings, commands.ShowKeyboardHelp, footerSectionApp, footerPriorityEssential).irreducible(),
			listCommandBinding(bindings, commands.ClearFilters, footerSectionApp, footerPriorityEssential).availableWhen(footerAlways).shownWhen(listFilterActive),
			listCommandBinding(bindings, commands.Quit, footerSectionApp, footerPriorityPrimary),
		}
	}
	tokens := make([]prioritizedFooterToken, 0, len(palette))
	for _, c := range palette {
		tok, ok := c.render(m, position)
		if !ok {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// render returns the token this candidate draws for m, or ok=false when it has
// nothing to draw at all. An unavailable binding still draws: keeping the key
// visible in the muted style is what lets the footer's order stay fixed while
// the cursor moves.
func (c listFooterCandidate) render(m Model, position string) (prioritizedFooterToken, bool) {
	if c.textFor != nil {
		text := c.textFor(m, position)
		if text == "" {
			return prioritizedFooterToken{}, false
		}
		// Metadata has no bare-key spelling: it names no key, so the short row
		// either carries the same words or drops it like any other token.
		tok := footerRenderedToken(text)
		return prioritizedFooterToken{token: tok, short: tok, priority: c.priority}, true
	}
	if c.shownFn != nil && !c.shownFn(m) {
		return prioritizedFooterToken{}, false
	}
	key, label := c.key, c.label
	if c.keyFor != nil {
		key = c.keyFor(m)
	}
	if c.labelFor != nil {
		label = c.labelFor(m)
	}
	available := c.availFn(m)
	token := footerBindingToken(key, label)
	if !available {
		token = footerUnavailableBindingToken(key, label)
	}
	return prioritizedFooterToken{
		token:       token,
		short:       footerShortToken(key, available),
		irreducible: c.irreducibleFlag,
		priority:    c.priority,
	}, true
}

// listFooterBindings projects the palette onto the keybindings it declares,
// dropping the metadata candidates that name no key. It is what generated help
// documents and what parity tests compare against: the footer may omit a
// binding, but this projection never does.
func listFooterBindings() []footerEntry {
	palette := listFooterPalette()
	out := make([]footerEntry, 0, len(palette))
	for _, c := range palette {
		if c.key == "" {
			continue
		}
		out = append(out, c.footerEntry)
	}
	return out
}
