package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
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
	ModeDetail            // detail pane: per-session hook event log
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
	sessions   []session.RunningSession // sorted full list
	filtered   []session.RunningSession // sessions after query+filter applied
	cursor     int
	listOffset int // top row index for the scrollable list view in ModeList

	// mode
	mode Mode

	// filter (unified search + account + state DSL)
	filterText     string          // live input while in ModeSearch
	typeBuffer     string          // live input from ModeList's type-to-filter dispatch
	activeFilter   Filter          // committed filter applied to filtered
	savedFilter    Filter          // snapshot taken on enter; restored on Esc
	focusedChipIdx int             // -1 when no chip focused; otherwise index into Components()
	completion     completionState // sigil-aware popup state for ModeSearch / type-to-filter

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
	pickerSessions       []config.Session
	pickerRunning        map[string]bool
	pickerSelected       map[int]bool
	pickerCursor         int
	pickerOffset         int // top row index for the scrollable picker view
	pickerFilter         string // live filter buffer (DSL)
	pickerFilteredIdx    []int  // indices into pickerSessions for the filtered view
	pickerSearchMode     bool   // true while / is active and filter input is open
	pickerPreviewEnabled bool   // picker-local preview toggle, independent of ModeList

	// last error from a close/restart action
	actionErr error

	// config picker mode
	configPickerAccounts   []config.Account
	configPickerCursor     int
	configPickerCurrent    string
	configPickerReturnMode Mode
	configPickerProjectIdx int

	// picker launch overrides: project index -> profile name
	pickerProfileOverride map[int]string

	// activity tracking: window_id → last activity time, sourced from daemon.
	// Empty when the daemon is unreachable; treated as "Active" by stateGroup.
	lastActivity  map[int]time.Time
	idleThreshold time.Duration

	// watchdog snapshot: window_id → last sweep-eligible action plus the
	// resolved threshold for that window. Populated alongside lastActivity
	// from the same TypeStateQuery round-trip; nil when the daemon is
	// unreachable. Drives the per-row age column in renderListAt.
	watchdog map[int]daemon.WatchdogState

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
	amxBin                string
	daemonAlive           bool
	daemonHealthCheckedAt time.Time
	hookHealth            HookHealth
	hookHealthErr         error
	hookHealthCheckedAt   time.Time
	deprecations          []config.Deprecation
	deprecationDismissed  bool

	// preview pane
	previewEnabled       bool
	previewWindowID      int
	previewText          string
	previewError         string
	previewNotice        string
	previewNoticeExpires time.Time

	// toast strip + launch-progress bookkeeping
	toasts            []Toast
	launchBatchID     int
	launchBatchDone   int
	launchBatchErrors int

	// pendingUndo stacks the most recent destructive actions eligible for
	// `u`-driven undo. Entries expire at their deadline; sweepUndo trims
	// them on toast ticks so memory stays bounded.
	pendingUndo []undoEntry

	// helpOffset is the first visible line index in the scrollable help
	// overlay (ModeHelp). Reset to 0 whenever the overlay is opened or closed.
	helpOffset int

	// detail pane state (ModeDetail). Fields are zeroed when the pane is
	// closed so the placeholder render reads as a fresh state.
	detailWindowID int                   // 0 when pane is closed
	detailEvents   []daemon.SessionEvent // events from the daemon for detailWindowID
	detailScroll   int                   // top row index for the scrollable view
	detailLoading  bool                  // true while the daemon query is in flight
	detailErr      error                 // last error from the daemon query
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
		fetchCmd(m.client, m.cfg, m.sortMode),
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
		// Clamp scroll offsets to the new viewport. Without this, a resize
		// that shrinks the visible window leaves the persisted offset above
		// maxOffset; the renderer clamps for display but the next j/k press
		// operates on the stale value, so the user has to scroll through
		// dead rows before the viewport reacts.
		clampDetailScroll(&m)
		clampHelpOffset(&m)
		clampListOffset(&m)
		scrollListToCursor(&m)
		clampPickerOffset(&m)
		scrollPickerToCursor(&m)
		return m, nil

	case tickMsg:
		m.sweepRecentTransitions()
		cmds := []tea.Cmd{fetchCmd(m.client, m.cfg, m.sortMode), tickCmd(m.interval)}
		now := time.Now()
		checkDaemon := now.Sub(m.daemonHealthCheckedAt) >= daemonHealthInterval
		checkHooks := now.Sub(m.hookHealthCheckedAt) >= hookHealthInterval
		if checkDaemon || checkHooks {
			cmds = append(cmds, healthCmd(m.cfg, m.amxBin, checkDaemon, checkHooks))
		}
		if m.mode == ModeDetail && m.detailWindowID != 0 {
			cmds = append(cmds, sessionDetailCmd(m.detailWindowID))
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
		m.watchdog = msg.watchdog
		m.sessions = msg.sessions
		m.recordTransitions()
		m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
		m.sortFiltered()
		m.cursor = restoreCursor(m.filtered, priorBase, m.lastActivity, m.idleThreshold)
		clampListOffset(&m)
		scrollListToCursor(&m)
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case actionDoneMsg:
		m.actionErr = msg.err
		// Trigger a refresh so the list reflects the new state.
		return m, fetchCmd(m.client, m.cfg, m.sortMode)

	case resortDoneMsg:
		m.actionErr = msg.err
		var cmds []tea.Cmd
		var t Toast
		if msg.err != nil {
			t = Toast{Severity: toastError, Text: "Resort failed: " + msg.err.Error()}
		} else {
			t = Toast{Severity: toastSuccess, Text: "Tabs resorted"}
		}
		if c := m.pushToast(t); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, fetchCmd(m.client, m.cfg, m.sortMode))
		return m, tea.Batch(cmds...)

	case launchProgressMsg:
		return m.handleLaunchProgress(msg)

	case toastTickMsg:
		m.sweepUndo()
		return m, m.sweepToasts()

	case pickerOpenDoneMsg:
		if msg.err != nil {
			m.actionErr = msg.err
			return m, fetchCmd(m.client, m.cfg, m.sortMode)
		}
		return m, tea.Quit

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

	case detailEventsMsg:
		// Stale responses (mode changed or different window than the one
		// currently shown in the pane) are dropped silently so a slow
		// daemon roundtrip cannot overwrite a fresh request's loading
		// state or repopulate the pane after the user closed it.
		if m.mode != ModeDetail || msg.windowID != m.detailWindowID {
			return m, nil
		}
		m.detailEvents = msg.events
		m.detailErr = msg.err
		m.detailLoading = false
		// A refresh that shrinks the event ring (daemon GC, or an empty
		// reply) can leave detailScroll above the new maxOffset. The
		// renderer clamps for display, but the next j/k/pgup/pgdn press
		// would operate on the stale offset and chew through dead rows
		// before the viewport reacts — mirror the WindowSizeMsg clamp.
		clampDetailScroll(&m)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}
