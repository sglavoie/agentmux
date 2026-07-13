package tui

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

// Model is the root Bubble Tea model for the dashboard.
type Model struct {
	// deps
	client                     kitty.KittyClient
	cfg                        *config.Config
	ctx                        context.Context
	dataServices               *DataServices
	dataServicesInitInFlight   bool
	dataServicesRetryAttempt   int
	dataServicesRetryScheduled bool
	dataServicesRecoveryState  dataServicesRecoveryState
	dataServicesRecoveryResult datastore.RecoveryResult
	dataServicesRecoveryErr    error
	// dataServicesProcessRestartRequested is set only after this process has
	// replaced a corrupt database. The Bubble Tea program must exit without
	// reopening SQLite so RunFromWindow can re-exec into a process with fresh
	// modernc SQLite and WAL state.
	dataServicesProcessRestartRequested bool
	interval                            time.Duration
	sortMode                            string
	attentionStates                     map[string]bool // states the bracket-key nav (]/[ and }/{) jumps to
	backgroundCommands                  modelBackgroundCommands
	daemonCommand                       func(daemon.Request) (daemon.Response, error)

	// session state
	sessions   []session.RunningSession // sorted full list
	filtered   []session.RunningSession // sessions after query+filter applied
	cursor     int
	listOffset int // top row index for the scrollable list view in ModeList
	// initialSourceWindowID is the Kitty window from which the dashboard was
	// opened. Only the startup fetch uses it to select the matching tab row.
	initialSourceWindowID int
	// lastSelectionRequestToken prevents a request left on a Kitty window
	// after a failed best-effort clear from repeatedly stealing the cursor.
	lastSelectionRequestToken string

	// pendingJumpDir holds the direction of a pending bracket-key chord:
	// 0 = no chord pending, +1 = ] pressed, -1 = [ pressed. Cleared on
	// chord completion, on Esc, on any unknown follow-up key, and on
	// transitions out of ModeList via clearJumpChord().
	pendingJumpDir int

	// mode
	mode Mode

	// filter (unified search + account + state DSL)
	filterText     string          // live input while in ModeSearch
	activeFilter   Filter          // committed filter applied to filtered
	savedFilter    Filter          // snapshot taken on enter; restored on Esc
	focusedChipIdx int             // -1 when no chip focused; otherwise index into Components()
	completion     completionState // sigil-aware popup state for ModeSearch

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
	confirmReturnMode  Mode

	// picker mode
	pickerSessions       []config.Session
	pickerRunning        map[string]int
	pickerSelected       map[int]bool
	pickerCursor         int
	pickerOffset         int    // top row index for the scrollable picker view
	pickerFilter         string // live filter buffer (DSL)
	pickerFilteredIdx    []int  // indices into pickerSessions for the filtered view
	pickerSearchMode     bool   // true while / is active and filter input is open
	pickerPreviewEnabled bool   // picker-local preview toggle, independent of ModeList

	// active agent-session-loop runs retained for detail views. Dashboard loop
	// markers come from the daemon's bulk state snapshot.
	activeLoopCandidates []agentloop.Candidate
	activeLoopRefreshErr error
	activeLoopRefreshSeq int

	// last error from a close/restart action
	actionErr error

	// actionsOffset is the first visible rendered action row in the per-row
	// actions overlay. Reset to 0 whenever the overlay is opened or closed.
	actionsOffset int
	// actionsReturnMode preserves the source surface behind the generic row
	// actions overlay. Its zero value is ModeList for legacy callers.
	actionsReturnMode Mode

	// command palette state. paletteReturnMode preserves the exact surface
	// (and its cursor/filter state) that was visible when ':' opened the
	// palette. Entries are derived on demand from that surface's selection and
	// the latest workspace projection, so they never retain stale entity
	// pointers across refreshes.
	paletteQuery      string
	paletteCursor     int
	paletteOffset     int
	paletteReturnMode Mode
	paletteMessage    string

	// unified search is the FTS5-backed cross-entity surface opened from the
	// palette. Its query accepts normal text plus explicit type:/workspace:/
	// harness:/profile:/from:/to: filter tokens; S and P retain their separate
	// literal transcript/plan search state below.
	unifiedQuery                 string
	unifiedResults               []unifiedsearch.Result
	unifiedCursor                int
	unifiedOffset                int
	unifiedInput                 bool
	unifiedLoading               bool
	unifiedSearchErr             error
	unifiedSearchSeq             int
	unifiedReturnMode            Mode
	pendingUnifiedPlanID         string
	pendingUnifiedConversationID string

	// config picker mode
	configPickerAccounts   []config.Account
	configPickerCursor     int
	configPickerCurrent    string
	configPickerPurpose    ConfigPickerPurpose
	configPickerReturnMode Mode
	configPickerProjectIdx int
	// handoffConversation is populated only while the profile picker is
	// choosing a target for an indexed-conversation handoff.
	handoffConversation *transcript.SessionMeta
	// handoffEdit keeps the selected handoff flow explicit while its target
	// profile is being chosen. H starts the fast flow; E opens the generated
	// draft in the configured editor before the target harness is launched.
	handoffEdit bool
	// isolatedForkConversation is retained only while the explicit worktree
	// fork confirmation is visible. Unlike the ordinary in-place fork, this
	// path creates durable Git state and therefore always asks first.
	isolatedForkConversation *transcript.SessionMeta

	// picker launch overrides: project index -> profile name
	pickerProfileOverride map[int]string

	// activity tracking: window_id → last activity time, sourced from daemon.
	// Empty when the daemon is unreachable; treated as "Active" by stateGroup.
	lastActivity  map[int]time.Time
	idleThreshold time.Duration
	// daemonStates is the raw state-query result captured with the live Kitty
	// list. The workspace projection consumes it asynchronously after each
	// refresh; list rendering continues to use the state already applied to
	// sessions above.
	daemonStates map[int]daemon.WindowStateSnapshot

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
	previewEnabled  bool
	previewWindowID int
	previewText     string
	previewError    string

	// transient status messages + launch-progress bookkeeping
	toasts                   []Toast
	launchBatchID            int
	launchBatchDone          int
	launchBatchErrors        int
	launchBatchQuitOnSuccess bool

	// pendingUndo stacks the most recent destructive actions eligible for
	// `u`-driven undo. Entries expire at their deadline; sweepUndo trims
	// them on toast ticks so memory stays bounded.
	pendingUndo []undoEntry

	// helpOffset is the first visible line index in the scrollable help
	// overlay (ModeHelp). Reset to 0 whenever the overlay is opened or closed.
	helpOffset     int
	helpReturnMode Mode

	// detail pane state (ModeDetail). Fields are zeroed when the pane is
	// closed so the placeholder render reads as a fresh state.
	detailWindowID   int                   // 0 when pane is closed
	detailEvents     []daemon.SessionEvent // events from the daemon for detailWindowID
	detailScroll     int                   // top row index for the scrollable view
	detailLoading    bool                  // true while the daemon query is in flight
	detailErr        error                 // last error from the daemon query
	detailReturnMode Mode

	// timeline sub-view controls (within ModeDetail). All five are zeroed by exitDetailMode.
	detailViewTimeline   bool
	detailTimelineScroll int
	detailTimelineFold   map[int]bool
	detailShowSuppressed bool
	detailShowTimestamps bool

	// transcript index + per-harness readers built once on demand and
	// reused across every ModeSessionSearch refresh/search. transcriptIndex
	// is nil until the first entry into ModeSessionSearch.
	transcriptIndex   *transcript.Index
	transcriptReaders map[string]transcript.Reader

	// Background refresh state for ModeSessionSearch. Service initialization
	// supplies the process-owned facade; entering the surface may then refresh
	// that facade through the shared writer gate.
	sessionRefreshInFlight bool

	// session search view (ModeSessionSearch). sessionResults holds the
	// most recent transcript.Search output. sessionQuery is the free-text
	// query typed after `/`. sessionSearchMode is true while the user is
	// actively editing sessionQuery (mirrors pickerSearchMode). The chip
	// fields hold the current harness/profile/project filters. sessionLoading
	// covers cache/query result loading; sessionRefreshInFlight tracks the
	// slower background scan so warmed rows can remain visible while it runs.
	sessionQuery      string
	sessionResults    []transcript.Result
	sessionCursor     int
	sessionOffset     int
	sessionSearchMode bool
	sessionLoading    bool
	sessionRefreshErr error
	sessionSearchErr  error
	sessionSearchSeq  int      // bumps on each Search dispatch; stale msgs are dropped
	filterHarness     []string // chip filter; "" means "no filter"
	filterProfile     []string
	filterProject     []string

	// chip picker overlay (ModeChipPicker) opened from ModeSessionSearch when
	// the user presses h/p/r to choose harness/profile/project. chipPickerKind
	// selects which filter is being edited. chipPickerAllOptions is the full
	// list captured at open time; chipPickerOptions is the visible list used by
	// the cursor, renderer, and selection (the leading empty string represents
	// "(any)" when visible). chipPickerCursor/Offset follow the same window
	// contract as sessionCursor/sessionOffset.
	chipPickerKind       chipPickerKind
	chipPickerAllOptions []string
	chipPickerOptions    []string
	chipPickerCursor     int
	chipPickerOffset     int
	chipPickerQuery      string
	chipPickerSearchMode bool

	// session viewer (ModeSessionView). viewerSession is the metadata of the
	// transcript currently being read; nil when the viewer is closed.
	// viewerMessages is the parsed message list. The viewer splits selection
	// from windowing: viewerCursor is the selected message row (always a
	// valid index into viewerMessages when the viewer is open), and
	// viewerScroll is the top-of-window index. j/k/g/G/pgup/pgdn move the
	// cursor; scroll follows via scrollViewerToCursor. viewerMatchLines
	// holds the indices into viewerMessages whose text contains the current
	// sessionQuery (lowercased substring match); viewerMatchIdx is the
	// cursor into viewerMatchLines for n/N navigation (which moves
	// viewerCursor to the chosen match). viewerLoading covers the async
	// ReadMessages call; viewerLoadSeq is a monotonic counter separate from
	// sessionSearchSeq so a stale viewer load arriving after the user moved
	// on is dropped.
	viewerSession    *transcript.SessionMeta
	viewerMessages   []transcript.Message
	viewerCursor     int
	viewerScroll     int
	viewerMatchIdx   int
	viewerMatchLines []int
	viewerLoading    bool
	viewerLoadErr    error
	viewerLoadSeq    int

	// delete flow (ModeDeleteChoice). deleteTarget holds the entry the user
	// selected with `d` from ModeSessionSearch; nil when no delete is in
	// progress. The prompt itself reads from this pointer.
	deleteTarget *transcript.SessionMeta

	// ModeMessageDetail overlay state. messageDetailIdx is the message
	// index captured at overlay open time; -1 means closed.
	// messageDetailScroll is the top wrapped-line index.
	messageDetailIdx    int
	messageDetailScroll int

	// plan search view (ModePlanSearch). plansIndex is lazy-constructed on first
	// entry and reused across refreshes. planResults holds the most recent
	// plans.Search output; planQuery is the text typed after `/`. planSearchMode
	// is true while editing planQuery. planSearchSeq dedupes in-flight
	// refresh/search dispatches the same way sessionSearchSeq does.
	plansIndex                *plans.Index
	planRefreshCommand        func(*plans.Index, *config.Config, int) tea.Cmd
	planSearchCommand         func(int, *plans.Index, plans.Query) tea.Cmd
	sessionWarmRefreshCommand func(int, transcript.Query) tea.Cmd
	sessionRefreshCommand     func(int) tea.Cmd
	sessionSearchCommand      func(int, transcript.Query) tea.Cmd
	planQuery                 string
	planResults               []plans.Result
	planCursor                int
	planOffset                int
	planSearchMode            bool
	planLoading               bool
	planRefreshErr            error
	planSearchErr             error
	planSearchSeq             int
	planSortMode              plans.SortMode // cycled with `s`; zero value = SortByMatches
	planViewReturnMode        Mode

	// plan viewer (ModePlanView). viewerPlan points at the plan meta the
	// user opened with Enter; nil when the viewer is closed. planViewerLines
	// is the flattened concatenation of every file in viewerPlan.Files with
	// "--- <rel> ---" separator rows between them. planViewerCursor is a
	// logical source-line index into planViewerLines; planViewerScroll is a
	// visual wrapped-row offset derived from those logical lines. The viewer
	// keeps selection on source lines so actions like `y` copy the raw line,
	// while windowing and page keys operate on wrapped terminal rows.
	// planViewerMatchLines holds the logical line indices containing planQuery
	// (case-insensitive substring); planViewerMatchIdx is the cursor into that
	// list for n/N navigation. planViewerLoadSeq is monotonic so a stale async
	// load (user already left the viewer or opened a different plan) is
	// dropped in Update.
	viewerPlan           *plans.PlanMeta
	planViewerLines      []string
	planViewerCursor     int
	planViewerScroll     int
	planViewerMatchIdx   int
	planViewerMatchLines []int
	planViewerLoading    bool
	planViewerLoadErr    error
	planViewerLoadSeq    int

	// workspaceSnapshot is the immutable, rendering-ready projection prepared
	// in the background for the secondary workspace-home view.
	workspaceSnapshot      workspace.Snapshot
	workspaceCursor        int
	workspaceFocus         workspaceFocus
	workspaceDrillDown     bool
	workspaceDetailCursor  int
	workspaceProjectOffset int
	workspaceDetailOffset  int
	// workspaceConversationMeta retains the complete indexed metadata needed
	// for a workspace conversation's smart-continue action. The rendering
	// snapshot intentionally exposes only a safe display subset.
	workspaceConversationMeta   map[string]transcript.SessionMeta
	workspaceCatalog            workspace.Catalog
	workspaceProjectionSeq      int
	workspaceLoading            bool
	workspaceLoadErr            error
	workspaceProjectionInFlight bool
	workspaceProjectionQueued   bool
	// workspaceProjectionDisabled keeps deterministic in-process UI fixtures
	// from opening the operator's SQLite/index files. Production models leave
	// it false; tests inject completed projection messages directly.
	workspaceProjectionDisabled bool
	// workspaceActivityMarker owns read-marker persistence for automatic opens
	// and palette actions. Tests inject a hermetic recorder; production lazily
	// opens the default datastore for each short mutation.
	workspaceActivityMarker actions.ActivityMarker
}

// NewModel creates a dashboard model.
func NewModel(cfg *config.Config, client kitty.KittyClient) Model {
	// The dashboard opens on the running-session list. The project workspace
	// home remains available as a secondary view through the v key.
	return newModel(cfg, client, 0, ModeList)
}

// NewModelWithDataServices creates the compatibility list model with an
// injected process owner. Tests and embedders can use this to control the TUI
// datastore lifecycle explicitly.
func NewModelWithDataServices(cfg *config.Config, client kitty.KittyClient, services *DataServices) Model {
	return newModelWithDataServices(context.Background(), cfg, client, 0, ModeList, services)
}

// NewModelFromWindow creates a dashboard model that initially selects the
// running session in the Kitty tab containing sourceWindowID. A zero or
// unmapped ID leaves the dashboard's normal initial selection unchanged.
func NewModelFromWindow(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) Model {
	return newModel(cfg, client, sourceWindowID, ModeList)
}

// NewModelFromWindowWithDataServices creates the live-session model with an
// injected process owner.
func NewModelFromWindowWithDataServices(cfg *config.Config, client kitty.KittyClient, sourceWindowID int, services *DataServices) Model {
	return newModelWithDataServices(context.Background(), cfg, client, sourceWindowID, ModeList, services)
}

// Close releases the model's process-scoped data services. RunFromWindow
// calls this lifecycle operation automatically; embedders that construct and
// run a Model directly should defer it themselves.
func (m Model) Close() error {
	if m.dataServices == nil {
		return nil
	}
	return m.dataServices.Close()
}

func newModel(cfg *config.Config, client kitty.KittyClient, sourceWindowID int, initialMode Mode) Model {
	return newModelWithDataServices(context.Background(), cfg, client, sourceWindowID, initialMode, NewDataServices())
}

func newModelWithDataServices(ctx context.Context, cfg *config.Config, client kitty.KittyClient, sourceWindowID int, initialMode Mode, services *DataServices) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if services == nil {
		services = NewDataServices()
	}
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
		ctx:                   ctx,
		dataServices:          services,
		interval:              interval,
		sortMode:              sort,
		attentionStates:       attn,
		backgroundCommands:    defaultModelBackgroundCommands(),
		daemonCommand:         daemonRequest,
		idleThreshold:         idle,
		focusedChipIdx:        -1,
		amxBin:                amxBin,
		deprecations:          cfg.Deprecations,
		messageDetailIdx:      -1,
		initialSourceWindowID: sourceWindowID,
		mode:                  initialMode,
		workspaceLoading:      initialMode == ModeWorkspace,
	}
}

// Init starts the refresh ticker and fetches the first session list.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.runInitialFetchCmd(m.initialSourceWindowID),
		m.runActiveLoopRefreshCmd(m.activeLoopRefreshSeq),
		tickCmd(m.interval),
		m.runHealthCmd(true, true),
		func() tea.Msg { return dataServicesInitRequestMsg{} },
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

	case dataServicesInitRequestMsg:
		return m, m.dispatchDataServicesInit()

	case dataServicesReadyMsg:
		return m.handleDataServicesReady(msg)

	case dataServicesRetryMsg:
		m.dataServicesRetryScheduled = false
		return m, m.dispatchDataServicesInit()

	case dataServicesRecoveryRequestMsg:
		return m.handleDataServicesRecoveryRequest(msg)

	case dataServicesRecoveryReadyMsg:
		return m.handleDataServicesRecoveryReady(msg)

	case workspaceProjectionMsg:
		return m.handleWorkspaceProjectionMsg(msg)

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

	case sessionWarmRefreshDoneMsg:
		return m.handleSessionWarmRefreshDone(msg)

	case sessionsRefreshDoneMsg:
		return m.handleSessionsRefreshDone(msg)

	case sessionSearchDebounceMsg:
		return m.handleSessionSearchDebounce(msg)

	case sessionSearchDoneMsg:
		return m.handleSessionSearchDone(msg)

	case plansRefreshDoneMsg:
		return m.handlePlansRefreshDone(msg)

	case planSearchDebounceMsg:
		return m.handlePlanSearchDebounce(msg)

	case planSearchDoneMsg:
		return m.handlePlanSearchDone(msg)

	case unifiedSearchDebounceMsg:
		return m.handleUnifiedSearchDebounce(msg)

	case unifiedSearchDoneMsg:
		return m.handleUnifiedSearchDone(msg)

	case planEditorErrMsg:
		return m.handlePlanEditorErr(msg)

	case planFilesLoadedMsg:
		return m.handlePlanFilesLoaded(msg)

	case viewerMessagesLoadedMsg:
		return m.handleViewerMessagesLoaded(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}
