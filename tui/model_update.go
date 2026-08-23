package tui

// Root model construction and the Bubble Tea entry points. Update is a pure
// dispatch table: every case hands off to the handler owned by the view that
// declared the message, so adding a message type never grows this file's
// logic. The state those handlers read lives in model.go.

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

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
		daemonCommand:         daemonRequest,
		now:                   time.Now,
		idleThreshold:         idle,
		focusedChipIdx:        -1,
		amxBin:                amxBin,
		deprecations:          cfg.Deprecations,
		messageDetailIdx:      -1,
		initialSourceWindowID: sourceWindowID,
		mode:                  initialMode,
	}
}

// Init starts the refresh ticker and fetches the first session list.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.runInitialFetchCmd(m.initialSourceWindowID),
		m.runActiveLoopRefreshCmd(m.activeLoopRefreshSeq),
		tickCmd(m.interval),
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

	case tickMsg:
		return m.handleTick(msg)

	case healthMsg:
		return m.handleHealth(msg)

	case previewTickMsg:
		return m.handlePreviewTick(msg)

	case previewContentMsg:
		return m.handlePreviewContent(msg)

	case sessionsMsg:
		return m.handleSessionsMsg(msg)

	case activeLoopsMsg:
		return m.handleActiveLoopsMsg(msg)

	case errMsg:
		m.err = msg.err
		return m, nil

	case actionDoneMsg:
		return m.handleActionDone(msg)

	case copyDoneMsg:
		return m.handleCopyDone(msg)

	case resortDoneMsg:
		return m.handleResortDone(msg)

	case launchProgressMsg:
		return m.handleLaunchProgress(msg)

	case toastTickMsg:
		m.sweepUndo()
		return m, m.sweepToasts()

	case pickerOpenDoneMsg:
		return m.handlePickerOpenDone(msg)

	case focusDoneMsg:
		return m.handleFocusDone(msg)

	case peekDoneMsg:
		return m.handlePeekDone(msg)

	case detailEventsMsg:
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
