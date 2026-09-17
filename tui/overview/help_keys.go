// The help overlay's state, and the keys that move it.
//
// Opening and closing it TOUCHES NOTHING. The selection, the page, the captures
// in flight, and the recovery stack are all exactly as they were: help is a
// separate field on the model that the renderer composites over a finished
// frame, so there is no path from opening it to the state underneath. That is
// asserted rather than assumed, because "press ? and lose your place" is the
// failure this overlay would otherwise be most likely to have.
//
// It is deliberately NOT part of the action state. Help acts on nothing, captures
// no row, and starts no command; a surface that shared the action layer's state
// would be able to be raised while an action was pending.
package overview

import (
	tea "charm.land/bubbletea/v2"
)

// helpState is the overlay's whole state: whether it is up, and how far down it
// has been scrolled.
//
// The offset is kept across a close and reopen on purpose. An operator who
// scrolled to the movement section, went back to the grid to try a key, and
// pressed ? again is asking for the same place in the same list.
type helpState struct {
	open   bool
	offset int
}

// openHelp raises the overlay. It writes nothing but the overlay's own state,
// which is the property the file doc promises.
func (m *model) openHelp() {
	m.help.open = true
	m.help.offset = m.clampHelpOffset(m.help.offset)
}

// closeHelp lowers it, keeping the scroll position.
func (m *model) closeHelp() {
	m.help.open = false
}

// handleHelpKey drives the overlay.
//
// The scroll vocabulary is the one every other scrolling surface in this
// repository uses — j/k, arrows, ctrl+f/b, ctrl+d/u, g/G — so an operator who
// can read a transcript can read this. Dismissal is esc, q, and ? itself: the
// key that opened it closes it, which is what makes ? a toggle rather than a
// door that needs a different handle to leave by. ctrl+c leaves the whole
// program, exactly as it does under the action modals, because it is the
// operator asking for the process rather than for this overlay.
func (m model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	page := max(m.helpVisibleHeight()-1, 1)
	scroll := func(delta int) (tea.Model, tea.Cmd) {
		m.help.offset = m.clampHelpOffset(m.help.offset + delta)
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m.dismiss()
	case "esc", "q", "?":
		m.closeHelp()
		return m, nil
	case "j", "down":
		return scroll(1)
	case "k", "up":
		return scroll(-1)
	case "pgdown", "ctrl+f":
		return scroll(page)
	case "pgup", "ctrl+b":
		return scroll(-page)
	case "ctrl+d":
		return scroll(max(page/2, 1))
	case "ctrl+u":
		return scroll(-max(page/2, 1))
	case "g":
		return scroll(-len(m.helpContent()))
	case "G":
		return scroll(len(m.helpContent()))
	}
	return m, nil
}

// helpFrame is the model's geometry, handed to the renderer.
func (m model) helpFrame() helpFrame {
	return helpFrame{width: m.width, height: overlayBodyHeight(m), offset: m.help.offset}
}

// helpVisibleHeight is how many content rows the overlay shows at once.
func (m model) helpVisibleHeight() int { return helpVisible(overlayBodyHeight(m)) }

// helpContent is the rendered body, one string per row.
func (m model) helpContent() []string { return helpLines(helpContentWidth(m.width)) }

// clampHelpOffset confines the scroll position to the content that exists.
func (m model) clampHelpOffset(offset int) int {
	return clamp(offset, 0, max(len(m.helpContent())-m.helpVisibleHeight(), 0))
}

// renderHelpBox is the overlay the grid is composited under.
func (m model) renderHelpBox() string { return helpBox(m.helpFrame()) }
