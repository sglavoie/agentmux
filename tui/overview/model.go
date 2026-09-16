// The overview's running half: the Bubble Tea model that loads the live
// sessions, holds the selection, and paints the grid the rest of this package
// draws.
//
// It is a STANDALONE program rather than a mode of the root dashboard, and that
// is the design rather than a convenience. The root model warms transcripts,
// plans, health and agent-session-loop discovery on startup, owns the two Kitty
// writes a refresh may perform, and carries a dozen modes' state; an overview
// raised over a pane needs none of it and must not pay for any of it. So this
// file constructs nothing from tui and imports nothing from it: the live-session
// read arrives through commands.RefreshOverview, which is the dashboard's own
// reader with its write budget removed. Enter focuses a session; c changes only
// the daemon's in-memory view preference.
//
// Every effect is injected. The topology read, the focus action, the interval
// tick, and the clock are fields on a deps value, so a test drives the whole
// lifecycle — startup, coalescing, staleness, focus success and failure — with
// no daemon, no Kitty, and no wall clock. The Kitty client is stored only to be
// handed to the two default seams, which is why a test may construct the model
// with a nil client: with the seams replaced there is no path that touches it,
// and a regression that grew one would panic rather than pass.
package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// defaultRefreshInterval and defaultIdleThreshold are what an unset
// configuration means here. They are the dashboard's own defaults, restated
// rather than imported, because the two surfaces are separate programs and a
// silent divergence between their cadences would be invisible; the assertion
// that they agree belongs in a test, which is where it is.
const (
	defaultRefreshInterval = 2 * time.Second
	defaultIdleThreshold   = 5 * time.Minute
)

// firstRefreshSeq is the sequence the constructor arms.
//
// Sequences start at one so zero stays recognizable as "never issued" on the way
// back from a refresh command, which is the same contract commands.SessionsMsg
// documents.
const firstRefreshSeq = 1

// deps is every effect the overview starts away from its update loop, plus the
// clock.
//
// They are struct fields rather than package variables so two models in one test
// binary cannot see each other's recorders, and they are function values rather
// than an interface because each is one call with no state of its own.
type deps struct {
	preferences func(compact *bool) tea.Cmd
	// topology takes the refresh sequence and the startup source window ID. The
	// message it produces carries that sequence back, which is what lets the
	// controller tell its own completion from one that was superseded.
	topology func(seq, sourceWindowID int) tea.Cmd
	// focus acts on one Kitty tab. It is the only Kitty mutation this package can
	// reach, and only Enter calls it.
	focus func(tabID int) tea.Cmd
	// tick schedules the next interval. tea.Tick fires once, so every handler
	// that consumes a tick re-arms through here.
	tick func(d time.Duration) tea.Cmd
	// capture reads one window's terminal output. It is the surface's second
	// read, and the request it takes carries both identities the scheduler
	// settles on, which is what lets a test park a reply and deliver it later.
	capture func(req commands.CaptureRequest) tea.Cmd
	// captureTick schedules the next capture cadence. It is separate from tick
	// because the two cadences are different numbers and a shared seam would make
	// a test unable to say which ticker a model re-armed.
	captureTick func(d time.Duration) tea.Cmd
	// now is the clock every freshness reading is measured against.
	now func() time.Time
}

// defaultDeps binds the seams to the real world. It is the one place in the
// package that names a clock or builds a command that can reach Kitty.
func defaultDeps(cfg *config.Config, client kitty.KittyClient) deps {
	return deps{
		preferences: commands.OverviewPreferences,
		topology: func(seq, sourceWindowID int) tea.Cmd {
			return commands.RefreshOverview(client, cfg, seq, sourceWindowID)
		},
		focus: func(tabID int) tea.Cmd { return commands.Focus(client, tabID) },
		tick:  commands.Tick,
		capture: func(req commands.CaptureRequest) tea.Cmd {
			return commands.CaptureOutput(client, req)
		},
		captureTick: commands.CaptureTick,
		now:         time.Now,
	}
}

// model is the overview's whole state.
//
// The selection is held as an INDEX plus the tab identity that index resolved
// to. The index is what the grid and the navigator address, and the identity is
// what survives a refresh that reordered, added, or removed rows. Neither is the
// page: the page is derived from the index on every frame, so there is no third
// value to keep in step.
type model struct {
	cfg    *config.Config
	client kitty.KittyClient
	deps   deps

	interval      time.Duration
	idleThreshold time.Duration
	// captureInterval is the preview cadence, read from the preview settings and
	// run on its own ticker. A terminal's last few lines change far faster than
	// the set of terminals does.
	captureInterval time.Duration

	width, height         int
	compact               bool
	preferenceChanged     bool
	preferenceSaving      bool
	preferenceSavingValue bool
	preferenceErr         error
	quitAfterPreference   bool

	// sessions starts in urgency-first opening order, then retains the positions
	// of surviving tabs for the lifetime of this overview.
	sessions []session.RunningSession
	// selected indexes sessions; selectedTabID and selectedWindowID are the row
	// it last resolved to. The tab is the selection's identity and the window is
	// its current anchor, which is a separate fact: a tab whose harness restarted
	// keeps its ID and changes its anchor, and only one of those two changes
	// invalidates a preview.
	selected         int
	selectedTabID    int
	selectedWindowID int
	// sourceTabID marks the managed tab this overview was opened from,
	// independently of the keyboard selection and across window restarts.
	sourceTabID int

	// previews is keyed by Kitty window ID, the same key tile rendering reads: it
	// is what each tile SHOWS, and an absent entry is the honest loading state of
	// a window whose first capture has not landed. It is copied on write, so an
	// earlier generation of the model never sees a later one's captures.
	previews map[int]Preview
	// captures is what each tile is READING, and the one piece of state held by
	// pointer — see capture.go for why the slots are shared where the content is
	// copied.
	captures     *captureController
	lastActivity map[int]time.Time

	// topology and topologyObservedAt are the freshness of the tree on screen:
	// the age it already carried when it was applied, plus everything elapsed
	// since. loaded records that a snapshot has landed at all, which is the
	// difference between a surface that is still starting up and one whose list
	// is simply empty.
	topology           commands.TopologyFreshness
	topologyObservedAt time.Time
	loaded             bool
	err                error

	refreshNextSeq   int
	refreshActiveSeq int
	refreshInFlight  bool
	refreshPending   bool

	// startupSourceWindowID is the pane the overview was opened from, and it is
	// spent rather than remembered: it survives a failed first load so the retry
	// can still honor it, and is cleared by the first snapshot that resolves it
	// against a real window tree.
	startupSourceWindowID int

	// focusTabID is the tab a pending focus was dispatched against, and zero when
	// none is outstanding. It is the tab captured at the keypress rather than
	// whatever is selected when the answer comes back, and it is what makes a
	// second Enter a no-op instead of a second focus.
	focusTabID int
	focusErr   error
}

// newModel builds the overview with its first refresh already armed.
//
// Arming happens HERE and not in Init, and the reason is the receiver: Init
// takes the model by value, so a sequence it drew and an in-flight bit it set
// would be discarded along with its copy, and the first interval tick would see
// an idle controller and start a second concurrent startup refresh. Init's job
// is therefore only to DISPATCH the refresh this constructor already reserved.
func newModel(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) model {
	interval := time.Duration(cfg.Defaults.RefreshInterval) * time.Second
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	idle := time.Duration(cfg.Defaults.IdleThreshold) * time.Second
	if idle <= 0 {
		idle = defaultIdleThreshold
	}
	return model{
		cfg:                   cfg,
		client:                client,
		deps:                  defaultDeps(cfg, client),
		interval:              interval,
		idleThreshold:         idle,
		captureInterval:       captureIntervalFor(cfg),
		previews:              map[int]Preview{},
		captures:              newCaptureController(),
		startupSourceWindowID: sourceWindowID,
		refreshNextSeq:        firstRefreshSeq,
		refreshActiveSeq:      firstRefreshSeq,
		refreshInFlight:       true,
	}
}

// Init dispatches the refresh newModel armed and starts the two tickers.
//
// It starts no capture. There is nothing to capture yet — no window tree has
// landed and no terminal size has arrived — so the first reads are scheduled by
// the first snapshot and the first resize, whichever the runtime delivers first,
// rather than against a session list that does not exist.
//
// It deliberately starts nothing else either. The dashboard's Init also warms the
// transcript cache, runs a health probe, and discovers active loops; none of
// that is part of showing live panes, and an overview that paid for it would
// spend an operator's first second on work they cannot see.
func (m model) Init() tea.Cmd {
	var preferenceCmd tea.Cmd
	if m.deps.preferences != nil {
		preferenceCmd = m.deps.preferences(nil)
	}
	return tea.Batch(
		preferenceCmd,
		m.deps.topology(m.refreshActiveSeq, m.startupSourceWindowID),
		m.deps.tick(m.interval),
		m.deps.captureTick(m.captureInterval),
	)
}

// View paints the current state through this package's renderer.
//
// Everything the renderer needs is a field or a pure function of one, so the
// frame is a total function of the model: the same model renders the same bytes,
// which is what lets a lifecycle test assert on what an operator would see
// rather than only on what the model holds.
func (m model) View() tea.View {
	return tea.NewView(Render(Input{
		FocusPending:  m.focusTabID != 0,
		Closing:       m.quitAfterPreference,
		Compact:       m.compact,
		Width:         m.width,
		Height:        m.height,
		Sessions:      m.sessions,
		Selected:      m.selected,
		SourceTabID:   m.sourceTabID,
		Previews:      m.previews,
		Cfg:           m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		Now:           m.deps.now(),
		Feedback:      m.feedback(),
	}))
}

// selectedSession is the row the cursor is on, and false when there is no row to
// be on.
func (m model) selectedSession() (session.RunningSession, bool) {
	if m.selected < 0 || m.selected >= len(m.sessions) {
		return session.RunningSession{}, false
	}
	return m.sessions[m.selected], true
}

// Run opens the overview and blocks until the operator dismisses it or a focus
// succeeds.
//
// sourceWindowID is the pane the overview was launched from; its containing tab
// is the initial selection when one is found, and a zero or unresolvable ID
// leaves the first session selected instead.
//
// The signal handler is left off, as it is for the other surfaces raised in a
// Kitty overlay window: this model binds ctrl+c itself as dismissal, and a
// second handler racing that binding would make which of the two wins depend on
// scheduling.
func Run(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) error {
	p := tea.NewProgram(newModel(cfg, client, sourceWindowID), tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
