// This file is the health details overlay's controller: entering it, scrolling
// it, dismissing the one notice that can be dismissed, and leaving it.
//
// The overlay is a READER. Every key in it either moves the viewport or records
// the operator's dismissal of the configuration notice; none of them observes
// anything. commands.Health remains the sole observer and remains driven only by
// the tick schedule, so opening, scrolling, and closing this overlay cannot
// change when a daemon ping happens — or, because that ping spawns a detached
// daemon when it fails, whether one is started at all.

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// openHealth raises the details overlay. The caller supplies the mode Esc lands
// on, for the same reason the help sheet and the config picker take one: the
// overlay is reachable from the palette in any root view, and a fixed return
// would drop the operator into a view they never opened.
func (m *Model) openHealth(returnMode Mode) {
	m.healthReturnMode = returnMode
	m.healthOffset = 0
	m.mode = ModeHealth
}

func (m *Model) closeHealth() {
	m.mode = m.healthReturnMode
	m.healthOffset = 0
}

// healthScrollBounds reports the furthest offset that still shows the last body
// line, and the rows a page step covers. Both are derived from the same input
// the renderer will use, so the keys and the window agree about where the end
// is.
//
// The maximum is found by asking layout.ListWindowFor rather than by computing
// total-minus-visible. Once the body is scrolled at all the window spends a row
// on its ▲ marker, so the arithmetic answer stops one line short — and the line
// it stops short of is the last line of the last issue, which on a short
// terminal is the whole of the configuration reading.
func healthScrollBounds(m Model) (maxOffset, page int) {
	in := healthInput(m)
	total := len(dashboard.HealthLines(in))
	page = in.Height
	if total <= 0 || page <= 0 {
		return 0, page
	}
	for offset := 0; offset < total; offset++ {
		if layout.ListWindowFor(total, offset, page).End >= total {
			return offset, page
		}
	}
	return total - 1, page
}

// clampHealthScroll pulls the offset back inside the body after the geometry
// changed under it. It is called from the same resize paths that clamp the
// detail pane, because a terminal that grew can leave an offset pointing past a
// body that now fits entirely on screen.
func clampHealthScroll(m *Model) {
	maxOffset, _ := healthScrollBounds(*m)
	m.healthOffset = clamp(m.healthOffset, 0, maxOffset)
}

func (m Model) handleHealthKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	maxOffset, page := healthScrollBounds(m)
	step := page / 2
	if step < 1 {
		step = 1
	}

	switch msg.String() {
	case keyEsc, keyQ:
		m.closeHealth()
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyD:
		return m.dismissHealthNotice()
	case keyJ, keyDown:
		if m.healthOffset < maxOffset {
			m.healthOffset++
		}
	case keyK, keyUp:
		if m.healthOffset > 0 {
			m.healthOffset--
		}
	case keyPgDown, keyCtrlF:
		m.healthOffset = clamp(m.healthOffset+page, 0, maxOffset)
	case keyPgUp, keyCtrlB:
		m.healthOffset = clamp(m.healthOffset-page, 0, maxOffset)
	case keyCtrlD:
		m.healthOffset = clamp(m.healthOffset+step, 0, maxOffset)
	case keyCtrlU:
		m.healthOffset = clamp(m.healthOffset-step, 0, maxOffset)
	case keyG, keyHome:
		m.healthOffset = 0
	case keyGG, keyEnd:
		m.healthOffset = maxOffset
	}
	return m, nil
}

// dismissHealthNotice silences the configuration notice for the session.
//
// It reaches the flag through the issue model rather than setting it directly,
// so the key can only act on an issue that both declares itself Dismissible and
// is currently asking for attention. That is what keeps the scope of a dismissal
// honest in the one direction that matters: `d` cannot silence a daemon that is
// down or hooks that have drifted, because neither issue is dismissible, and the
// overall reading recomputed on the next frame still carries them.
func (m Model) dismissHealthNotice() (tea.Model, tea.Cmd) {
	for _, issue := range healthIssues(m) {
		if !issue.Dismissible || !issue.WantsAttention() {
			continue
		}
		if issue.ID != dashboard.HealthIssueConfig {
			continue
		}
		m.deprecationDismissed = true
		clampHealthScroll(&m)
		return m, m.pushToast(Toast{
			Severity: toastSuccess,
			Text:     "Configuration notice dismissed for this session; health failures are unaffected",
		})
	}
	return m, nil
}
