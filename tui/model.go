package tui

// The root model's state. Construction, Init, and the Update dispatch table
// live in model_update.go; each field group's behaviour lives in the
// model_*.go file named for that view.

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

// Model is the root Bubble Tea model for the dashboard.
type Model struct {
	// deps
	client             kitty.KittyClient
	cfg                *config.Config
	interval           time.Duration
	sortMode           string
	attentionStates    map[string]bool // states the bracket-key nav (]/[ and }/{) jumps to
	backgroundCommands modelBackgroundCommands
	daemonCommand      func(protocol.Request) (protocol.Response, error)
	// now is the render clock. Production uses time.Now; the UI test harness
	// freezes it so the seconds-resolution run timer renders identical text on
	// every run.
	now func() time.Time
	// handoffRunner and worktreeRunner are nil in production, which tells the
	// action registry to use its real workflows. Tests substitute recorders so
	// the viewer's H/E/w commands can execute without creating Git state or
	// writing to the operator's handoff directory.
	handoffRunner  handoff.Runner
	worktreeRunner worktree.Runner

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

	// command palette state (ModeCommandPalette). commandContext snapshots the
	// dashboard row selected at entry so refreshes cannot retarget an action.
	// commandResults preserves catalog/group order after query filtering.
	commandQuery   string
	commandResults []command
	commandCursor  int
	commandOffset  int
	commandContext commandContext

	// running-session filter (profile + state DSL)
	filterText     string               // live input while in ModeSearch
	activeFilter   dashboard.Filter     // committed filter applied to filtered
	savedFilter    dashboard.Filter     // snapshot taken on enter; restored on Esc
	focusedChipIdx int                  // -1 when no chip focused; otherwise index into Components()
	completion     dashboard.Completion // sigil-aware popup state for ModeSearch

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
	pickerPinned         map[int]bool // project index -> persistent launcher pin
	pickerCursor         int
	pickerOffset         int    // top row index for the scrollable picker view
	pickerFilter         string // live filter buffer (DSL)
	pickerProfileFilter  string // cycled default-profile filter; empty means all profiles
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

	// config picker mode
	configPickerAccounts   []config.Account
	configPickerCursor     int
	configPickerCurrent    string
	configPickerPurpose    launch.ConfigPickerPurpose
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
	// watchdog snapshot: window_id → last sweep-eligible action plus the
	// resolved threshold for that window. Populated alongside lastActivity
	// from the same TypeStateQuery round-trip; nil when the daemon is
	// unreachable. Drives the per-row age column in renderListAt.
	watchdog map[int]protocol.WatchdogState

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
	helpOffset int

	// helpReturnMode is the mode the help overlay was raised from. Closing
	// help lands there, so a search query or open viewer survives the detour.
	helpReturnMode Mode

	// detail pane state (ModeDetail). Fields are zeroed when the pane is
	// closed so the placeholder render reads as a fresh state.
	detailWindowID int                     // 0 when pane is closed
	detailEvents   []protocol.SessionEvent // events from the daemon for detailWindowID
	detailScroll   int                     // top row index for the scrollable view
	detailLoading  bool                    // true while the daemon query is in flight
	detailErr      error                   // last error from the daemon query

	// timeline sub-view controls (within ModeDetail). All five are zeroed by exitDetailMode.
	detailViewTimeline   bool
	detailTimelineScroll int
	detailTimelineFold   map[int]bool
	detailShowSuppressed bool
	detailShowTimestamps bool

	// transcript index + per-harness readers are loaded from the filesystem
	// cache at startup and reused across every ModeSessionSearch refresh/search.
	transcriptIndex   *transcript.Index
	transcriptReaders map[string]transcript.Reader

	// Background warm-up state for ModeSessionSearch. The startup cache load
	// publishes warm rows before the slower source refresh and retention prune.
	sessionCacheLoadInFlight bool
	sessionRefreshInFlight   bool

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

	// chip picker overlay (ModeChipPicker) opened from ModeSessionSearch or
	// ModePlanSearch to choose a categorical filter. chipPickerKind selects
	// which filter is being edited, while chipPickerReturnMode preserves the
	// owning search view. chipPickerAllOptions is the full
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
	chipPickerReturnMode Mode

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

	// worktree inventory view (ModeWorktrees). worktreeEntries keeps the last
	// successful Git-authoritative snapshot while worktreeFiltered is the
	// whitespace-AND filtered window. Refreshes preserve the selected stable ID
	// and retain cached rows until the matching sequence response arrives.
	worktreeEntries    []actions.WorktreeView
	worktreeFiltered   []actions.WorktreeView
	worktreeCursor     int
	worktreeOffset     int
	worktreeFilter     string
	worktreeFilterMode bool
	worktreeLoading    bool
	worktreeWarnings   []string
	worktreeErr        error
	worktreeRefreshSeq int
	worktreeSelectedID string
	// Lifecycle detours retain a value copy of the selected row so refreshes
	// cannot retarget a confirmation, live-session choice, or profile choice.
	worktreeActionTarget   *actions.WorktreeView
	worktreeActionInFlight bool

	// agent-session-loop runs view (ModeASLRuns). aslRows is the last snapshot
	// projection sorted by urgency; refreshes keep the cached rows and the
	// selected row_id so a poll landing under the cursor cannot move it.
	// aslLoaded distinguishes "no snapshot yet" from "a snapshot with no rows",
	// and aslStatus carries the daemon's availability verdict so the header can
	// say unavailable instead of rendering a false empty state.
	aslRows       []aslbridge.Row
	aslMeta       aslbridge.SnapshotMeta
	aslStatus     aslbridge.Status
	aslCursor     int
	aslOffset     int
	aslSelectedID string
	aslLoading    bool
	aslLoaded     bool
	aslErr        error
	aslRefreshSeq int
	// aslFocusTarget retains the row a focus detour was raised for, so an
	// ambiguity choice cannot be retargeted by a refresh landing mid-detour.
	aslFocusTarget string
	// aslResponse is the answer form (ModeASLResponse). It owns the fetched
	// question body and the operator's drafts and is zeroed wholesale on cancel,
	// success, and mode exit.
	aslResponse aslResponseState
	// aslOperations is the bounded body-free receipt window shown next to the
	// runs list, so an accepted submission can reach consumed after its form has
	// already closed.
	aslOperations []aslOperationView
	// aslActionTarget retains the run a stop/abort confirmation was raised for,
	// so a refresh landing under the overlay cannot retarget it.
	aslActionTarget    *aslActionTarget
	aslControlInFlight bool
	aslControlSeq      int
	// aslNotice explains the outcome of a deliberate action: a refusal, an
	// invalidated target, or an accepted control command. It is kept apart from
	// aslErr because aslErr also decides whether the held snapshot is
	// trustworthy, and an action refusal says nothing about the snapshot.
	aslNotice string

	// Shared live-session ambiguity picker (ModeLiveSessionPicker). Any caller
	// that resolves a target to more than one live Kitty session raises it;
	// livePickerPurpose decides what Enter does and where Esc lands.
	livePickerCandidates []actions.WorktreeLiveSession
	livePickerCursor     int
	livePickerOffset     int
	livePickerPurpose    LivePickerPurpose
	livePickerTitle      string
	livePickerReturnMode Mode

	// plan search view (ModePlanSearch). plansIndex is lazy-constructed on first
	// entry and reused across refreshes. planResults holds the most recent
	// plans.Search output; planQuery is the text typed after `/`. planSearchMode
	// is true while editing planQuery. planSearchSeq dedupes in-flight
	// refresh/search dispatches the same way sessionSearchSeq does.
	plansIndex                *plans.Index
	planCacheLoadInFlight     bool
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
	planSortMode              plans.SortMode // cycled with `o`; zero value = SortByMatches
	planFilterProject         plans.ProjectFilter

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
}
