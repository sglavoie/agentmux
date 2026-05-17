package tui

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// Health-pip refresh cadences. Daemon checks are a cheap socket round-trip
// (5s), hook checks read each profile's settings.json so they run on a
// longer interval (15s).
const (
	daemonHealthInterval = 5 * time.Second
	hookHealthInterval   = 15 * time.Second
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
	typeBuffer      string // live input from ModeList's type-to-filter dispatch
	activeFilter    Filter // committed filter applied to filtered
	savedFilter     Filter // snapshot taken on enter; restored on Esc
	focusedChipIdx  int    // -1 when no chip focused; otherwise index into Components()
	completion      completionState // sigil-aware popup state for ModeSearch / type-to-filter

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
	pickerFilter        string // live filter buffer (DSL)
	pickerFilteredIdx   []int  // indices into pickerSessions for the filtered view
	pickerSearchMode    bool   // true while / is active and filter input is open
	pickerPreviewEnabled bool  // picker-local preview toggle, independent of ModeList

	// last error from a close/restart action
	actionErr error

	// config picker mode
	configPickerAccounts []config.Account
	configPickerCursor   int
	configPickerCurrent  string
	configPickerReturnMode Mode
	configPickerProjectIdx int

	// picker launch overrides: project index -> profile name
	pickerProfileOverride map[int]string

	// activity tracking: window_id → last activity time, sourced from daemon.
	// Empty when the daemon is unreachable; treated as "Active" by stateGroup.
	lastActivity  map[int]time.Time
	idleThreshold time.Duration

	// recency-pulse bookkeeping. lastSeenStates is the snapshot of every
	// session's state at the last user input in ModeList (or the first
	// refresh after startup). recentTransitions records when a session's
	// state was first observed to differ from lastSeenStates so the list
	// can highlight it for a short window — see pulseDecay.
	lastSeenStates    map[int]string
	recentTransitions map[int]time.Time

	// health pip + deprecation banner bookkeeping. daemonAlive and
	// hookHealth are refreshed at the daemonHealthInterval /
	// hookHealthInterval cadences via healthCmd; deprecations is captured
	// once at startup from cfg.Deprecations.
	amxBin              string
	daemonAlive         bool
	daemonHealthCheckedAt time.Time
	hookHealth          HookHealth
	hookHealthErr       error
	hookHealthCheckedAt time.Time
	deprecations         []config.Deprecation
	deprecationDismissed bool

	// preview pane
	previewEnabled       bool
	previewWindowID      int
	previewText          string
	previewError         string
	previewNotice        string
	previewNoticeExpires time.Time

	// toast strip + launch-progress bookkeeping
	toasts          []Toast
	launchBatchID     int
	launchBatchDone   int
	launchBatchErrors int

	// pendingUndo stacks the most recent destructive actions eligible for
	// `u`-driven undo. Entries expire at their deadline; sweepUndo trims
	// them on toast ticks so memory stays bounded.
	pendingUndo []undoEntry
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
	amxBin, err := os.Executable()
	if err != nil {
		amxBin = "amx"
	}
	return Model{
		client:         client,
		cfg:            cfg,
		interval:       interval,
		sortMode:       sort,
		idleThreshold:  idle,
		focusedChipIdx: -1,
		amxBin:         amxBin,
		deprecations:   cfg.Deprecations,
	}
}

// Init starts the refresh ticker and fetches the first session list.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchCmd(m.client, m.sortMode),
		tickCmd(m.interval),
		healthCmd(m.cfg, m.amxBin, true, true),
	)
}

// Update handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cfg := m.cfg.Defaults.Preview
		if m.previewEnabled {
			if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
				m.disablePreviewWithNotice("preview disabled: terminal too small")
			}
		}
		if m.pickerPreviewEnabled {
			if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
				m.pickerPreviewEnabled = false
				m.previewText = ""
				m.previewError = ""
				m.previewWindowID = 0
				m.previewNotice = "preview disabled: terminal too small"
				m.previewNoticeExpires = time.Now().Add(2 * time.Second)
			}
		}
		return m, nil

	case tickMsg:
		m.sweepRecentTransitions()
		cmds := []tea.Cmd{fetchCmd(m.client, m.sortMode), tickCmd(m.interval)}
		now := time.Now()
		checkDaemon := now.Sub(m.daemonHealthCheckedAt) >= daemonHealthInterval
		checkHooks := now.Sub(m.hookHealthCheckedAt) >= hookHealthInterval
		if checkDaemon || checkHooks {
			cmds = append(cmds, healthCmd(m.cfg, m.amxBin, checkDaemon, checkHooks))
		}
		return m, tea.Batch(cmds...)

	case healthMsg:
		now := time.Now()
		if msg.daemonChecked {
			m.daemonAlive = msg.daemonAlive
			m.daemonHealthCheckedAt = now
		}
		if msg.hookChecked {
			m.hookHealth = msg.hookSummary
			m.hookHealthErr = msg.hookErr
			m.hookHealthCheckedAt = now
		}
		return m, nil

	case previewTickMsg:
		if !m.activePreviewEnabled() {
			return m, nil
		}
		d := time.Duration(m.cfg.Defaults.Preview.RefreshMs) * time.Millisecond
		return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))

	case previewContentMsg:
		if !m.activePreviewEnabled() || msg.windowID != m.previewWindowID {
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
		m.recordTransitions()
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
		m.sweepUndo()
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
	m.snapshotSeenStates()
	key := msg.String()

	// Completion popup (active over the type-to-filter buffer) intercepts a
	// fixed key set before the normal dispatcher runs. Mirrors handleSearchKey.
	if m.completion.Visible {
		switch key {
		case keyEsc:
			m.closeCompletion()
			return m, nil
		case keyEnter:
			m.insertCompletionInto(&m.typeBuffer)
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
			return m, nil
		case keyTab, keyDown:
			m.cycleCompletion(1)
			return m, nil
		case keyUp:
			m.cycleCompletion(-1)
			return m, nil
		}
	}

	bufferActive := m.typeBuffer != ""

	switch key {
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
		// Carry an in-progress type-to-filter buffer over to ModeSearch so
		// users escalating to the full DSL don't lose what they've typed.
		prefill := ""
		if m.typeBuffer != "" {
			prefill = m.typeBuffer
			m.typeBuffer = ""
		}
		m.enterFilter(prefill)

	case keyF:
		m.typeBuffer = ""
		m.enterFilter("@")

	case keyEnter:
		if m.typeBuffer != "" {
			// Commit the type-to-filter buffer: the filter is already
			// live-applied; clearing the buffer returns row-action keys to
			// service while the chips above the list reflect the commit.
			m.typeBuffer = ""
			m.closeCompletion()
			return m, nil
		}
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
			return m, nil
		}
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
		}

	case keySpace:
		if len(m.filtered) > 0 {
			return m, focusCmd(m.client, m.filtered[m.cursor].TabID)
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
		idx := int(key[0] - '1')
		if idx < len(m.activeFilter.Components()) {
			m.applyFilterChange(m.activeFilter.RemoveComponent(idx))
		}

	case keyBackspace:
		if m.typeBuffer != "" {
			if r := []rune(m.typeBuffer); len(r) > 0 {
				m.typeBuffer = string(r[:len(r)-1])
			}
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
			return m, nil
		}
		if m.focusedChipIdx >= 0 {
			m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
		}

	case keyEsc:
		if m.typeBuffer != "" {
			m.typeBuffer = ""
			m.closeCompletion()
		}
		// Clear active filter (and any chip focus) regardless. After a
		// type-buffer was cleared above, this also clears the now-committed
		// filter chips so a single esc returns the user to a clean list.
		m.applyFilterChange(Filter{})

	case keyQuestion:
		// typeBuffer policy on mode transitions: `?` (help) and `l` (picker)
		// both *preserve* the buffer and any visible completion popup so
		// returning to ModeList resumes exactly where the user left off.
		// `/` carries the buffer forward as prefill (above); `f`/`esc`/`enter`
		// each clear it explicitly. ModeHelp and ModePicker have their own
		// render paths, so the popup state lives dormant until ModeList
		// becomes active again.
		m.mode = ModeHelp

	case keyL:
		// See typeBuffer policy comment on the `?` case above.
		m.pickerSessions = sortPickerSessions(m.cfg.Sessions)
		m.pickerRunning = buildPickerRunning(m.sessions)
		m.pickerSelected = make(map[int]bool)
		m.pickerProfileOverride = loadPickerOverrides(m.pickerSessions)
		m.pickerCursor = 0
		m.pickerFilter = ""
		m.pickerSearchMode = false
		m.pickerFilteredIdx = applyPickerFilter(m.pickerSessions, "")
		m.mode = ModePicker
		if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
			return m, cmd
		}

	default:
		// Row-action keys: handled normally when the type buffer is empty;
		// diverted into the buffer once it has at least one character.
		if !bufferActive {
			switch key {
			case keyTogglePreview:
				return m.togglePreview()
			case keyA:
				if len(m.filtered) > 0 {
					m.targetSession = m.filtered[m.cursor]
					m.actionErr = nil
					m.mode = ModeActions
				}
				return m, nil
			case keyE:
				if len(m.filtered) > 0 {
					s := m.filtered[m.cursor]
					return m, peekCmd(m.client, s.WindowID)
				}
				return m, nil
			case keyR:
				if len(m.filtered) > 0 {
					m.targetSession = m.filtered[m.cursor]
					m.actionErr = nil
					if m.cfg.Defaults.ConfirmDestructive {
						m.confirmAction = "restart"
						m.confirmConsequence = restartConsequence
						m.mode = ModeConfirm
						return m, nil
					}
					return m, m.restartWithToast(m.targetSession)
				}
				return m, nil
			case keyX:
				if m.focusedChipIdx >= 0 {
					m.applyFilterChange(m.activeFilter.RemoveComponent(m.focusedChipIdx))
					return m, nil
				}
				if len(m.filtered) > 0 {
					m.targetSession = m.filtered[m.cursor]
					m.actionErr = nil
					if m.cfg.Defaults.ConfirmDestructive {
						m.confirmAction = "close"
						m.confirmConsequence = closeConsequence
						m.mode = ModeConfirm
						return m, nil
					}
					return m, m.closeWithToast(m.targetSession)
				}
				return m, nil
			case keyU:
				return m, m.undoLastCmd()
			case keyC:
				if len(m.filtered) > 0 {
					m.targetSession = m.filtered[m.cursor]
					m.configPickerAccounts = m.cfg.Accounts
					m.configPickerCurrent = m.targetSession.EffectiveProfile()
					m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.targetSession.EffectiveProfile())
					m.configPickerReturnMode = ModeList
					m.configPickerProjectIdx = -1
					m.actionErr = nil
					m.mode = ModeConfigPicker
				}
				return m, nil
			case keyClearStatus:
				if len(m.filtered) > 0 {
					s := m.filtered[m.cursor]
					m.actionErr = nil
					return m, clearStatusCmd(m.client, s)
				}
				return m, nil
			case keyO:
				if len(m.filtered) > 0 {
					s := m.filtered[m.cursor]
					if cs, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject()); ok {
						m.targetSession = s
						m.actionErr = nil
						return m, openDirActionCmd(m, cs)
					}
				}
				return m, nil
			case keyY:
				if len(m.filtered) > 0 {
					title := m.filtered[m.cursor].Base
					m.actionErr = nil
					return m, copyTitleCmd(title)
				}
				return m, nil
			case keyD:
				if !m.deprecationDismissed && len(m.deprecations) > 0 {
					m.deprecationDismissed = true
				}
				return m, nil
			}
		}
		// Fall through: anything else printable goes into the type buffer.
		if t := msg.Key().Text; t != "" {
			m.typeBuffer += t
			m.applyTypeBuffer()
			m.recomputeCompletionFor(m.typeBuffer)
		}
	}

	return m, nil
}

// applyTypeBuffer parses the current type-to-filter buffer and re-applies the
// resulting Filter to the session list. Mirrors reapplyFilterLive for the
// ModeList code path.
func (m *Model) applyTypeBuffer() {
	m.activeFilter = Parse(m.typeBuffer)
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = clamp(m.cursor, 0, len(m.filtered)-1)
	m.focusedChipIdx = -1
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
	m.recomputeCompletion()
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
	key := msg.String()
	if m.completion.Visible {
		switch key {
		case keyEsc:
			m.closeCompletion()
			return m, nil
		case keyEnter:
			m.insertCompletion()
			m.reapplyFilterLive()
			m.recomputeCompletion()
			return m, nil
		case keyTab, keyDown:
			m.cycleCompletion(1)
			return m, nil
		case keyUp:
			m.cycleCompletion(-1)
			return m, nil
		}
	}

	switch key {
	case keyEsc:
		// Cancel: restore the snapshot taken on entry and recompute.
		m.activeFilter = m.savedFilter
		m.filterText = m.savedFilter.Raw
		m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
		m.sortFiltered()
		m.cursor = 0
		m.focusedChipIdx = -1
		m.closeCompletion()
		m.mode = ModeList

	case keyEnter:
		// Live-applied already — just exit the input.
		m.closeCompletion()
		m.mode = ModeList

	case keyBackspace:
		if r := []rune(m.filterText); len(r) > 0 {
			m.filterText = string(r[:len(r)-1])
		}
		m.reapplyFilterLive()
		m.recomputeCompletion()

	default:
		if t := msg.Key().Text; t != "" {
			m.filterText += t
			m.reapplyFilterLive()
			m.recomputeCompletion()
		}
	}

	return m, nil
}

func (m Model) handleConfigPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyQ:
		m.mode = m.configPickerReturnMode
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		m.configPickerCursor = clamp(m.configPickerCursor+1, 0, len(m.configPickerAccounts)-1)
	case keyK, keyUp:
		m.configPickerCursor = clamp(m.configPickerCursor-1, 0, len(m.configPickerAccounts)-1)
	case keyEnter:
		if m.configPickerCursor < len(m.configPickerAccounts) {
			acc := m.configPickerAccounts[m.configPickerCursor]
			m.mode = m.configPickerReturnMode
			if m.configPickerReturnMode == ModePicker && m.configPickerProjectIdx >= 0 {
				defaultProfile := m.pickerSessions[m.configPickerProjectIdx].EffectiveProfile()
				if acc.Name == defaultProfile {
					delete(m.pickerProfileOverride, m.configPickerProjectIdx)
				} else {
					if m.pickerProfileOverride == nil {
						m.pickerProfileOverride = make(map[int]string)
					}
					m.pickerProfileOverride[m.configPickerProjectIdx] = acc.Name
				}
				m.savePickerOverrides()
				if cmd := m.refreshPreviewForPickerCursor(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
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
	if m.pickerSearchMode {
		return m.handlePickerSearchKey(msg)
	}
	return m.handlePickerNavKey(msg)
}

// handlePickerSearchKey handles input while the picker is in search mode
// (activated by pressing `/`). Only esc, enter, and backspace have special
// meaning; every other printable character (including j, k, space, p, etc.)
// is appended to the filter buffer so the user can type freely.
func (m Model) handlePickerSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc, keyEnter:
		m.pickerSearchMode = false
		return m, nil
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
	default:
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
}

// handlePickerNavKey handles input while the picker is in navigation mode
// (the default). Printable characters other than recognised shortcuts are
// silently ignored; press `/` to enter search mode and type a filter.
func (m Model) handlePickerNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keySlash:
		m.pickerSearchMode = true
		return m, nil
	case keyEsc:
		// Two-stage Esc: first clears a non-empty filter; second exits picker.
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
		m.pickerProfileOverride = nil
		if cmd := m.refreshPreviewForCursor(); cmd != nil {
			return m, cmd
		}
		return m, nil
	case keyCtrlC, keyQ:
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
		return m.togglePickerPreview()
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
			return m, pickerOpenCmd(m, m.pickerSessionWithResolvedProfile(origIdx))
		}
		return m, nil
	case keyClearStatus:
		if len(m.pickerFilteredIdx) > 0 {
			origIdx := m.pickerFilteredIdx[m.pickerCursor]
			sess := m.pickerSessionWithResolvedProfile(origIdx)
			m.targetSession = session.RunningSession{
				Base:    session.FormatTitle(sess),
				Project: m.pickerSessions[origIdx].Title,
				Profile: m.pickerEffectiveProfile(origIdx),
				Account: m.pickerEffectiveProfile(origIdx),
			}
			m.configPickerAccounts = m.cfg.Accounts
			m.configPickerCurrent = m.pickerEffectiveProfile(origIdx)
			m.configPickerCursor = pickerAccountIndex(m.cfg.Accounts, m.configPickerCurrent)
			m.configPickerReturnMode = ModePicker
			m.configPickerProjectIdx = origIdx
			m.mode = ModeConfigPicker
		}
		return m, nil
	case keyEnter:
		cmd := m.dispatchPickerLaunch()
		m.mode = ModeList
		m.pickerSelected = nil
		m.pickerProfileOverride = nil
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
	default:
		return m, nil
	}
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
			sb.WriteString(renderDeprecationBanner(m))
			sb.WriteString(renderLegend(m))
			sb.WriteString(renderChips(m, m.width))
			sb.WriteString(renderInlineFilter(m))
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

// activePreviewEnabled reports whether the preview pane is enabled for the
// currently active mode. ModePicker has its own independent flag so that
// toggling preview inside the picker does not disturb the list's preview state.
func (m Model) activePreviewEnabled() bool {
	if m.mode == ModePicker {
		return m.pickerPreviewEnabled
	}
	return m.previewEnabled
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
	sess := m.pickerSessionWithResolvedProfile(origIdx)
	key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
	for _, rs := range m.sessions {
		if runningSessionKey(rs) == key {
			return rs.WindowID
		}
	}
	return 0
}

// refreshPreviewForPickerCursor returns an immediate fetch cmd when the
// picker cursor now points at a different running session (or none).
// Returns nil when preview is off or the window hasn't changed.
func (m *Model) refreshPreviewForPickerCursor() tea.Cmd {
	if !m.pickerPreviewEnabled {
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

// togglePickerPreview flips the picker's own preview pane on/off, independent
// of m.previewEnabled (the list's preview flag).
func (m Model) togglePickerPreview() (tea.Model, tea.Cmd) {
	if m.pickerPreviewEnabled {
		m.pickerPreviewEnabled = false
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
	m.pickerPreviewEnabled = true
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = m.pickerCurrentWindowID()
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
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

// healthCmd refreshes the daemon and/or hook health cached on the model.
// Each section is only re-queried when the corresponding flag is set so
// the cheap daemon ping doesn't pull in the more expensive hooks scan on
// every tick. Errors are non-fatal — they collapse into the "degraded"
// pip rendering rather than tearing down the dashboard.
func healthCmd(cfg *config.Config, amxBin string, checkDaemon, checkHooks bool) tea.Cmd {
	return func() tea.Msg {
		msg := healthMsg{daemonChecked: checkDaemon, hookChecked: checkHooks}
		if checkDaemon {
			h := daemon.NewClient().Health()
			msg.daemonAlive = h.Alive
		}
		if checkHooks {
			summary, err := hooks.OverallHookHealth(cfg, amxBin)
			if err != nil {
				msg.hookErr = err
			} else {
				msg.hookSummary = HookHealth{
					InSync:  summary.InSync,
					Drifted: summary.Drifted,
					Missing: summary.Missing,
				}
			}
		}
		return msg
	}
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
		sess = m.pickerSessionWithResolvedProfile(i)
		key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
		baseTitle := session.FormatTitle(sess)
		if m.pickerRunning[key] {
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
	m.launchBatchErrors = 0

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
			acc, ok := findAccountByName(accounts, sess.EffectiveProfile())
			if !ok {
				return launchProgressMsg{batchID: batchID, total: total, title: title, err: nil}
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
		m.launchBatchErrors++
		if c := m.pushToast(Toast{
			Severity: toastError,
			Text:     fmt.Sprintf("launch failed: %s — %s", msg.title, msg.err),
		}); c != nil {
			cmds = append(cmds, c)
		}
	}
	batchDone := m.launchBatchDone >= msg.total
	severity := toastInfo
	if batchDone {
		severity = toastSuccess
	}
	text := fmt.Sprintf("Launched %d/%d · %s", m.launchBatchDone, msg.total, msg.title)
	expires := time.Now().Add(progressToastTTL)
	if batchDone {
		expires = time.Now().Add(defaultToastTTL)
	}
	if c := m.replaceToastByID(msg.batchID, Toast{
		Severity: severity,
		Text:     text,
		Expires:  expires,
	}); c != nil {
		cmds = append(cmds, c)
	}
	if batchDone && m.launchBatchErrors == 0 {
		cmds = append(cmds, tea.Quit)
	} else {
		cmds = append(cmds, fetchCmd(m.client, m.sortMode))
	}
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
		acc, ok := findAccountByName(accounts, sess.EffectiveProfile())
		if !ok {
			return pickerOpenDoneMsg{err: nil}
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
			Account:   cs.EffectiveProfile(),
			Profile:   cs.EffectiveProfile(),
			Project:   cs.Title,
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

func (m Model) pickerEffectiveProfile(origIdx int) string {
	if name := m.pickerProfileOverride[origIdx]; name != "" {
		return name
	}
	if origIdx >= 0 && origIdx < len(m.pickerSessions) {
		return m.pickerSessions[origIdx].EffectiveProfile()
	}
	return ""
}

func (m Model) pickerSessionWithResolvedProfile(origIdx int) config.Session {
	if origIdx < 0 || origIdx >= len(m.pickerSessions) {
		return config.Session{}
	}
	return sessionWithProfile(m.pickerSessions[origIdx], m.pickerEffectiveProfile(origIdx))
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
		if gi != gj {
			return gi < gj
		}
		// Within Active (group 1), sessions with an explicit state icon sort before those without.
		if gi == 1 {
			si := m.filtered[i].State != ""
			sj := m.filtered[j].State != ""
			if si != sj {
				return si
			}
		}
		return false
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

// loadPickerOverrides loads persisted profile overrides from disk and maps
// them to the current pickerSessions by title. Stale entries (titles no
// longer in the session list) are pruned and the file is re-saved.
func loadPickerOverrides(sessions []config.Session) map[int]string {
	out := make(map[int]string)
	f, err := overrides.Load()
	if err != nil {
		slog.Debug("overrides: load error", "err", err)
		return out
	}
	titleIdx := make(map[string]int, len(sessions))
	for i, s := range sessions {
		titleIdx[s.Title] = i
	}
	var pruned bool
	var fresh []overrides.Entry
	for _, e := range f.Entries {
		idx, ok := titleIdx[e.ProjectTitle]
		if !ok {
			slog.Debug("overrides: pruning stale entry", "title", e.ProjectTitle)
			pruned = true
			continue
		}
		fresh = append(fresh, e)
		out[idx] = e.ProfileName
	}
	if pruned {
		f.Entries = fresh
		if err := overrides.Save(f); err != nil {
			slog.Debug("overrides: save after prune error", "err", err)
		}
	}
	return out
}

// pulseDecay is how long a recorded state transition keeps pulsing in the
// list before it's swept off.
const pulseDecay = 5 * time.Second

// recordTransitions diffs the current m.sessions against m.lastSeenStates
// and records any state change in m.recentTransitions. On the very first
// refresh (lastSeenStates is nil) the snapshot is seeded silently so the
// initial list does not pulse every row.
func (m *Model) recordTransitions() {
	if m.lastSeenStates == nil {
		m.lastSeenStates = make(map[int]string, len(m.sessions))
		for _, s := range m.sessions {
			m.lastSeenStates[s.WindowID] = s.State
		}
		return
	}
	for _, s := range m.sessions {
		prev, ok := m.lastSeenStates[s.WindowID]
		if !ok {
			// New session since last snapshot — treat as no-op so a freshly
			// launched tab does not pulse on its first appearance.
			m.lastSeenStates[s.WindowID] = s.State
			continue
		}
		if prev == s.State {
			continue
		}
		if m.recentTransitions == nil {
			m.recentTransitions = make(map[int]time.Time)
		}
		if _, already := m.recentTransitions[s.WindowID]; !already {
			m.recentTransitions[s.WindowID] = time.Now()
		}
	}
}

// sweepRecentTransitions drops entries older than pulseDecay so the maps
// stay bounded and stale pulses do not linger after their visual window.
func (m *Model) sweepRecentTransitions() {
	if len(m.recentTransitions) == 0 {
		return
	}
	cutoff := time.Now().Add(-pulseDecay)
	for id, t := range m.recentTransitions {
		if t.Before(cutoff) {
			delete(m.recentTransitions, id)
		}
	}
}

// snapshotSeenStates refreshes the per-session state snapshot to match the
// current view and clears any in-flight pulses. Called on every ModeList
// key press so the pulse layer surfaces only changes the user has not yet
// seen.
func (m *Model) snapshotSeenStates() {
	if m.lastSeenStates == nil {
		m.lastSeenStates = make(map[int]string, len(m.sessions))
	}
	for _, s := range m.sessions {
		m.lastSeenStates[s.WindowID] = s.State
	}
	if len(m.recentTransitions) > 0 {
		m.recentTransitions = nil
	}
}

// hasActivePulse reports whether windowID has a recent-transition record
// that has not yet decayed past pulseDecay.
func (m Model) hasActivePulse(windowID int) bool {
	if len(m.recentTransitions) == 0 {
		return false
	}
	t, ok := m.recentTransitions[windowID]
	if !ok {
		return false
	}
	return time.Since(t) < pulseDecay
}

// savePickerOverrides writes the current pickerProfileOverride map to disk.
func (m Model) savePickerOverrides() {
	f := overrides.File{Version: 1}
	for idx, profile := range m.pickerProfileOverride {
		if idx >= 0 && idx < len(m.pickerSessions) {
			f.Entries = append(f.Entries, overrides.Entry{
				ProjectTitle: m.pickerSessions[idx].Title,
				ProfileName:  profile,
			})
		}
	}
	if err := overrides.Save(f); err != nil {
		slog.Debug("overrides: save error", "err", err)
	}
}
