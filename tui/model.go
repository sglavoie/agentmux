// This file is the root program model itself: every field the TUI holds, the
// mode vocabulary those fields are read under, and the recent-transition
// pulses the dashboard paints from. Everything else in the root package
// answers a question about this struct or decides what a message does to it.

package tui

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
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

// The root model's state. Construction, Init, and the Update dispatch table
// live in model_update.go; each field group's behaviour lives in the
// model_*.go file named for that view.

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
	// railRequested is the operator's process-local preference; geometry may
	// still suppress the rail when it would leave the session list under its
	// 80-cell guarantee. railFocused and the cursor/offset belong to the rail's
	// independent keyboard viewport.
	railRequested bool
	railFocused   bool
	railCursor    int
	railOffset    int
	// initialSourceWindowID is the Kitty window from which the dashboard was
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
	// hookHealthInterval cadences via a health command; deprecations is captured
	// once at startup from cfg.Deprecations.
	amxBin                string
	daemonAlive           bool
	daemonHealthCheckedAt time.Time
	hookHealth            commands.HookHealth
	hookHealthErr         error
	hookHealthCheckedAt   time.Time
	deprecations          []config.Deprecation
	deprecationDismissed  bool

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
	previewEnabled   bool
	previewWindowID  int
	previewTargetSeq int
	previewText      string
	previewError     string

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
	aslResponse asl.ResponseState
	// aslOperations is the bounded body-free receipt window shown next to the
	// runs list, so an accepted submission can reach consumed after its form has
	// already closed.
	aslOperations []asl.OperationView
	// aslActionTarget retains the run a stop/abort confirmation was raised for,
	// so a refresh landing under the overlay cannot retarget it.
	aslActionTarget    *asl.ActionTarget
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

// Mode represents the current UI mode.
type Mode int

const (
	ModeList              Mode = iota
	ModeSearch                 // typing a filter expression (DSL)
	ModeConfirm                // y/n confirmation for a destructive action
	ModePicker                 // multi-select session launcher
	ModeConfigPicker           // account picker for config override
	ModeHelp                   // help overlay
	ModeDetail                 // detail pane: per-session hook event log
	ModeActions                // per-row actions overlay
	ModeSessionSearch          // searchable list of past harness transcripts
	ModeSessionView            // scrollable transcript viewer for a single past session
	ModeDeleteChoice           // three-way prompt: hide from index, also delete file, cancel
	ModeChipPicker             // overlay listing chip values for ModeSessionSearch
	ModeMessageDetail          // full-message overlay opened from ModeSessionView
	ModeWorktrees              // managed Git worktree inventory and non-destructive actions
	ModePlanSearch             // searchable list of past agent-plan directories
	ModePlanView               // inline pager for a single plan directory
	ModeCommandPalette         // searchable dashboard command catalog
	ModeLiveSessionPicker      // ambiguous live Kitty sessions for one target
	ModeASLRuns                // agent-session-loop active and actionable runs
	ModeASLResponse            // answer editor and closure buttons for one ASL question
)

// LivePickerPurpose names the caller that raised ModeLiveSessionPicker. The
// picker only chooses among candidates; the purpose decides what Enter does
// with the chosen one and which root view the detour returns to.
type LivePickerPurpose int

const (
	LivePickerWorktreeReopen LivePickerPurpose = iota
	LivePickerASLFocus
)

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
		// Advance the snapshot to the new state. Without this, a session
		// that changed once and remained stable would re-pulse forever:
		// after sweepRecentTransitions drops the entry past pulseDecay,
		// the next refresh still sees prev != s.State and would record a
		// fresh pulse. Updating here ties the pulse strictly to the
		// observed change rather than the persistent mismatch between
		// the (stale) snapshot and current state.
		m.lastSeenStates[s.WindowID] = s.State
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

// modelNow reads the render clock, falling back to the wall clock for a Model
// built as a bare literal — which is how most of this package's render tests
// construct their input. Anything that stamps or compares a display time must
// go through here, or the scenario harness cannot freeze it.
func modelNow(m Model) time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}
