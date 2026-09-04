package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

const mouseDoubleClickWindow = 400 * time.Millisecond

type mouseHitKind int

const (
	mouseHitNone mouseHitKind = iota
	mouseHitView
	mouseHitRailToggle
	mouseHitPreview
	mouseHitChip
	mouseHitSession
	mouseHitFamily
	mouseHitFamilyChevron
	mouseHitRailRow
)

type mouseHitRegion struct {
	kind mouseHitKind
	rect dashboard.HitRect

	identity        dashboard.CursorIdentity
	filterRaw       string
	chipIndex       int
	chipLabel       string
	project         string
	railLabel       string
	familyCollapsed bool
}

type mouseHitMap struct {
	clicks   []mouseHitRegion
	listPane dashboard.HitRect
	railPane dashboard.HitRect
	hasList  bool
	hasRail  bool
}

type dashboardHitRender struct {
	text   string
	hitMap mouseHitMap
}

type mouseActionKind int

const (
	mouseActionClick mouseActionKind = iota
	mouseActionListWheel
	mouseActionRailWheel
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
	if rect, ok := clipHitRect(m.railPane, width, height); ok && m.hasRail {
		out.railPane, out.hasRail = rect, true
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

func renderedRows(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// renderDashboardWithHits is the semantic form beneath the ordinary dashboard
// string renderer. Each child contributes text and regions together, so ANSI
// styling, wide glyphs, wrapped chips, group headings, and pane composition
// cannot move a target without moving the region produced beside it.
func renderDashboardWithHits(m Model) dashboardHitRender {
	var sb strings.Builder
	var hitMap mouseHitMap
	y := 0

	header := renderHeaderWithHits(m)
	sb.WriteString(header.text)
	for _, hit := range header.hits {
		hit.rect = translateHitRect(hit.rect, 0, y)
		hitMap.clicks = append(hitMap.clicks, hit)
	}
	y += renderedRows(header.text)

	for _, block := range []string{renderDeprecationBanner(m), renderLegend(m)} {
		sb.WriteString(block)
		y += renderedRows(block)
	}

	chips := dashboard.RenderChipsWithHits(m.activeFilter, dashboard.ProfileColorMap(m.cfg), m.focusedChipIdx, m.width)
	sb.WriteString(chips.Text)
	for _, chip := range chips.Hits {
		hitMap.clicks = append(hitMap.clicks, mouseHitRegion{
			kind: mouseHitChip, rect: translateHitRect(chip.Rect, 0, y),
			filterRaw: m.activeFilter.Raw, chipIndex: chip.Index, chipLabel: chip.Label,
		})
	}
	y += renderedRows(chips.Text)

	spacer := renderInlineFilter(m)
	sb.WriteString(spacer)
	y += renderedRows(spacer)

	panes := renderSplitWithHits(m)
	sb.WriteString(panes.text)
	for _, hit := range panes.hitMap.clicks {
		hit.rect = translateHitRect(hit.rect, 0, y)
		hitMap.clicks = append(hitMap.clicks, hit)
	}
	if panes.hitMap.hasList {
		hitMap.listPane = translateHitRect(panes.hitMap.listPane, 0, y)
		hitMap.hasList = true
	}
	if panes.hitMap.hasRail {
		hitMap.railPane = translateHitRect(panes.hitMap.railPane, 0, y)
		hitMap.hasRail = true
	}

	sb.WriteString(renderDetailStripBlock(m))
	return dashboardHitRender{text: sb.String(), hitMap: hitMap}
}

func renderSplitWithHits(m Model) dashboardHitRender {
	g := dashboardGeometry(m)
	list := dashboard.RenderListWithHits(dashboardListInput(m, g.listWidth, g.listHeight))
	result := dashboardHitRender{text: list.Text}
	listX := 0

	var rail dashboard.RailRender
	if g.rail {
		rail = dashboard.RenderRailWithHits(dashboard.RailInput{
			Rows: dashboardRailRows(m), Cursor: m.railCursor, Offset: m.railOffset,
			Width: g.railWidth, Height: g.railHeight, AppliedProject: m.activeFilter.Project, Focused: m.railFocused,
		})
		divider := strings.TrimRight(strings.Repeat(styleDim.Render("│")+"\n", g.listHeight), "\n")
		result.text = lipgloss.JoinHorizontal(lipgloss.Top, rail.Text, divider, list.Text)
		listX = g.railWidth + 1
		result.hitMap.railPane = dashboard.HitRect{Width: g.railWidth, Height: g.railHeight}
		result.hitMap.hasRail = true
		for _, hit := range rail.Hits {
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{
				kind: mouseHitRailRow, rect: hit.Rect, project: hit.Project, railLabel: hit.Label,
			})
		}
	}

	for _, hit := range list.Hits {
		row := translateHitRect(hit.Rect, listX, 0)
		if hit.HasChevron {
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{
				kind:            mouseHitFamilyChevron,
				rect:            dashboard.HitRect{X: listX + hit.ChevronX, Y: hit.Rect.Y, Width: 1, Height: 1},
				identity:        hit.Identity,
				familyCollapsed: hit.Collapsed,
			})
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{kind: mouseHitFamily, rect: row, identity: hit.Identity})
		} else {
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{kind: mouseHitSession, rect: row, identity: hit.Identity})
		}
	}
	result.hitMap.listPane = dashboard.HitRect{X: listX, Width: g.listWidth, Height: g.listHeight}
	result.hitMap.hasList = true

	if !g.split {
		if g.rail {
			result.text += "\n"
		}
		return result
	}
	if g.vertical {
		divider := styleDim.Render(strings.Repeat("─", m.width))
		result.text = lipgloss.JoinVertical(lipgloss.Left, result.text, divider,
			renderPreviewPane(m, g.previewWidth, g.previewHeight)) + "\n"
		return result
	}
	divider := strings.TrimRight(strings.Repeat(styleDim.Render("│")+"\n", g.listHeight), "\n")
	result.text = lipgloss.JoinHorizontal(lipgloss.Top, result.text, divider,
		renderPreviewPane(m, g.previewWidth, g.previewHeight)) + "\n"
	return result
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
			if m.hasRail && hitContains(m.railPane, mouse.X, mouse.Y) {
				return func() tea.Msg { return mouseActionMsg{kind: mouseActionRailWheel, delta: delta} }
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
	if m.mode != ModeList {
		return m, nil
	}
	m.snapshotSeenStates()
	m.clearJumpChord()

	switch msg.kind {
	case mouseActionListWheel:
		if m.listItemCount() == 0 {
			return m, nil
		}
		m.cursor = clamp(m.cursor+msg.delta, 0, m.listItemCount()-1)
		scrollListToCursor(&m)
		return m, m.refreshPreviewForCursor()
	case mouseActionRailWheel:
		if !dashboardGeometry(m).rail {
			return m, nil
		}
		m.moveRailCursor(msg.delta)
		return m, nil
	}

	target := msg.target
	if target.kind != mouseHitSession {
		m.clearMouseClickChain()
	}
	switch target.kind {
	case mouseHitView:
		return m, m.toggleListView()
	case mouseHitRailToggle:
		m.toggleRail()
		return m, nil
	case mouseHitPreview:
		updated, cmd := m.togglePreview()
		out := updated.(Model)
		out.syncRailState()
		return out, cmd
	case mouseHitChip:
		components := m.activeFilter.Components()
		if m.activeFilter.Raw != target.filterRaw || target.chipIndex < 0 || target.chipIndex >= len(components) || components[target.chipIndex].Label() != target.chipLabel {
			return m, nil
		}
		return m, m.applyFilterChange(m.activeFilter.RemoveComponent(target.chipIndex))
	case mouseHitRailRow:
		if !dashboardGeometry(m).rail {
			return m, nil
		}
		rows := dashboardRailRows(m)
		for i, row := range rows {
			if row.Project == target.project && row.Label == target.railLabel {
				m.railCursor = i
				return m, m.applyRailSelection()
			}
		}
		return m, nil
	case mouseHitFamily, mouseHitFamilyChevron:
		i := findMouseItem(m.items, target.identity)
		if i < 0 {
			return m, nil
		}
		item, _ := m.items.At(i)
		m.railFocused = false
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
		m.railFocused = false
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
