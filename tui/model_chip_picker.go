// This file is what the root model does about filtering: the dashboard filter
// expression it applies and cycles, and the chip picker overlay through which
// a filter's components — and the Plans browser's project filter — are chosen
// and removed.

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// chipPickerKind identifies which search-view chip filter the picker edits.
type chipPickerKind int

const (
	chipPickerHarness chipPickerKind = iota
	chipPickerProfile
	chipPickerProject
)

// chipPickerUnknownProjectValue is an internal option value whose display
// label is "Unknown". A sentinel keeps a configured project literally named
// "Unknown" distinct from plans that have no configured-project match.
const chipPickerUnknownProjectValue = "\x00plans-project-unknown"

// enterChipPicker opens the chip picker overlay for the given kind. The
// option list is captured at open time (prepended with "" for the "(any)"
// row) so a runtime config change can't shift the cursor under the user.
// The cursor is preselected to the row matching the currently-active filter
// when that value is still present in the option list; otherwise it lands on
// the "(any)" row.
func (m Model) enterChipPicker(kind chipPickerKind) (tea.Model, tea.Cmd) {
	returnMode := m.mode
	if returnMode != ModePlanSearch {
		returnMode = ModeSessionSearch
	}
	m.mode = ModeChipPicker
	m.chipPickerReturnMode = returnMode
	m.chipPickerKind = kind
	m.chipPickerQuery = ""
	m.chipPickerSearchMode = false

	var opts []string
	var current string
	switch kind {
	case chipPickerHarness:
		opts = harness.Names()
		current = singleChipValue(m.filterHarness)
	case chipPickerProfile:
		opts = profileNames(m)
		current = singleChipValue(m.filterProfile)
	case chipPickerProject:
		if returnMode == ModePlanSearch {
			opts = planProjectOptions(m)
			current = planProjectFilterOption(m.planFilterProject)
		} else {
			opts = projectTitles(m)
			current = singleChipValue(m.filterProject)
		}
	}
	options := make([]string, 0, len(opts)+1)
	options = append(options, "")
	options = append(options, opts...)
	m.chipPickerAllOptions = options
	m.chipPickerOptions = filterChipPickerOptions(m.chipPickerAllOptions, m.chipPickerQuery)

	cursor := 0
	if current != "" {
		for i, o := range m.chipPickerOptions {
			if o == current {
				cursor = i
				break
			}
		}
	}
	m.chipPickerCursor = cursor
	m.chipPickerOffset = 0
	scrollChipPickerToCursor(&m)
	return m, nil
}

// handleChipPickerKey dispatches keys for ModeChipPicker. esc/q cancel
// without changing the filter; enter commits the highlighted row and
// dispatches a re-search. Navigation mirrors the other overlays.
func (m Model) handleChipPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.chipPickerSearchMode {
		return m.handleChipPickerSearchKey(msg)
	}
	return m.handleChipPickerNavKey(msg)
}

func (m Model) handleChipPickerSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyEnter:
		m.chipPickerSearchMode = false
		return m, nil
	case keyBackspace:
		if r := []rune(m.chipPickerQuery); len(r) > 0 {
			m.chipPickerQuery = string(r[:len(r)-1])
			applyChipPickerQuery(&m)
		}
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.chipPickerQuery += t
			applyChipPickerQuery(&m)
		}
		return m, nil
	}
}

func (m Model) handleChipPickerNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keySlash:
		m.chipPickerSearchMode = true
		return m, nil
	case keyEsc, keyQ:
		m.mode = chipPickerSourceMode(m)
		return m, nil
	case keyJ, keyDown:
		m.chipPickerCursor = clamp(m.chipPickerCursor+1, 0, len(m.chipPickerOptions)-1)
		scrollChipPickerToCursor(&m)
		return m, nil
	case keyK, keyUp:
		m.chipPickerCursor = clamp(m.chipPickerCursor-1, 0, len(m.chipPickerOptions)-1)
		scrollChipPickerToCursor(&m)
		return m, nil
	case keyG:
		m.chipPickerCursor = 0
		scrollChipPickerToCursor(&m)
		return m, nil
	case keyGG:
		m.chipPickerCursor = len(m.chipPickerOptions) - 1
		if m.chipPickerCursor < 0 {
			m.chipPickerCursor = 0
		}
		scrollChipPickerToCursor(&m)
		return m, nil
	case keyPgDown:
		step := chipPickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.chipPickerCursor = clamp(m.chipPickerCursor+step, 0, len(m.chipPickerOptions)-1)
		scrollChipPickerToCursor(&m)
		return m, nil
	case keyPgUp:
		step := chipPickerVisibleHeight(m) - 1
		if step < 1 {
			step = 1
		}
		m.chipPickerCursor = clamp(m.chipPickerCursor-step, 0, len(m.chipPickerOptions)-1)
		scrollChipPickerToCursor(&m)
		return m, nil
	case keyEnter:
		if len(m.chipPickerOptions) == 0 {
			return m, nil
		}
		if m.chipPickerCursor < 0 || m.chipPickerCursor >= len(m.chipPickerOptions) {
			m.mode = chipPickerSourceMode(m)
			return m, nil
		}
		selected := m.chipPickerOptions[m.chipPickerCursor]
		if chipPickerSourceMode(m) == ModePlanSearch && m.chipPickerKind == chipPickerProject {
			switch selected {
			case "":
				m.planFilterProject = plans.ProjectFilter{Kind: plans.ProjectAny}
			case chipPickerUnknownProjectValue:
				m.planFilterProject = plans.ProjectFilter{Kind: plans.ProjectUnknown}
			default:
				m.planFilterProject = plans.ProjectFilter{Kind: plans.ProjectNamed, Name: selected}
			}
			m.mode = ModePlanSearch
			return m, m.dispatchPlanSearch()
		}
		var next []string
		if selected != "" {
			next = []string{selected}
		}
		switch m.chipPickerKind {
		case chipPickerHarness:
			m.filterHarness = next
		case chipPickerProfile:
			m.filterProfile = next
		case chipPickerProject:
			m.filterProject = next
		}
		m.mode = ModeSessionSearch
		return m, m.dispatchSessionSearch()
	}
	return m, nil
}

func applyChipPickerQuery(m *Model) {
	m.chipPickerOptions = filterChipPickerOptions(m.chipPickerAllOptions, m.chipPickerQuery)
	m.chipPickerCursor = 0
	m.chipPickerOffset = 0
	scrollChipPickerToCursor(m)
}

func filterChipPickerOptions(options []string, query string) []string {
	if query == "" {
		return append([]string(nil), options...)
	}
	q := strings.ToLower(query)
	filtered := make([]string, 0, len(options))
	for _, opt := range options {
		if opt == "" {
			continue
		}
		if strings.Contains(strings.ToLower(chipPickerOptionLabel(opt)), q) {
			filtered = append(filtered, opt)
		}
	}
	return filtered
}

func chipPickerSourceMode(m Model) Mode {
	if m.chipPickerReturnMode == ModePlanSearch {
		return ModePlanSearch
	}
	return ModeSessionSearch
}

func chipPickerOptionLabel(option string) string {
	if option == chipPickerUnknownProjectValue {
		return "Unknown"
	}
	return option
}

func planProjectFilterOption(filter plans.ProjectFilter) string {
	switch filter.Kind {
	case plans.ProjectNamed:
		return filter.Name
	case plans.ProjectUnknown:
		return chipPickerUnknownProjectValue
	default:
		return ""
	}
}

// planProjectOptions returns only projects represented by indexed plans, in
// configuration order, plus Unknown when at least one plan is unassociated.
func planProjectOptions(m Model) []string {
	var metas []plans.PlanMeta
	if m.plansIndex != nil {
		metas = m.plansIndex.List()
	} else {
		metas = make([]plans.PlanMeta, 0, len(m.planResults))
		for _, result := range m.planResults {
			metas = append(metas, result.Meta)
		}
	}

	represented := make(map[string]bool)
	unknown := false
	for _, meta := range metas {
		projects := meta.ProjectNames()
		if len(projects) == 0 {
			unknown = true
		}
		for _, project := range projects {
			represented[project] = true
		}
	}

	options := make([]string, 0, len(represented)+1)
	seen := make(map[string]bool, len(represented))
	if m.cfg != nil {
		for _, project := range m.cfg.Projects {
			if represented[project.Title] && !seen[project.Title] {
				options = append(options, project.Title)
				seen[project.Title] = true
			}
		}
	}
	// Preserve discoverability for a warm cache whose configuration has not
	// yet completed its refresh, and for lightweight test models without cfg.
	for _, meta := range metas {
		for _, project := range meta.ProjectNames() {
			if !seen[project] {
				options = append(options, project)
				seen[project] = true
			}
		}
	}
	if unknown {
		options = append(options, chipPickerUnknownProjectValue)
	}
	return options
}

// scrollChipPickerToCursor keeps the cursor inside the visible window after
// navigation. Mirrors scrollSessionSearchToCursor: the renderer reserves one
// row for ▲ when offset > 0 and one row for ▼ when items remain past the
// window, so the scroll budget must subtract them too.
func scrollChipPickerToCursor(m *Model) {
	total := len(m.chipPickerOptions)
	height := chipPickerVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.chipPickerOffset = 0
		return
	}
	if m.chipPickerCursor < 0 {
		m.chipPickerCursor = 0
	}
	if m.chipPickerCursor >= total {
		m.chipPickerCursor = total - 1
	}
	if m.chipPickerCursor < m.chipPickerOffset {
		m.chipPickerOffset = m.chipPickerCursor
	}
	for m.chipPickerOffset < m.chipPickerCursor {
		budget := height
		if m.chipPickerOffset > 0 {
			budget--
		}
		if m.chipPickerCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.chipPickerCursor-m.chipPickerOffset+1 <= budget {
			break
		}
		m.chipPickerOffset++
	}
	if m.chipPickerOffset < 0 {
		m.chipPickerOffset = 0
	}
}

// applyActiveFilter re-applies m.activeFilter to the session list and reprojects
// around prior, which callers capture against the OLD projection before they
// change the filter. It is the one path every filter-driven rebuild takes —
// chip removal, clear-all, the profile cycle, entering search, every keystroke
// inside it, and the Esc that leaves it — so none of them can rebuild the list
// without also deciding where the selection went.
//
// Restoring by identity rather than resetting to row 0 is the whole point: a
// filter narrows the list, it does not usually remove what the operator was
// looking at, and a numeric cursor survives a narrowing only by coincidence of
// index. RestoreItemCursor's fallback (the first attention item, then item 0)
// is reached only when the captured item is genuinely gone.
//
// The rows are sorted here directly rather than through sortFiltered because
// reprojectList already performs the rebuild sortFiltered would pair with the
// sort. The invariant sortFiltered exists to hold — no write to m.filtered
// without a reprojection behind it — is exactly what reprojectList is.
//
// The returned command refreshes the preview, and it is returned rather than
// dispatched because the selection has to settle first: the pane's target is
// whatever the cursor points at once the new projection exists. Callers must not
// drop it — a preview left pointed at a window the filter has just hidden keeps
// polling a session that is no longer on screen.
func (m *Model) applyActiveFilter(prior dashboard.CursorIdentity) tea.Cmd {
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	dashboard.SortRows(m.filtered, m.lastActivity, m.idleThreshold)
	return m.reprojectList(prior)
}

// applyFilterChange swaps in a new active filter and re-applies it, carrying the
// current selection across. Used by chip removal and the Esc "clear all" path so
// they share the bookkeeping.
func (m *Model) applyFilterChange(f dashboard.Filter) tea.Cmd {
	prior := dashboard.CaptureItemIdentity(m.items, m.cursor)
	m.activeFilter = f
	return m.applyActiveFilter(prior)
}

// cycleDashboardProfile updates the dashboard's existing @profile filter
// component while preserving all other DSL components.
func (m *Model) cycleDashboardProfile() tea.Cmd {
	next := dashboard.NextProfileFilter(m.sessions, m.activeFilter.Account)
	filter := m.activeFilter
	filter.Account = next
	filter.Raw = dashboard.SerializeFilter(filter)
	return m.applyFilterChange(filter)
}

// enterFilter switches into ModeSearch with the given pre-filled buffer and
// snapshots the current filter so Esc can restore it. The pre-fill is parsed
// and live-applied immediately so the list reflects the buffer on entry.
//
// The selection is snapshotted beside the filter, because Esc restores a state
// the operator was in and that state included which row they were on. Filter and
// selection are captured in the same step for the same reason they are restored
// in one: taking them apart would let a keystroke between them make the pair
// describe a list that never existed.
//
// The mode is set before the reprojection so the scroll math sizes the window
// for the mode the list is about to be drawn in rather than the one it is
// leaving.
func (m *Model) enterFilter(prefill string) tea.Cmd {
	prior := dashboard.CaptureItemIdentity(m.items, m.cursor)
	m.savedFilter = m.activeFilter
	m.savedSelection = prior
	m.filterText = prefill
	m.activeFilter = dashboard.Parse(prefill)
	m.mode = ModeSearch
	cmd := m.applyActiveFilter(prior)
	m.recomputeCompletion()
	return cmd
}

// reapplyFilterLive parses filterText and refreshes the list. Used by every
// keystroke handler in ModeSearch to provide live filtering, so it is the path
// on which a still-visible selection is most often nearly lost: one added
// character reorders the list under a numeric cursor.
func (m *Model) reapplyFilterLive() tea.Cmd {
	prior := dashboard.CaptureItemIdentity(m.items, m.cursor)
	m.activeFilter = dashboard.Parse(m.filterText)
	return m.applyActiveFilter(prior)
}

// sortFiltered orders m.filtered so attention items come first, then active,
// then idle, using the model's activity map and idle threshold to bucket
// sessions with no state icon, then rebuilds the item projection the cursor
// indexes. Sorting and reprojecting are one step because a projection built
// from a pre-sort row order names the wrong line for every row the sort moved.
func (m *Model) sortFiltered() {
	dashboard.SortRows(m.filtered, m.lastActivity, m.idleThreshold)
	m.rebuildItems()
}

// clamp constrains v to [lo, hi]. Returns lo when hi < lo.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
