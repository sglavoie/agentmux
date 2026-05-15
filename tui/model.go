package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/daemon"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// Mode represents the current UI mode.
type Mode int

const (
	ModeList         Mode = iota
	ModeSearch            // typing a filter expression (DSL)
	ModeConfirm           // y/n confirmation for a destructive action
	ModePicker            // multi-select session launcher
	ModeConfigPicker      // account picker for config override
	ModeHelp              // help overlay
	ModeActions           // per-row actions overlay
)

// Model is the root Bubble Tea model for the dashboard.
type Model struct {
	// deps
	client   kitty.KittyClient
	cfg      *config.Config
	interval time.Duration
	sortMode string

	// session state
	sessions []session.RunningSession // sorted full list
	filtered []session.RunningSession // sessions after query+filter applied
	cursor   int

	// mode
	mode Mode

	// filter (unified search + account + state DSL)
	filterText      string // live input while in ModeSearch
	activeFilter    Filter // committed filter applied to filtered
	savedFilter     Filter // snapshot taken on enter; restored on Esc
	focusedChipIdx  int    // -1 when no chip focused; otherwise index into Components()

	// terminal dimensions
	width  int
	height int

	// last error from Kitty query
	err error

	// confirm / config-picker target
	targetSession session.RunningSession

	// confirm mode
	confirmAction      string
	confirmConsequence string

	// picker mode
	pickerSessions    []config.Session
	pickerRunning     map[string]bool
	pickerSelected    map[int]bool
	pickerCursor      int
	pickerFilter      string // live filter buffer (DSL); always active in ModePicker
	pickerFilteredIdx []int  // indices into pickerSessions for the filtered view

	// last error from a close/restart action
	actionErr error

	// config picker mode
	configPickerAccounts []config.Account
	configPickerCursor   int
	configPickerCurrent  string

	// activity tracking: window_id → last activity time, sourced from daemon.
	// Empty when the daemon is unreachable; treated as "Active" by stateGroup.
	lastActivity  map[int]time.Time
	idleThreshold time.Duration

	// preview pane
	previewEnabled       bool
	previewWindowID      int
	previewText          string
	previewError         string
	previewNotice        string
	previewNoticeExpires time.Time

	// toast strip + launch-progress bookkeeping
	toasts          []Toast
	launchBatchID   int
	launchBatchDone int
}

// NewModel creates a dashboard model.
func NewModel(cfg *config.Config, client kitty.KittyClient) Model {
	interval := time.Duration(cfg.Defaults.RefreshInterval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	sort := cfg.Defaults.Sort
	if sort == "" {
		sort = "alpha"
	}
	idle := time.Duration(cfg.Defaults.IdleThreshold) * time.Second
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	return Model{
		client:         client,
		cfg:            cfg,
		interval:       interval,
		sortMode:       sort,
		idleThreshold:  idle,
		focusedChipIdx: -1,
	}
}

// Init starts the refresh ticker and fetches the first session list.
func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.client, m.sortMode), tickCmd(m.interval))
}

// Update handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.previewEnabled {
			cfg := m.cfg.Defaults.Preview
			if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
				m.disablePreviewWithNotice("preview disabled: terminal too small")
			}
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetchCmd(m.client, m.sortMode), tickCmd(m.interval))

	case previewTickMsg:
		if !m.previewEnabled {
			return m, nil
		}
		d := time.Duration(m.cfg.Defaults.Preview.RefreshMs) * time.Millisecond
		return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))

	case previewContentMsg:
		if !m.previewEnabled || msg.windowID != m.previewWindowID {
			return m, nil
		}
		if msg.err != nil {
			m.previewError = msg.err.Error()
		} else {
			m.previewError = ""
			m.previewText = msg.text
		}
		return m, nil

	case sessionsMsg:
		m.err = nil
		// Capture the prior cursor's row identity before the list is
		// rebuilt so we can restore it post-refresh. Empty list → no
		// prior selection to preserve.
		var priorBase string
		if len(m.filtered) > 0 && m.cursor >= 0 && m.cursor < len(m.filtered) {
			priorBase = m.filtered[m.cursor].Base
		}
		m.lastActivity = msg.activity
		m.sessions = msg.sessions
		m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
		m.sortFiltered()
		m.cursor = restoreCursor(m.filtered, priorBase, m.lastActivity, m.idleThreshold)
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case actionDoneMsg:
		m.actionErr = msg.err
		// Trigger a refresh so the list reflects the new state.
		return m, fetchCmd(m.client, m.sortMode)

	case launchProgressMsg:
		return m.handleLaunchProgress(msg)

	case toastTickMsg:
		return m, m.sweepToasts()

	case pickerOpenDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
		}
		return m, fetchCmd(m.client, m.sortMode)

	case focusDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
			return m, nil
		}
		return m, tea.Quit

	case peekDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey dispatches key events by current mode.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeSearch:
		return m.handleSearchKey(msg)
	case ModeConfirm:
		return m.handleConfirmKey(msg)
	case ModePicker:
		return m.handlePickerKey(msg)
	case ModeConfigPicker:
		return m.handleConfigPickerKey(msg)
	case ModeHelp:
		return m.handleHelpKey(msg)
	case ModeActions:
		return m.handleActionsKey(msg)
	default:
		return m.handleListKey(msg)
	}
}

func (m Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC, keyQ:
		return m, tea.Quit

	case keyJ, keyDown:
		m.cursor = clamp(m.cursor+1, 0, len(m.filtered)-1)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyK, keyUp:
		m.cursor = clamp(m.cursor-1, 0, len(m.filtered)-1)
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyTogglePreview:
		return m.togglePreview()

	case keyG:
		m.cursor = 0
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keyGG:
		m.cursor = len(m.filtered) - 1
		if m.cursor < 0 {
			m.cursor = 0
		}
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}

	case keySlash:
		m.enterFilter("")

	case keyF:
		m.enterFilter("@")

	case keyE:
		if len(m.filtered) > 0 {
			s := m.filtered[m.cursor]
			return m, peekCmd(m.client, s.WindowID)
		}

	case keyEnter:
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
			return m, nil
		}
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.actionErr = nil
			m.mode = ModeActions
		}

	case keySpace:
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

	case keyO:
		if len(m.filtered) > 0 {
			s := m.filtered[m.cursor]
			if cs, ok := findConfigSessionByBase(m.cfg, s.Base); ok {
				m.targetSession = s
				m.actionErr = nil
				return m, openDirActionCmd(m, cs)
			}
		}

	case keyY:
		if len(m.filtered) > 0 {
			title := m.filtered[m.cursor].Base
			m.actionErr = nil
			return m, copyTitleCmd(title)
		}

	case keyTab:
		if n := len(m.activeFilter.Components()); n > 0 {
			if m.focusedChipIdx < 0 {
				m.focusedChipIdx = 0
			} else {
				m.focusedChipIdx = (m.focusedChipIdx + 1) % n
			}
		}

	case key1, key2, key3, key4, key5, key6, key7, key8, key9:
		idx := int(msg.String()[0] - '1')
		if idx < len(m.activeFilter.Components()) {
			m.applyFilterChange(m.activeFilter.RemoveComponent(idx))
		}

	case keyX:
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.confirmAction = "close"
			m.confirmConsequence = closeConsequence
			m.actionErr = nil
			m.mode = ModeConfirm
		}

	case keyR:
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.confirmAction = "restart"
			m.confirmConsequence = restartConsequence
			m.actionErr = nil
			m.mode = ModeConfirm
		}

	case keyC:
		if len(m.filtered) > 0 {
			m.targetSession = m.filtered[m.cursor]
			m.configPickerAccounts = m.cfg.Accounts
			m.configPickerCurrent = m.targetSession.Account
			m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.Account)
			m.actionErr = nil
			m.mode = ModeConfigPicker
		}

	case keyClearStatus:
		if len(m.filtered) > 0 {
			s := m.filtered[m.cursor]
			m.actionErr = nil
			return m, clearStatusCmd(m.client, s)
		}

	case keyQuestion:
		m.mode = ModeHelp

	case keyL:
		m.pickerSessions = sortPickerSessions(m.cfg.Sessions)
		m.pickerRunning = buildPickerRunning(m.sessions)
		m.pickerSelected = make(map[int]bool)
		m.pickerCursor = 0
		m.pickerFilter = ""
		m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
		m.mode = ModePicker
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}

	case keyEsc:
		// Clear active filter (and any chip focus).
		m.applyFilterChange(Filter{})
	}

	return m, nil
}

// applyFilterChange swaps in a new active filter, re-applies it to the
// session list, and resets cursor + chip focus. Used by chip removal and
// the Esc "clear all" path so they share the bookkeeping.
func (m *Model) applyFilterChange(f Filter) {
	m.activeFilter = f
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = 0
	m.focusedChipIdx = -1
}

// enterFilter switches into ModeSearch with the given pre-filled buffer and
// snapshots the current filter so Esc can restore it. The pre-fill is parsed
// and live-applied immediately so the list reflects the buffer on entry.
func (m *Model) enterFilter(prefill string) {
	m.savedFilter = m.activeFilter
	m.filterText = prefill
	m.activeFilter = Parse(prefill)
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = 0
	m.focusedChipIdx = -1
	m.mode = ModeSearch
}

// reapplyFilterLive parses filterText and refreshes the list. Used by every
// keystroke handler in ModeSearch to provide live filtering.
func (m *Model) reapplyFilterLive() {
	m.activeFilter = Parse(m.filterText)
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = clamp(m.cursor, 0, len(m.filtered)-1)
	m.focusedChipIdx = -1
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		// Cancel: restore the snapshot taken on entry and recompute.
		m.activeFilter = m.savedFilter
		m.filterText = m.savedFilter.Raw
		m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
		m.sortFiltered()
		m.cursor = 0
		m.focusedChipIdx = -1
		m.mode = ModeList

	case keyEnter:
		// Live-applied already — just exit the input.
		m.mode = ModeList

	case keyBackspace:
		if r := []rune(m.filterText); len(r) > 0 {
			m.filterText = string(r[:len(r)-1])
		}
		m.reapplyFilterLive()

	default:
		if t := msg.Key().Text; t != "" {
			m.filterText += t
			m.reapplyFilterLive()
		}
	}

	return m, nil
}

func (m Model) handleConfigPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		m.mode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.configPickerCursor = clamp(m.configPickerCursor+1, 0, len(m.configPickerAccounts)-1)
	case keyK, keyUp:
		m.configPickerCursor = clamp(m.configPickerCursor-1, 0, len(m.configPickerAccounts)-1)
	case keyEnter:
		if m.configPickerCursor < len(m.configPickerAccounts) {
			acc := m.configPickerAccounts[m.configPickerCursor]
			m.mode = ModeList
			if acc.Name == m.configPickerCurrent {
				return m, nil
			}
			return m, m.restartWithAccountCmd(acc)
		}
	}
	return m, nil
}

// handleActionsKey dispatches the per-row actions overlay. Esc/q close the
// overlay without side effects; any other key that matches an available
// action's keyMatch runs it. Unavailable or unknown keys are ignored.
func (m Model) handleActionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case keyEsc, keyQ:
		m.mode = ModeList
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	}
	for _, a := range actionsFor(m) {
		if s != a.keyMatch {
			continue
		}
		if !a.available(m) {
			return m, nil
		}
		return a.run(m)
	}
	return m, nil
}

func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQuestion, keyQ:
		m.mode = ModeList
	case keyCtrlC:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyY:
		m.mode = ModeList
		switch m.confirmAction {
		case "close":
			return m, closeCmd(m.client, m.targetSession.TabID)
		case "restart":
			return m, restartCmd(m.client, m.targetSession, m.cfg)
		}
	case keyN, keyEsc:
		m.mode = ModeList
	case keyCtrlC, keyQ:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		// Two-stage Esc: first clears a non-empty buffer; second exits.
		if m.pickerFilter != "" {
			m.pickerFilter = ""
			m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
			m.pickerCursor = 0
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}
		m.mode = ModeList
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.pickerCursor = clamp(m.pickerCursor+1, 0, len(m.pickerFilteredIdx)-1)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyK, keyUp:
		m.pickerCursor = clamp(m.pickerCursor-1, 0, len(m.pickerFilteredIdx)-1)
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyTogglePreview:
		return m.togglePreview()
	case keySpace:
		if len(m.pickerFilteredIdx) == 0 {
			return m, nil
		}
		if m.pickerSelected == nil {
			m.pickerSelected = make(map[int]bool)
		}
		origIdx := m.pickerFilteredIdx[m.pickerCursor]
		m.pickerSelected[origIdx] = !m.pickerSelected[origIdx]
		return m, nil
	case keyO:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			return m, pickerOpenCmd(m, m.pickerSessions[origIdx])
		}
		return m, nil
	case keyEnter:
		cmd := m.dispatchPickerLaunch()
		m.mode = ModeList
		m.pickerSelected = nil
		return m, cmd
	case keyBackspace:
		if r := []rune(m.pickerFilter); len(r) > 0 {
			m.pickerFilter = string(r[:len(r)-1])
			m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, m.pickerFilter)
			m.pickerCursor = 0
			if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
				return m, cmd
			}
		}
		return m, nil
	}

	// Default: live-append printable characters to the filter buffer. The
	// always-on filter means typing has no other meaning in the picker
	// (toggle is space, navigation is j/k, launch is enter, preview is p).
	if t := msg.Key().Text; t != "" {
		m.pickerFilter += t
		m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, m.pickerFilter)
		m.pickerCursor = 0
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}
	}
	return m, nil
}

// View renders the full dashboard.
func (m Model) View() tea.View {
	content := m.render()
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// render builds the string content for View.
func (m Model) render() string {
	var sb strings.Builder

	switch m.mode {
	case ModePicker:
		// Picker replaces both header and list.
		sb.WriteString(renderPicker(m))
	default:
		// Header area varies by mode; list is always shown below.
		switch m.mode {
		case ModeSearch:
			sb.WriteString(renderSearch(m))
		case ModeConfirm:
			sb.WriteString(renderConfirm(m))
		case ModeConfigPicker:
			sb.WriteString(renderConfigPicker(m))
		case ModeHelp:
			sb.WriteString(renderHelp(m))
		case ModeActions:
			sb.WriteString(renderActions(m))
		default:
			sb.WriteString(renderHeader(m))
		}
		if m.mode == ModeList {
			sb.WriteString(renderChips(m, m.width))
		}
		if m.mode == ModeList && m.previewEnabled {
			sb.WriteString(renderSplit(m))
		} else {
			sb.WriteString(renderList(m))
		}
	}

	sb.WriteByte('\n')

	sb.WriteString(renderToasts(m))
	sb.WriteString(renderFooter(m))

	return sb.String()
}

// renderSplit renders the list and preview pane side-by-side or stacked
// according to the configured direction. Falls back to the plain list when
// dimensions or selection make the preview unusable.
func renderSplit(m Model) string {
	cfg := m.cfg.Defaults.Preview
	footerLines := previewFooterLines(m)
	// Vertical space available for the list+preview area (header consumes 1
	// line, then a blank line separates the content from the footer).
	paneHeight := m.height - 1 - 1 - footerLines
	if paneHeight < 4 || m.width < 20 {
		return renderList(m)
	}

	var sess session.RunningSession
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		sess = m.filtered[m.cursor]
	}
	acctColor := buildAccountColorMap(m)

	if cfg.Direction == "vertical" {
		previewHeight := int(float64(paneHeight) * cfg.Ratio)
		if previewHeight < 3 {
			previewHeight = 3
		}
		if previewHeight > paneHeight-2 {
			previewHeight = paneHeight - 2
		}
		listHeight := paneHeight - previewHeight - 1
		listView := clipHeight(renderListAt(m, m.width), listHeight)
		divider := styleDim.Render(strings.Repeat("─", m.width))
		previewView := renderPreview(m.width, previewHeight, sess, m.previewText, m.previewError, acctColor)
		return lipgloss.JoinVertical(lipgloss.Left, listView, divider, previewView) + "\n"
	}

	previewWidth := int(float64(m.width) * cfg.Ratio)
	if previewWidth < 20 {
		previewWidth = 20
	}
	if previewWidth > m.width-20 {
		previewWidth = m.width - 20
	}
	listWidth := m.width - previewWidth - 1
	listView := clipHeight(renderListAt(m, listWidth), paneHeight)
	previewView := renderPreview(previewWidth, paneHeight, sess, m.previewText, m.previewError, acctColor)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listView, divider, previewView) + "\n"
}

// clipHeight forces s to exactly n visual lines: extra lines are dropped and
// missing lines are padded with empty strings.
func clipHeight(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// previewFooterLines returns the number of lines the footer will occupy in
// the current mode (used for layout math and resize fits-check).
func previewFooterLines(m Model) int {
	if m.mode == ModePicker {
		// Picker footer is always two lines regardless of preview state.
		return 2
	}
	if m.mode != ModeList {
		return 2
	}
	noticeActive := m.previewNotice != "" &&
		(m.previewNoticeExpires.IsZero() || time.Now().Before(m.previewNoticeExpires))
	if m.previewEnabled {
		// Footer keybindings are suppressed; only the notice (if any) takes a line.
		if noticeActive {
			return 1
		}
		return 0
	}
	if noticeActive {
		return 3
	}
	return 2
}

// togglePreview flips the preview pane on/off. Returns the model and any cmd
// (immediate fetch + ticker) to dispatch on enable.
func (m Model) togglePreview() (tea.Model, tea.Cmd) {
	if m.previewEnabled {
		m.previewEnabled = false
		m.previewText = ""
		m.previewError = ""
		m.previewWindowID = 0
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		m.previewNotice = "preview unavailable: terminal too small"
		m.previewNoticeExpires = time.Now().Add(2 * time.Second)
		return m, nil
	}
	m.previewEnabled = true
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = m.currentPreviewWindowID()
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
}

// currentPreviewWindowID returns the window ID the preview should track for
// the current cursor in the active mode. ModePicker resolves through the
// picker cursor (matching the highlighted config session to a running one);
// every other mode uses the dashboard cursor.
func (m Model) currentPreviewWindowID() int {
	if m.mode == ModePicker {
		return m.pickerCurrentWindowID()
	}
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor].WindowID
	}
	return 0
}

// pickerCurrentWindowID returns the WindowID of the running session matching
// the picker's highlighted config row, or 0 when nothing matches.
func (m Model) pickerCurrentWindowID() int {
	if len(m.pickerFilteredIdx) == 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return 0
	}
	origIdx := m.pickerFilteredIdx[m.pickerCursor]
	sess := m.pickerSessions[origIdx]
	baseTitle := session.FormatTitle(sess)
	for _, rs := range m.sessions {
		if rs.Base == baseTitle {
			return rs.WindowID
		}
	}
	return 0
}

// refreshPreviewForPickerCursor returns an immediate fetch cmd when the
// picker cursor now points at a different running session (or none).
// Returns nil when preview is off or the window hasn't changed.
func (m *Model) refreshPreviewForPickerCursor() tea.Cmd {
	if !m.previewEnabled {
		return nil
	}
	winID := m.pickerCurrentWindowID()
	if winID == m.previewWindowID {
		return nil
	}
	m.previewWindowID = winID
	m.previewText = ""
	m.previewError = ""
	return fetchPreviewCmd(m.client, winID)
}

// refreshPreviewForCursor returns an immediate fetch cmd when the cursor
// has moved to a session backed by a different window. Returns nil when
// preview is off or the selection still points at the same window.
func (m *Model) refreshPreviewForCursor() tea.Cmd {
	if !m.previewEnabled {
		return nil
	}
	var winID int
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		winID = m.filtered[m.cursor].WindowID
	}
	if winID == m.previewWindowID {
		return nil
	}
	m.previewWindowID = winID
	m.previewText = ""
	m.previewError = ""
	return fetchPreviewCmd(m.client, winID)
}

// disablePreviewWithNotice turns the preview off and shows a transient footer
// notice. Used both by the resize guard and any other auto-disable path.
func (m *Model) disablePreviewWithNotice(msg string) {
	m.previewEnabled = false
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = 0
	m.previewNotice = msg
	m.previewNoticeExpires = time.Now().Add(2 * time.Second)
}

// fetchCmd queries Kitty for running sessions and the daemon for per-window
// last-activity timestamps. Daemon errors are non-fatal — when unreachable
// the activity map is nil and stateGroup treats every session as Active.
func fetchCmd(client kitty.KittyClient, sortMode string) tea.Cmd {
	return func() tea.Msg {
		sessions, err := session.List(client)
		if err != nil {
			return errMsg{err: err}
		}
		return sessionsMsg{
			sessions: session.SortRunning(sessions, sortMode),
			activity: fetchActivity(),
		}
	}
}

// fetchActivity queries the daemon for last-activity timestamps. Returns nil
// when the daemon is unreachable or the query fails — callers must tolerate.
func fetchActivity() map[int]time.Time {
	resp, err := daemon.NewClient().Send(daemon.Request{Type: daemon.TypeStateQuery})
	if err != nil || resp.Activity == nil {
		return nil
	}
	out := make(map[int]time.Time, len(resp.Activity))
	for id, secs := range resp.Activity {
		out[id] = time.Unix(secs, 0)
	}
	return out
}

// clearStatusCmd wipes a session's tab-title icon (via Kitty) and asks the
// daemon to drop its guard via the user_clear action. The Kitty title write
// is the user-visible result; daemon errors are swallowed because the
// dashboard already updated the title directly.
func clearStatusCmd(client kitty.KittyClient, s session.RunningSession) tea.Cmd {
	return func() tea.Msg {
		newTitle := palette.BuildTitle(s.Base, "", s.Starred)
		err := client.SetTabTitle(kitty.MatchTabByWindowID(s.WindowID), newTitle)
		_, _ = daemon.NewClient().Send(daemon.Request{
			Type:         daemon.TypeStateUpdate,
			Action:       "user_clear",
			WindowID:     s.WindowID,
			SessionTitle: s.Base,
		})
		return actionDoneMsg{err: err}
	}
}

// focusCmd focuses a tab by ID and returns focusDoneMsg.
func focusCmd(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		return focusDoneMsg{err: client.FocusTab(kitty.MatchTabByID(tabID))}
	}
}

// closeCmd closes a tab by ID and returns actionDoneMsg.
func closeCmd(client kitty.KittyClient, tabID int) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: session.Close(client, tabID)}
	}
}

// restartCmd closes and relaunches a session and returns actionDoneMsg.
func restartCmd(client kitty.KittyClient, running session.RunningSession, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{err: session.Restart(client, running, cfg)}
	}
}

// restartWithAccountCmd closes and relaunches a session under a different account.
func (m Model) restartWithAccountCmd(acc config.Account) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = actionDoneMsg{err: fmt.Errorf("panic in restartWithAccount: %v", r)}
			}
		}()
		return actionDoneMsg{err: session.RestartWithAccount(m.client, m.targetSession, acc, m.cfg)}
	}
}

// dispatchPickerLaunch starts a multi-session launch and returns a batch of
// per-session commands. Each command emits a launchProgressMsg the model
// uses to drive the progress toast (1/N, 2/N, …). Returns nil when no
// non-running session is selected so the caller doesn't dispatch a dud cmd.
func (m *Model) dispatchPickerLaunch() tea.Cmd {
	type queued struct {
		sess  config.Session
		title string
	}
	var todo []queued
	for i, sess := range m.pickerSessions {
		if !m.pickerSelected[i] {
			continue
		}
		baseTitle := session.FormatTitle(sess)
		if m.pickerRunning[baseTitle] {
			continue // dedup: skip already-running sessions
		}
		todo = append(todo, queued{sess: sess, title: baseTitle})
	}
	if len(todo) == 0 {
		return nil
	}

	m.launchBatchID++
	batchID := m.launchBatchID
	total := len(todo)
	m.launchBatchDone = 0

	cmds := make([]tea.Cmd, 0, total+1)
	startText := fmt.Sprintf("Launching %d session(s)…", total)
	if c := m.replaceToastByID(batchID, Toast{
		Severity: toastInfo,
		Text:     startText,
		Expires:  time.Now().Add(progressToastTTL),
	}); c != nil {
		cmds = append(cmds, c)
	}

	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	for _, q := range todo {
		sess := q.sess
		title := q.title
		cmds = append(cmds, func() tea.Msg {
			var acc config.Account
			for _, a := range accounts {
				if a.Name == sess.Account {
					acc = a
					break
				}
			}
			err := session.Launch(client, sess, acc, cfg)
			return launchProgressMsg{batchID: batchID, total: total, title: title, err: err}
		})
	}
	return tea.Sequence(cmds...)
}

// handleLaunchProgress updates the progress toast for a finished
// per-session launch and triggers a session list refresh so the newly
// running session appears.
func (m Model) handleLaunchProgress(msg launchProgressMsg) (tea.Model, tea.Cmd) {
	if msg.batchID != m.launchBatchID {
		// Stale launch from a prior batch; ignore so we don't clobber a
		// fresh batch's toast.
		return m, fetchCmd(m.client, m.sortMode)
	}
	m.launchBatchDone++
	var cmds []tea.Cmd
	if msg.err != nil {
		if c := m.pushToast(Toast{
			Severity: toastError,
			Text:     fmt.Sprintf("launch failed: %s — %s", msg.title, msg.err),
		}); c != nil {
			cmds = append(cmds, c)
		}
	}
	severity := toastInfo
	if m.launchBatchDone >= msg.total {
		severity = toastSuccess
	}
	text := fmt.Sprintf("Launched %d/%d · %s", m.launchBatchDone, msg.total, msg.title)
	expires := time.Now().Add(progressToastTTL)
	if m.launchBatchDone >= msg.total {
		expires = time.Now().Add(defaultToastTTL)
	}
	if c := m.replaceToastByID(msg.batchID, Toast{
		Severity: severity,
		Text:     text,
		Expires:  expires,
	}); c != nil {
		cmds = append(cmds, c)
	}
	cmds = append(cmds, fetchCmd(m.client, m.sortMode))
	return m, tea.Batch(cmds...)
}

// restoreCursor picks the cursor row after the session list is rebuilt.
// Preference order: (1) the row whose Base matches priorBase, (2) the
// first attention-state row (orange band), (3) row 0. Returns 0 for an
// empty list so callers can use the result directly without clamping.
func restoreCursor(filtered []session.RunningSession, priorBase string, lastActivity map[int]time.Time, idle time.Duration) int {
	if len(filtered) == 0 {
		return 0
	}
	if priorBase != "" {
		for i, s := range filtered {
			if s.Base == priorBase {
				return i
			}
		}
	}
	for i, s := range filtered {
		if stateGroup(s, lastActivity, idle) == 0 {
			return i
		}
	}
	return 0
}

// pickerOpenCmd opens the given session's directory in a plain shell tab.
func pickerOpenCmd(m Model, sess config.Session) tea.Cmd {
	accounts := m.cfg.Accounts
	client := m.client
	cfg := m.cfg
	return func() tea.Msg {
		var acc config.Account
		for _, a := range accounts {
			if a.Name == sess.Account {
				acc = a
				break
			}
		}
		return pickerOpenDoneMsg{err: session.OpenDirectory(client, sess, acc, cfg)}
	}
}

// applyFilter returns sessions that pass the active Filter.
func applyFilter(sessions []session.RunningSession, f Filter, lastActivity map[int]time.Time, idle time.Duration) []session.RunningSession {
	if f.IsEmpty() {
		out := make([]session.RunningSession, len(sessions))
		copy(out, sessions)
		return out
	}
	var out []session.RunningSession
	for _, s := range sessions {
		if f.Match(s, lastActivity, idle) {
			out = append(out, s)
		}
	}
	return out
}

// applyPickerFilter returns indices into sessions matching the DSL query.
// Reuses the list-filter DSL (@account, !state, substring) so the same
// shortcut behaves the same in both contexts. An empty query returns the full
// index range. Config sessions are matched as synthetic running sessions —
// !state filters won't match anything since config rows have no live state.
func applyPickerFilter(sessions []config.Session, query string) []int {
	out := make([]int, 0, len(sessions))
	if strings.TrimSpace(query) == "" {
		for i := range sessions {
			out = append(out, i)
		}
		return out
	}
	f := Parse(query)
	for i, cs := range sessions {
		synthetic := session.RunningSession{
			Base:      session.FormatTitle(cs),
			Account:   cs.Account,
			Directory: cs.Directory,
		}
		if f.Match(synthetic, nil, 0) {
			out = append(out, i)
		}
	}
	return out
}

// pickerAccountIndex returns the index of the named account in the slice, or 0 if not found.
func pickerAccountIndex(accounts []config.Account, name string) int {
	for i, a := range accounts {
		if a.Name == name {
			return i
		}
	}
	return 0
}

// peekCmd launches a Kitty overlay pager for the given window's scrollback.
func peekCmd(client kitty.KittyClient, windowID int) tea.Cmd {
	return func() tea.Msg {
		return peekDoneMsg{err: client.LaunchOverlayPager(kitty.MatchWindowByID(windowID))}
	}
}

// sortFiltered applies a stable sort to m.filtered so attention items come
// first, then active, then idle — preserving existing order within each
// group. Uses the model's activity map + idle threshold to bucket sessions
// with no state icon.
func (m Model) sortFiltered() {
	sort.SliceStable(m.filtered, func(i, j int) bool {
		gi := stateGroup(m.filtered[i], m.lastActivity, m.idleThreshold)
		gj := stateGroup(m.filtered[j], m.lastActivity, m.idleThreshold)
		return gi < gj
	})
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
