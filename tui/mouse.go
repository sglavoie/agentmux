package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

const mouseDoubleClickWindow = 400 * time.Millisecond

type mouseHitKind int

const (
	mouseHitNone mouseHitKind = iota
	mouseHitView
	// mouseHitProjectSwitcher is the header control that names the project scope
	// the list is narrowed to and opens the switcher — the pointer's spelling of
	// `p`. It runs the same command the key does.
	mouseHitProjectSwitcher
	// mouseHitInspectorToggle is the header control that shows or hides the
	// inspector — the pointer's spelling of `i`.
	mouseHitInspectorToggle
	// mouseHitInspectorBody is the inspector pane itself. It is a click target
	// rather than dead space for two reasons: clicking into a pane is how a
	// pointer says "aim the keys here", and a click that produced no target at
	// all would leave a half-finished double-click chain on the queue intact
	// across it.
	mouseHitInspectorBody
	mouseHitChip
	mouseHitSession
	mouseHitFamily
	mouseHitFamilyChevron
	// mouseHitProjectScope is one scope row inside the open project switcher.
	// It is the only click target the overlay publishes: there is nothing else
	// on it to aim at, and a click anywhere else leaves it open.
	mouseHitProjectScope
)

type mouseHitRegion struct {
	kind mouseHitKind
	rect dashboard.HitRect

	identity        dashboard.CursorIdentity
	filterRaw       string
	chipIndex       int
	chipLabel       string
	project         string
	label           string
	familyCollapsed bool
}

type mouseHitMap struct {
	clicks        []mouseHitRegion
	listPane      dashboard.HitRect
	inspectorPane dashboard.HitRect
	hasList       bool
	hasInspector  bool
}

type mouseActionKind int

const (
	mouseActionClick mouseActionKind = iota
	mouseActionListWheel
	mouseActionInspectorWheel
)

type mouseActionMsg struct {
	kind   mouseActionKind
	target mouseHitRegion
	delta  int
}

func clipHitRect(r dashboard.HitRect, width, height int) (dashboard.HitRect, bool) {
	if width <= 0 || height <= 0 || r.Width <= 0 || r.Height <= 0 {
		return dashboard.HitRect{}, false
	}
	x0, y0 := r.X, r.Y
	x1, y1 := r.X+r.Width, r.Y+r.Height
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	if x0 >= x1 || y0 >= y1 {
		return dashboard.HitRect{}, false
	}
	return dashboard.HitRect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, true
}

func (m mouseHitMap) clipped(width, height int) mouseHitMap {
	out := mouseHitMap{}
	for _, hit := range m.clicks {
		if rect, ok := clipHitRect(hit.rect, width, height); ok {
			hit.rect = rect
			out.clicks = append(out.clicks, hit)
		}
	}
	if rect, ok := clipHitRect(m.listPane, width, height); ok && m.hasList {
		out.listPane, out.hasList = rect, true
	}
	if rect, ok := clipHitRect(m.inspectorPane, width, height); ok && m.hasInspector {
		out.inspectorPane, out.hasInspector = rect, true
	}
	return out
}

func hitContains(r dashboard.HitRect, x, y int) bool {
	return r.Width > 0 && r.Height > 0 && x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func translateHitRect(r dashboard.HitRect, x, y int) dashboard.HitRect {
	r.X += x
	r.Y += y
	return r
}

func (m mouseHitMap) handler() func(tea.MouseMsg) tea.Cmd {
	return func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		if mouse.X < 0 || mouse.Y < 0 || mouse.Mod&tea.ModShift != 0 {
			return nil
		}
		switch msg.(type) {
		case tea.MouseClickMsg:
			if mouse.Button != tea.MouseLeft {
				return nil
			}
			for _, target := range m.clicks {
				if hitContains(target.rect, mouse.X, mouse.Y) {
					t := target
					return func() tea.Msg { return mouseActionMsg{kind: mouseActionClick, target: t} }
				}
			}
			// The inspector's body is resolved last and as a whole rather than as
			// a set of rows: nothing inside it is individually selectable, and the
			// click means only "the pointer is in this pane".
			if m.hasInspector && hitContains(m.inspectorPane, mouse.X, mouse.Y) {
				return func() tea.Msg {
					return mouseActionMsg{kind: mouseActionClick, target: mouseHitRegion{kind: mouseHitInspectorBody}}
				}
			}
		case tea.MouseWheelMsg:
			delta := 0
			switch mouse.Button {
			case tea.MouseWheelUp:
				delta = -3
			case tea.MouseWheelDown:
				delta = 3
			default:
				return nil
			}
			// Every pane is tested before the list, and the list is tested last,
			// so a wheel event scrolls the pane the pointer is actually over. The
			// panes never overlap, so the order is a reading convenience rather
			// than a precedence rule.
			if m.hasInspector && hitContains(m.inspectorPane, mouse.X, mouse.Y) {
				return func() tea.Msg { return mouseActionMsg{kind: mouseActionInspectorWheel, delta: delta} }
			}
			if m.hasList && hitContains(m.listPane, mouse.X, mouse.Y) {
				return func() tea.Msg { return mouseActionMsg{kind: mouseActionListWheel, delta: delta} }
			}
		}
		return nil
	}
}

func sameMouseIdentity(a, b dashboard.CursorIdentity) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == dashboard.ItemFamilySummary {
		return a.FamilyKey != "" && a.FamilyKey == b.FamilyKey
	}
	if a.TabID == 0 && a.WindowID == 0 && a.Key == "" {
		return false
	}
	return a.TabID == b.TabID && a.WindowID == b.WindowID && a.Key == b.Key && a.Base == b.Base &&
		a.Profile == b.Profile && a.Project == b.Project && a.Harness == b.Harness &&
		a.Directory == b.Directory && a.FamilyKey == b.FamilyKey
}

func findMouseItem(items dashboard.ItemList, identity dashboard.CursorIdentity) int {
	for i := 0; i < items.Len(); i++ {
		if sameMouseIdentity(dashboard.CaptureItemIdentity(items, i), identity) {
			return i
		}
	}
	return -1
}

func (m *Model) clearMouseClickChain() {
	m.lastMouseSession = dashboard.CursorIdentity{}
	m.lastMouseClickAt = time.Time{}
}

func (m Model) handleMouseAction(msg mouseActionMsg) (tea.Model, tea.Cmd) {
	// The project switcher is the one non-ModeList surface that publishes click
	// targets, and it publishes exactly one kind. Resolving it here rather than
	// widening the ModeList guard below keeps every dashboard-body action out of
	// reach while an overlay owns the screen.
	if m.mode == ModeProjectSwitcher {
		if msg.kind != mouseActionClick || msg.target.kind != mouseHitProjectScope {
			return m, nil
		}
		scopes := projectSwitcherScopes(m)
		for _, scope := range scopes {
			if scope.Project == msg.target.project && scope.Label == msg.target.label {
				m.projectSwitcherKey = scope.Key()
				return m, m.applyProjectScope()
			}
		}
		return m, nil
	}
	if m.mode != ModeList {
		return m, nil
	}
	m.snapshotSeenStates()
	m.clearJumpChord()

	// Every wheel event breaks a pending double-click chain, whichever pane it
	// scrolled. A scroll moves the rows out from under the pointer, so the click
	// that follows it lands on a different session than the one that started the
	// chain even when the coordinates are identical — and an inspector scroll
	// says the operator's attention left the queue entirely.
	switch msg.kind {
	case mouseActionListWheel:
		m.clearMouseClickChain()
		if m.listItemCount() == 0 {
			return m, nil
		}
		m.focusPane(focusQueue)
		m.cursor = clamp(m.cursor+msg.delta, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		return m, m.refreshPreviewForCursor()
	case mouseActionInspectorWheel:
		m.clearMouseClickChain()
		return m.scrollInspectorBy(msg.delta), nil
	}

	target := msg.target
	if target.kind != mouseHitSession {
		m.clearMouseClickChain()
	}
	switch target.kind {
	case mouseHitView:
		return m, m.toggleListView()
	case mouseHitProjectSwitcher:
		return m.openProjectSwitcher()
	case mouseHitInspectorToggle:
		return m.toggleInspector()
	case mouseHitInspectorBody:
		// Clicking into the pane aims the keys at it. Nothing else happens: the
		// inspector has no selectable rows, and the chain break above is the
		// other half of what this click is for.
		m.focusPane(focusInspector)
		return m, nil
	case mouseHitChip:
		components := m.activeFilter.Components()
		if m.activeFilter.Raw != target.filterRaw || target.chipIndex < 0 || target.chipIndex >= len(components) || components[target.chipIndex].Label() != target.chipLabel {
			return m, nil
		}
		return m, m.applyFilterChange(m.activeFilter.RemoveComponent(target.chipIndex))
	case mouseHitFamily, mouseHitFamilyChevron:
		i := findMouseItem(m.items, target.identity)
		if i < 0 {
			return m, nil
		}
		item, _ := m.items.At(i)
		m.focusPane(focusQueue)
		if target.kind == mouseHitFamilyChevron {
			if item.Collapsed != target.familyCollapsed {
				return m, nil
			}
			return m, m.setFamilyCollapsed(item.FamilyKey, !target.familyCollapsed)
		}
		m.cursor = i
		scrollListToCursor(&m)
		return m, m.refreshPreviewForCursor()
	case mouseHitSession:
		i := findMouseItem(m.items, target.identity)
		if i < 0 {
			m.clearMouseClickChain()
			return m, nil
		}
		now := modelNow(m)
		double := sameMouseIdentity(m.lastMouseSession, target.identity) && !m.lastMouseClickAt.IsZero() &&
			!now.Before(m.lastMouseClickAt) && now.Sub(m.lastMouseClickAt) <= mouseDoubleClickWindow
		if double {
			m.clearMouseClickChain()
		} else {
			m.lastMouseSession = target.identity
			m.lastMouseClickAt = now
		}
		m.cursor = i
		m.focusPane(focusQueue)
		scrollListToCursor(&m)
		var cmds []tea.Cmd
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if double {
			if session, ok := m.selectedSession(); ok {
				cmds = append(cmds, commands.Focus(m.client, session.TabID))
			}
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}
