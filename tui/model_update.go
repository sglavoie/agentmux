// This file is the Bubble Tea entry point of the root model: construction,
// Init, the Update switch every message passes through, the window-resize
// arithmetic that reflows the layout, and the periodic tick that drives
// refresh, pulse decay, and toast expiry.

package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/commands"
)

// Root model construction and the Bubble Tea entry points. Update is a pure
// dispatch table: every case hands off to the handler owned by the view that
// declared the message, so adding a message type never grows this file's
// logic. The state those handlers read lives in model.go.

// NewModel creates a dashboard model.
func NewModel(cfg *config.Config, client kitty.KittyClient) Model {
	return newModel(cfg, client, 0, ModeList)
}

// NewModelFromWindow creates a dashboard model that initially selects the
// running session in the Kitty tab containing sourceWindowID. A zero or
// unmapped ID leaves the dashboard's normal initial selection unchanged.
func NewModelFromWindow(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) Model {
	return newModel(cfg, client, sourceWindowID, ModeList)
}

func newModel(cfg *config.Config, client kitty.KittyClient, sourceWindowID int, initialMode Mode) Model {
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
	attn := make(map[string]bool, len(cfg.Defaults.Jump.AttentionStates))
	for _, s := range cfg.Defaults.Jump.AttentionStates {
		attn[s] = true
	}
	return Model{
		client:                client,
		cfg:                   cfg,
		interval:              interval,
		sortMode:              sort,
		attentionStates:       attn,
		backgroundCommands:    defaultModelBackgroundCommands(),
		daemonCommand:         commands.DaemonRequest,
		now:                   time.Now,
		idleThreshold:         idle,
		focusedChipIdx:        -1,
		amxBin:                amxBin,
		deprecations:          cfg.Deprecations,
		messageDetailIdx:      -1,
		initialSourceWindowID: sourceWindowID,
		mode:                  initialMode,
		// Arm the first refresh here rather than in Init. Init has a value
		// receiver, so the sequence it drew and the in-flight bit it set would
		// be discarded with its copy of the model, and the first tick would
		// start a second concurrent refresh against the same startup one.
		topologyRefreshNextSeq:   firstTopologyRefreshSeq,
		topologyRefreshActiveSeq: firstTopologyRefreshSeq,
		topologyRefreshInFlight:  true,
	}
}

// firstTopologyRefreshSeq is the sequence the constructor arms. Sequences start
// at one so that zero can mean "unsequenced" on the wire between the refresh
// command and the update loop.
const firstTopologyRefreshSeq = 1

// Init starts the refresh ticker and fetches the first session list. The
// topology refresh it dispatches is the one newModel already armed.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.runTopologyRefreshCmd(m.topologyRefreshActiveSeq, m.initialSourceWindowID),
		m.runActiveLoopRefreshCmd(m.activeLoopRefreshSeq),
		commands.Tick(m.interval),
		m.runHealthCmd(true, true),
		startupSessionWarmupCmd(m.sessionSearchSeq),
	}
	return tea.Batch(cmds...)
}

// Update handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case commands.TickMsg:
		return m.handleTick(msg)

	case commands.HealthMsg:
		return m.handleHealth(msg)

	case previewTickMsg:
		return m.handlePreviewTick(msg)

	case previewContentMsg:
		return m.handlePreviewContent(msg)

	case commands.SessionsMsg:
		return m.handleSessionsMsg(msg)

	case commands.ActiveLoopsMsg:
		return m.handleActiveLoopsMsg(msg)

	case commands.ErrMsg:
		return m.handleRefreshErr(msg)

	case commands.ActionDoneMsg:
		return m.handleActionDone(msg)

	case commands.CopyDoneMsg:
		return m.handleCopyDone(msg)

	case commands.ResortDoneMsg:
		return m.handleResortDone(msg)

	case launchProgressMsg:
		return m.handleLaunchProgress(msg)

	case toastTickMsg:
		m.sweepUndo()
		return m, m.sweepToasts()

	case commands.PickerOpenDoneMsg:
		return m.handlePickerOpenDone(msg)

	case commands.FocusDoneMsg:
		return m.handleFocusDone(msg)

	case commands.PeekDoneMsg:
		return m.handlePeekDone(msg)

	case commands.DetailEventsMsg:
		return m.handleDetailEvents(msg)

	case sessionCacheLoadStartedMsg:
		return m.handleSessionCacheLoadStarted(msg)

	case sessionCacheLoadDoneMsg:
		return m.handleSessionCacheLoadDone(msg)

	case sessionWarmRefreshDoneMsg:
		return m.handleSessionWarmRefreshDone(msg)

	case sessionsRefreshDoneMsg:
		return m.handleSessionsRefreshDone(msg)

	case sessionSearchDebounceMsg:
		return m.handleSessionSearchDebounce(msg)

	case sessionSearchDoneMsg:
		return m.handleSessionSearchDone(msg)

	case transcriptDeleteDoneMsg:
		return m.handleTranscriptDeleteDone(msg)

	case plansRefreshDoneMsg:
		return m.handlePlansRefreshDone(msg)

	case planCacheLoadDoneMsg:
		return m.handlePlanCacheLoadDone(msg)

	case planSearchDebounceMsg:
		return m.handlePlanSearchDebounce(msg)

	case planSearchDoneMsg:
		return m.handlePlanSearchDone(msg)

	case planEditorErrMsg:
		return m.handlePlanEditorErr(msg)

	case planFilesLoadedMsg:
		return m.handlePlanFilesLoaded(msg)

	case viewerMessagesLoadedMsg:
		return m.handleViewerMessagesLoaded(msg)

	case worktreesLoadedMsg:
		return m.handleWorktreesLoaded(msg)

	case worktreeActionDoneMsg:
		return m.handleWorktreeActionDone(msg)

	case aslSnapshotLoadedMsg:
		return m.handleASLSnapshotLoaded(msg)

	case aslWorkbenchDoneMsg:
		return m.handleASLWorkbenchDone(msg)

	case aslQuestionLoadedMsg:
		return m.handleASLQuestionLoaded(msg)

	case aslActionDoneMsg:
		return m.handleASLActionDone(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// Health-pip refresh cadences. Daemon checks are a cheap socket round-trip
// (5s), hook checks read each profile's settings.json so they run on a
// longer interval (15s).
const (
	daemonHealthInterval = 5 * time.Second
	hookHealthInterval   = 15 * time.Second
)

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	cfg := m.cfg.Defaults.Preview
	var cmds []tea.Cmd
	if m.previewEnabled {
		if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
			if c := m.disablePreviewWithNotice("preview disabled: terminal too small"); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	if m.pickerPreviewEnabled {
		if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
			if c := m.disablePickerPreviewWithNotice("preview disabled: terminal too small"); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	// Clamp scroll offsets to the new viewport. Without this, a resize
	// that shrinks the visible window leaves the persisted offset above
	// maxOffset; the renderer clamps for display but the next j/k press
	// operates on the stale value, so the user has to scroll through
	// dead rows before the viewport reacts.
	clampDetailScroll(&m)
	clampHelpOffset(&m)
	clampActionsOffset(&m)
	clampCommandPaletteOffset(&m)
	scrollCommandPaletteToCursor(&m)
	clampMessageDetailScroll(&m)
	clampListOffset(&m)
	scrollListToCursor(&m)
	clampPickerOffset(&m)
	scrollPickerToCursor(&m)
	scrollSessionSearchToCursor(&m)
	scrollPlanSearchToCursor(&m)
	clampViewerScroll(&m)
	scrollViewerToCursor(&m)
	clampPlanViewerScroll(&m)
	scrollPlanViewerToCursor(&m)
	scrollChipPickerToCursor(&m)
	scrollWorktreesToCursor(&m)
	scrollLivePickerToCursor(&m)
	scrollASLToCursor(&m)
	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// handleTick processes a commands.TickMsg: sweeps stale transition pulses, schedules
// the next session/active-loop fetches and tick, and conditionally fires a health check.
func (m Model) handleTick(msg commands.TickMsg) (tea.Model, tea.Cmd) {
	_ = msg
	m.sweepRecentTransitions()
	m.activeLoopRefreshSeq++
	cmds := []tea.Cmd{
		m.requestTopologyRefresh(),
		m.runActiveLoopRefreshCmd(m.activeLoopRefreshSeq),
		commands.Tick(m.interval),
	}
	now := time.Now()
	checkDaemon := now.Sub(m.daemonHealthCheckedAt) >= daemonHealthInterval
	checkHooks := now.Sub(m.hookHealthCheckedAt) >= hookHealthInterval
	if checkDaemon || checkHooks {
		cmds = append(cmds, m.runHealthCmd(checkDaemon, checkHooks))
	}
	if m.mode == ModeDetail && m.detailWindowID != 0 {
		cmds = append(cmds, m.runSessionDetailCmd(m.detailWindowID))
	}
	// The Runs view polls on the shared tick rather than owning a refresh key,
	// so a snapshot arriving under the cursor cannot change modes or selection.
	if m.activeRootMode() == ModeASLRuns {
		m.captureASLSelection()
		m.aslLoading = true
		m.aslRefreshSeq++
		cmds = append(cmds, m.runASLSnapshotCmd(m.aslRefreshSeq))
	}
	return m, tea.Batch(cmds...)
}

// handleHealth processes a commands.HealthMsg: updates the daemon-alive flag and/or
// hook health summary, gated by which checks were actually run.
func (m Model) handleHealth(msg commands.HealthMsg) (tea.Model, tea.Cmd) {
	now := time.Now()
	if msg.DaemonChecked {
		m.daemonAlive = msg.DaemonAlive
		m.daemonHealthCheckedAt = now
	}
	if msg.HookChecked {
		m.hookHealth = msg.HookSummary
		m.hookHealthErr = msg.HookErr
		m.hookHealthCheckedAt = now
	}
	return m, nil
}

// handlePreviewTick processes a previewTickMsg: if preview is active, schedules
// the next content fetch and tick.
func (m Model) handlePreviewTick(msg previewTickMsg) (tea.Model, tea.Cmd) {
	_ = msg
	if !m.activePreviewEnabled() {
		return m, nil
	}
	d := time.Duration(m.cfg.Defaults.Preview.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
}

// handlePreviewContent processes a previewContentMsg: updates the cached
// preview text or error when the message belongs to the current window.
func (m Model) handlePreviewContent(msg previewContentMsg) (tea.Model, tea.Cmd) {
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
}
