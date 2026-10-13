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
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
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

// NewHistoryModel creates a model that starts in Past Sessions, across all
// projects, and selects the conversation wanted when the results list it.
func NewHistoryModel(cfg *config.Config, client kitty.KittyClient, wanted conversation.Identity) Model {
	m := newModel(cfg, client, 0, ModeSessionSearch)
	m.historyOnly = true
	m.sessionWantedKey = transcript.IndexKey(wanted.Harness, wanted.SessionID)
	// The entry every other route takes, for its loading state. Its command
	// is dropped: it is the cache load Init dispatches, and marking that load
	// in flight here keeps a second one from starting beside it.
	entered, _ := m.enterAllSessionSearch()
	return entered.(Model)
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
	// cfg.Defaults.Jump is deliberately not read here. The attention set it
	// carries drove the retired bracket-key navigation; `n`/`N` asks
	// dashboard.NeedsYou instead, which is the same judgement the queue heading
	// and the count are drawn from rather than a second, user-editable list of
	// state names. The configuration is still accepted and still validated —
	// internal/config raises a deprecation for it — but nothing on the dashboard
	// navigates by it.
	return Model{
		client:                client,
		cfg:                   cfg,
		interval:              interval,
		sortMode:              sort,
		backgroundCommands:    defaultModelBackgroundCommands(),
		daemonCommand:         commands.DaemonRequest,
		now:                   time.Now,
		idleThreshold:         idle,
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
	updated, cmd := m.dispatch(msg)
	// A history view has no session list: whatever would settle on it — a
	// closed palette, a dismissed prompt, a finished action — settles on Past
	// Sessions instead. Leaving Past Sessions itself quits (exitSessionSearch).
	if next, ok := updated.(Model); ok && next.historyOnly && next.mode == ModeList {
		next.mode = ModeSessionSearch
		return next, cmd
	}
	return updated, cmd
}

func (m Model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case recoveryDoneMsg:
		return m.handleRecoveryDone(msg)

	case toastTickMsg:
		m.sweepRecovery()
		return m, m.sweepToasts()

	case commands.PickerOpenDoneMsg:
		return m.handlePickerOpenDone(msg)

	case commands.FocusDoneMsg:
		return m.handleFocusDone(msg)

	case commands.PeekDoneMsg:
		return m.handlePeekDone(msg)

	case commands.AttachDoneMsg:
		return m.handleAttachDone(msg)

	case commands.SendDoneMsg:
		return m.handleSendDone(msg)

	case commands.SendScreenMsg:
		return m.handleSendScreen(msg)

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

	case planRunDoneMsg:
		return m.handlePlanRunDone(msg)

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

	case mouseActionMsg:
		return m.handleMouseAction(msg)
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
	// What the inspector was doing before the terminal changed size. The resize
	// itself decides nothing about the operator's intent — m.inspectorDisplay is
	// deliberately not touched anywhere in this function — so the only question is
	// whether the new geometry still honours it.
	wasActive := m.inspectorActive()
	m.width = msg.Width
	m.height = msg.Height
	cfg := m.cfg.Defaults.Preview
	var cmds []tea.Cmd
	// Nothing reconciles the focus here, and that is the point. A split that no
	// longer fits gives its keys back to the queue because inspectorFocused asks
	// whether the pane is on screen, not because anything rewrote the bit — so
	// widening restores the pane, the focus, and the scroll scope together. The
	// operator's own Tab, Escape, and mouse are the only things that move it.
	if !wasActive && m.inspectorActive() {
		// The pane just came back. Its polling chain died the first time a tick
		// found it hidden and nothing else revives one, so the resize has to — and
		// startPreviewPoll retires whatever chain the model still names, which is
		// what keeps a fast shrink/grow cycle from ending with two.
		//
		// It goes through reshowPreview rather than retargetPreview because a
		// resize is not a selection. Retargeting unconditionally would resume a
		// paused inspector every time the terminal crossed the split's floor and
		// came back, losing the place the operator was reading for a reason they
		// never asked for.
		if c := m.reshowPreview(); c != nil {
			cmds = append(cmds, c)
		}
	}
	// Only while the launcher is the surface on screen. pickerPreviewEnabled is
	// the launcher's remembered visibility and outlives the visit that set it, so
	// an unguarded check here would let a shrink of the DASHBOARD disable a pane
	// nobody is looking at — and, because the two share one target, retarget the
	// inspector the operator is reading.
	if launcherOwnsPreview(m) && m.pickerPreviewEnabled {
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
	clampHealthScroll(&m)
	clampHelpOffset(&m)
	clampActionsSelection(&m)
	clampCommandPaletteOffset(&m)
	scrollCommandPaletteToCursor(&m)
	clampMessageDetailScroll(&m)
	clampListOffset(&m)
	scrollListToCursor(&m)
	scrollProjectSwitcherToCursor(&m)
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
	// A check that outlasts the tick must not be joined by another: the hooks
	// scan reads the diagnostic journal, and scans started every tick on top of
	// one another only slow each other down. The two checks run as separate
	// commands so the daemon ping never waits on that scan.
	if !m.daemonHealthInFlight && now.Sub(m.daemonHealthCheckedAt) >= daemonHealthInterval {
		m.daemonHealthInFlight = true
		cmds = append(cmds, m.runHealthCmd(true, false))
	}
	if !m.hookHealthInFlight && now.Sub(m.hookHealthCheckedAt) >= hookHealthInterval {
		m.hookHealthInFlight = true
		cmds = append(cmds, m.runHealthCmd(false, true))
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
		m.daemonHealthInFlight = false
	}
	if msg.HookChecked {
		m.hookHealth = msg.HookSummary
		m.hookHealthErr = msg.HookErr
		m.hookHealthCheckedAt = now
		m.hookHealthInFlight = false
	}
	// A check that resolved can shorten the body under an open details overlay —
	// a hooks error carries three detail lines and the clean reading carries
	// none — so the offset is pulled back the same way a resize pulls it back.
	// This handler still only RECORDS what the tick-driven command observed; it
	// starts nothing, and the overlay being open changes neither of those things.
	clampHealthScroll(&m)
	return m, nil
}

// handlePreviewTick processes a previewTickMsg: schedules the chain's next beat
// and asks the read controller for a refresh of the current target.
//
// The tick re-reads the current target under the current generation. It must not
// open a new one: a read slower than the refresh interval would then be
// superseded by the next tick before it ever landed. Whether that read actually
// starts is the controller's call — while one is out the tick coalesces into the
// pending catch-up instead of stacking a second read on top of it.
func (m Model) handlePreviewTick(msg previewTickMsg) (tea.Model, tea.Cmd) {
	// A tick from a retired chain is not merely late. Acting on one would
	// schedule a second successor, and from then on two chains would beat
	// against the same pane forever, at twice the configured rate and with no
	// path back to one. Zero belongs to no chain at all.
	if msg.pollSeq == 0 || msg.pollSeq != m.previewPollSeq {
		return m, nil
	}
	if !m.activePreviewEnabled() {
		// The pane is hidden, so let the chain die here rather than poll a
		// surface nobody is looking at. Whichever path shows it again opens a
		// fresh chain through startPreviewPoll.
		return m, nil
	}
	next := previewTickCmd(previewRefreshInterval(m.cfg), msg.pollSeq)
	return m, tea.Batch(m.requestPreviewRead(), next)
}

// handlePreviewContent processes a previewContentMsg: updates the cached preview
// text or error when the message describes what the pane is pointed at, and
// settles the controller's bookkeeping when it is the read that was outstanding.
//
// Those are two independent questions and the order they are asked in is the
// point. A reply for an abandoned target is worthless as content and is still
// the settlement the controller is waiting on, so the slot is released first and
// unconditionally; only then is the content judged. Gating the settlement on the
// content test — the shape this handler used to have — would strand the slot on
// every read the operator outran and stop the pane reading at all.
func (m Model) handlePreviewContent(msg previewContentMsg) (tea.Model, tea.Cmd) {
	settled := m.settlePreviewRead(msg.requestSeq)
	if m.previewAcceptsContent(msg) {
		// Either outcome is a read that has landed for this target, which is
		// what previewLoaded records: it separates "nothing has come back yet"
		// from "this session has printed nothing", and a failure has answered
		// the question too.
		m.previewLoaded = true
		if msg.err != nil {
			m.previewError = msg.err.Error()
		} else {
			m.previewError = ""
			m.previewText = msg.text
		}
	}
	if !settled {
		return m, nil
	}
	return m, m.catchUpPreviewRead()
}
