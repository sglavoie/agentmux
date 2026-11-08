// The root program model. Conversation, plan browser, and ASL state are
// embedded by value from their model_*_state.go files; mode names and
// transition pulses live in model_modes.go and model_transitions.go.

package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/runform"
	"github.com/sglavoie/agentmux/tui/sendform"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// The root model's state. Construction, Init, and the Update dispatch table
// live in model_update.go; each field group's behaviour lives in the
// model_*.go file named for that view.

// Model is the root Bubble Tea model for the dashboard.
type Model struct {
	reviewEditor                                          reviewEditor
	reviewUndo                                            string
	reviewBusy                                            bool
	flightReport                                          *flightlog.Report
	flightRead                                            func(flightlog.Filters, []string, int) tea.Cmd
	flightFilters                                         flightlog.Filters
	flightLoading, flightDetail                           bool
	flightError, flightEventKind                          string
	flightSequence, flightTab, flightCursor, flightOffset int
	// deps
	client any
	cfg    *config.Config
	// Production watches the config off the update loop. Constructors remain
	// usable with an in-memory config by leaving configPath empty.
	configPath         string
	configInfo         os.FileInfo
	configInFlight     bool
	configError        string
	pendingConfig      *config.Config
	interval           time.Duration
	sortMode           string
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
	sessions []session.RunningSession // sorted full list
	filtered []session.RunningSession // sessions after query+filter applied
	// items is the flattened selectable projection of filtered: one entry per
	// line the dashboard list draws, in draw order. cursor and listOffset index
	// this, not filtered, because a line does not have to stand for exactly one
	// session. It is rebuilt from filtered — never edited in place — by
	// rebuildItems, which every write to filtered is followed by.
	items dashboard.ItemList
	// listView is the arrangement items is projected under. Its zero value is
	// dashboard.ViewUrgency, so the dashboard starts — and every construction
	// path that never mentions a view stays — on the historical list.
	//
	// collapsedFamilies records which family summaries are folded, keyed by
	// family key. An absent entry means expanded, so the zero map is "everything
	// open" and no view switch has to seed it. It is in-memory only: nothing
	// persists a view or a collapse across runs.
	listView          dashboard.ViewMode
	collapsedFamilies map[string]bool
	cursor            int
	listOffset        int // top item index for the scrollable list view in ModeList
	// Project switcher overlay (ModeProjectSwitcher). The scope list itself is
	// never stored: it is derived from the live session set on every frame and
	// every keypress, so a refresh landing under the open overlay updates its
	// tallies in place.
	//
	// projectSwitcherKey is therefore the whole of the selection — the
	// case-folded project identity, empty for All — and the cursor is resolved
	// from it against the list as it currently stands. An index would name a
	// different project the moment a refresh reordered the scopes.
	projectSwitcherKey    string
	projectSwitcherOffset int
	projectSwitcherQuery  string
	projectSwitcherSearch bool
	// initialSourceWindowID is the session window from which the dashboard was
	// opened, used to select the matching tab row. It is retained across
	// refreshes and cleared by the first usable topology result, so a startup
	// refresh that fails does not cost the operator their selection.
	initialSourceWindowID int
	// Live-topology refresh controller. This is the dashboard's window tree,
	// which is a different thing from sessionRefreshInFlight below: that one
	// tracks the Past Sessions transcript index scan.
	//
	// At most one refresh is in flight at a time. topologyRefreshNextSeq is the
	// counter sequences are drawn from, topologyRefreshActiveSeq is the one
	// currently outstanding (meaningful only while topologyRefreshInFlight),
	// and topologyRefreshPending is a single bit: every request that arrives
	// during a refresh collapses into it, so a burst of ticks and post-action
	// refreshes costs exactly one catch-up rather than one fetch each. The
	// first sequence is armed in the constructor because Init has a value
	// receiver and cannot persist the mutation that starting a refresh is.
	topologyRefreshNextSeq   int
	topologyRefreshActiveSeq int
	topologyRefreshInFlight  bool
	topologyRefreshPending   bool
	// topology describes the window tree the current session list was built
	// from: daemon snapshot or direct listing, and how old it is.
	//
	// topologyObservedAt is the model clock reading when that tree was applied.
	// Both are written only by a successful refresh, which is what makes the
	// pair survive an outage: a failed refresh leaves the last good tree and its
	// stamp in place, and the age rendered from them keeps growing on its own.
	// A zero topologyObservedAt is the cold state — no refresh has ever produced
	// a usable tree — and is the only condition under which a refresh failure is
	// fatal enough to render red.
	topology           commands.TopologyFreshness
	topologyObservedAt time.Time
	// lastSelectionRequestToken prevents a request left on a session window
	// after a failed best-effort clear from repeatedly stealing the cursor.
	lastSelectionRequestToken string

	// mode
	mode Mode

	// command palette state (ModeCommandPalette). commandContext snapshots the
	// dashboard row selected at entry so refreshes cannot retarget an action.
	// commandResults preserves catalog/group order after query filtering.
	commandQuery   string
	commandResults []commands.Command[Model]
	commandCursor  int
	commandOffset  int
	commandContext commands.Context

	// running-session filter (profile + state DSL)
	filterText   string           // live input while in ModeSearch
	activeFilter dashboard.Filter // committed filter applied to filtered
	savedFilter  dashboard.Filter // snapshot taken on enter; restored on Esc
	// savedSelection is the item the cursor was on when ModeSearch was entered,
	// captured beside savedFilter and restored with it. Cancelling a search puts
	// the operator back where they were, which is a filter and a selection, not
	// a filter alone.
	savedSelection dashboard.CursorIdentity
	completion     dashboard.Completion // sigil-aware popup state for ModeSearch

	// terminal dimensions
	width  int
	height int

	// last error from session query
	err error
	// portableErr is set while the tmux rows are kept from an earlier
	// listing (commands.PortableOutage); nil once a refresh reads them again.
	portableErr error

	// confirm / config-picker target
	targetSession session.RunningSession

	// confirm mode
	confirmAction      string
	confirmConsequence string
	confirmReturnMode  Mode

	// picker mode
	pickerSessions    []config.Session
	pickerRunning     map[string]int
	pickerDirProblems map[int]string // project index -> why its directory is unusable
	// pickerGen names one load of pickerSessions, so an asynchronous directory
	// check or completion started against an older list is dropped.
	pickerGen int
	// pickerStateWriter and pickerStateSeq order the launcher's saves.
	pickerStateWriter   *pickerStateWriter
	pickerStateSeq      uint64
	pickerSelected      map[int]bool
	pickerPinned        map[int]bool // project index -> persistent launcher pin
	pickerCursor        int
	pickerOffset        int    // top row index for the scrollable picker view
	pickerFilter        string // live filter buffer (DSL)
	pickerProfileFilter string // cycled default-profile filter; empty means all profiles
	pickerFilteredIdx   []int  // indices into pickerSessions for the filtered view
	// Restored when the launcher filter prompt is cancelled.
	pickerFilterSaved    string
	pickerSearchMode     bool // true while / is active and filter input is open
	pickerPreviewEnabled bool // picker-local preview toggle, independent of ModeList
	// directory prompt: open on pickerDirIdx while pickerDirMode is true
	pickerDirMode  bool
	pickerDirIdx   int
	pickerDirValue string
	pickerDirError string // why the last entry was refused; cleared by the next edit

	// last error from a close/restart action
	actionErr error

	// actionsCursor is the selected entry in the per-row session menu, indexing
	// sessionactions.MenuDefinitions rather than the menu's display rows: a band
	// rule is not selectable, and an index into rows would move under this model
	// whenever a band boundary did. A freshly opened menu puts it on
	// sessionactions.InitialSelection.
	actionsCursor int
	// actionsOffset is the first visible rendered menu row. It follows the cursor
	// rather than being steered on its own, so the selection can never scroll out
	// of the window; both are reset when the menu is opened or closed.
	actionsOffset int

	// sessionActions is the shared session-action controller's own state: the
	// action waiting on a picker or a confirmation, the request counter, and the
	// dispatches that have not yet reported back. It is held here rather than in
	// the controller because this model is copied by value on every update, and a
	// controller with mutable state of its own would be stale in whichever copy
	// the update loop went on to use.
	sessionActions sessionactions.State
	// pendingActionOrigin is where the operator was when they chose the action
	// about to be dispatched. It is written by the surface that offered the
	// action — the per-row menu, the palette — and consumed by the first
	// runSessionAction that follows, which is why it is cleared on read: a
	// direct dashboard binding must not inherit the palette query of whatever
	// ran before it.
	pendingActionOrigin sessionactions.Origin
	// actionHost holds the session IDs this dashboard is itself running in,
	// republished by every topology refresh. It answers "would this action
	// close the surface I am standing on", which is a different question from
	// every selection the dashboard makes and must not be confused with
	// initialSourceWindowID, which names the tab to SELECT and is spent by the
	// first refresh that resolves it.
	actionHost sessionactions.Host

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
	// picker launch overrides: project index -> working directory. Unlike the
	// profile choice it is never saved: it lasts until the launcher closes.
	pickerDirOverride map[int]string

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
	// hookHealthInterval cadences via a health command; deprecations is captured
	// once at startup from cfg.Deprecations. The in-flight flags keep a tick
	// from starting a check while the previous one of its kind is still out.
	amxBin                string
	daemonAlive           bool
	daemonHealthCheckedAt time.Time
	daemonHealthInFlight  bool
	hookHealth            commands.HookHealth
	hookHealthErr         error
	hookHealthCheckedAt   time.Time
	hookHealthInFlight    bool
	deprecations          []config.Deprecation
	deprecationDismissed  bool

	// health details overlay. Both fields are viewport state only: the issues it
	// shows are re-projected from the fields above on every frame, so a health
	// refresh that lands under the open overlay is visible on the next one.
	healthReturnMode Mode
	healthOffset     int

	// preview pane
	//
	// previewTargetSeq identifies what the pane is currently pointed at. A window
	// ID alone cannot: the cursor can leave a session for a family summary — which
	// owns no window and paints a project card instead of scrollback — and come
	// back, and a read issued before the detour would then match on window ID and
	// repaint the pane with scrollback captured for an older selection. Every
	// retarget bumps the counter and stamps it on the fetch, so a response is only
	// accepted for the target that asked for it.
	//
	// The periodic refresh deliberately does NOT bump it: a tick re-reads the
	// SAME target, and bumping per tick would mean that whenever a read takes
	// longer than the tick interval every response is superseded before it lands
	// and the pane never updates at all.
	// inspectorDisplay is the operator's standing intent for the dashboard
	// inspector and paneFocus is which of the two panes the keyboard is aimed
	// at. Neither is a statement about the current terminal: whether a split
	// actually fits is geometry, re-derived on every frame, so a resize that
	// takes the split away and one that gives it back leave the intent
	// untouched. The zero values are the defaults — an automatic inspector and
	// a focused queue — which is what makes the pane present by default without
	// any constructor saying so.
	inspectorDisplay inspectorDisplay
	paneFocus        paneFocus
	previewWindowID  int
	previewTargetSeq int
	// previewText is the latest fetched snapshot and the only one the model
	// retains. The one other copy that exists is the display snapshot
	// inspectorViewport takes when the operator scrolls back; nothing queues
	// reads or keeps a history of them, so a burst of refreshes costs two
	// captures at most. See tui/model_preview_read.go for the controller that
	// keeps it that way.
	previewText  string
	previewError string
	// The read controller's bookkeeping. previewRequestSeq is the identity
	// counter every fetch is stamped with, previewReadActiveSeq names the one
	// read currently out, and previewReadPending records that a newer target
	// wants a read as soon as that one settles. Request identity is deliberately
	// separate from previewTargetSeq: the target decides whether content may
	// PAINT, the request decides whether the in-flight slot may be RELEASED, and
	// a reply for an abandoned target must still do the second.
	previewRequestSeq    int
	previewReadActiveSeq int
	previewReadInFlight  bool
	previewReadPending   bool
	// previewPollSeq names the live polling chain. Each tick carries the chain
	// it was scheduled by and only the live one schedules its successor, so a
	// tick left over from a chain that was retired (hide/show, picker exit)
	// cannot fork the pane into two chains polling at once. Zero is never a live
	// chain: the first chain is 1.
	previewPollSeq int
	// previewLoaded records that a read has come back for the CURRENT target,
	// successfully or not. It is the one fact an empty previewText cannot carry:
	// a session that has printed nothing and a session whose first read is still
	// in flight both hold "", and the inspector says two different things about
	// them. Cleared on every retarget, set by the response handler.
	previewLoaded bool
	// inspectorViewport is where the inspector's output pane is looking. Its zero
	// value follows the tail, so a retarget resets it by assignment.
	inspectorViewport dashboard.OutputViewport
	// launcherBorrowedPane is the dashboard inspector's pane, set aside for as
	// long as the launcher is pointing the shared one at a row of its own. It is
	// non-nil for exactly that interval: nil means the pane above is the
	// dashboard's own, and a value means those fields currently describe the
	// launcher's preview and the dashboard's are in here.
	//
	// The pointer is never written through, only replaced, which is what makes it
	// safe on a model every handler copies by value.
	launcherBorrowedPane *dashboardPane
	// Mouse double-click state is process-local and identity-bearing. A row's
	// coordinates are never retained: every click arrives with the identity
	// captured by the last render and is revalidated against the live items.
	lastMouseSession dashboard.CursorIdentity
	lastMouseClickAt time.Time

	// transient status messages + launch-progress bookkeeping
	toasts                   []Toast
	toastTicking             bool
	feedbackOffset           int
	feedbackSelected         uint64
	nextNoticeID             uint64
	feedbackReturnMode       Mode
	launchBatchID            int
	launchBatchDone          int
	launchBatchErrors        int
	launchBatchQuitOnSuccess bool

	// recovery stacks the confirmed closes that `u` and the Undo Last Close
	// palette command may still reopen. It is the shared vocabulary's type
	// rather than a private slice because the overview is the second surface
	// that will offer the same affordance. Entries expire at their own
	// deadlines; sweepRecovery trims them on toast ticks so memory stays
	// bounded even when nothing is ever undone.
	recovery sessionactions.Recovery

	// helpOffset is the first visible line index in the scrollable help
	// overlay (ModeHelp). Reset to 0 whenever the overlay is opened or closed.
	helpOffset int

	// helpReturnMode is the mode the help overlay was raised from. Closing
	// help lands there, so a search query or open viewer survives the detour.
	helpReturnMode Mode

	// detail pane state (ModeDetail). Fields are zeroed when the pane is
	// closed so the placeholder render reads as a fresh state.
	detailMetadata bool
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

	modelHistoryState
	// Conversation search command seams and the query restored when search is cancelled.
	sessionWarmRefreshCommand func(int, transcript.Query) tea.Cmd
	sessionRefreshCommand     func(int) tea.Cmd
	sessionSearchCommand      func(int, transcript.Query) tea.Cmd
	sessionQuerySaved         string
	// worktree inventory view (ModeWorktrees). worktreeEntries keeps the last
	// successful Git-authoritative snapshot while worktreeFiltered is the
	// whitespace-AND filtered window. Refreshes preserve the selected stable ID
	// and retain cached rows until the matching sequence response arrives.
	worktreeEntries  []actions.WorktreeView
	worktreeFiltered []actions.WorktreeView
	worktreeCursor   int
	worktreeOffset   int
	worktreeFilter   string
	// Restored when the worktree filter prompt is cancelled.
	worktreeFilterSaved string
	worktreeFilterMode  bool
	worktreeLoading     bool
	worktreeWarnings    []string
	worktreeErr         error
	worktreeRefreshSeq  int
	worktreeSelectedID  string
	// Lifecycle detours retain a value copy of the selected row so refreshes
	// cannot retarget a confirmation, live-session choice, or profile choice.
	worktreeActionTarget   *actions.WorktreeView
	worktreeActionInFlight bool

	modelASLState

	// send is the send form (ModeSend). It holds the operator's draft and is
	// cleared when the form closes; unsent drafts and pending sends outlive it.
	send           sendform.State
	sendDrafts     sendform.Drafts
	sendOperations map[int]sendOperation
	// planRun is the run form (ModePlanRun), zeroed wholesale when it closes.
	// planRunOperations outlives it: the title of each plan a submission is
	// still out for, by seq, so a run started after esc still says so.
	planRun           runform.State
	planRunOperations map[int]string

	// Shared live-session ambiguity picker (ModeLiveSessionPicker). Any caller
	// that resolves a target to more than one live session raises it;
	// livePickerPurpose decides what Enter does and where Esc lands.
	livePickerCandidates []actions.WorktreeLiveSession
	livePickerCursor     int
	livePickerOffset     int
	livePickerPurpose    LivePickerPurpose
	livePickerTitle      string
	livePickerReturnMode Mode

	modelPlanState
}
