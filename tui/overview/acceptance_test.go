// The overview as an operator meets it: the real model, the real update loop,
// the real retrieval and capture commands, and a Kitty that answers.
//
// Every other test in this package replaces a seam with a recorder that produces
// nothing, which is what makes a single rule — coalescing, fairness, the
// selection reconcile — assertable in isolation. These are the other half. They
// keep commands.RefreshOverview, commands.CaptureOutput and commands.Focus
// exactly as the surface ships them and replace the WORLD underneath, so what is
// under test is the whole path from a keypress to a `kitten @ get-text` and back
// to a painted frame.
//
// Four properties make that possible without a terminal and without a clock.
//
// The WORLD is one mutable Kitty instance (kittyWorld), serving both the
// daemon's published snapshot and the direct listing from the same tree, so a
// tab that closes closes for every reader at once. It answers ListWindows,
// GetText and FocusTab freely; every other method is measured against a PERMIT
// SET that starts empty and that only the scenario may widen. A write the
// scenario asked for by running a session action is RECORDED with what it named
// its target; a write nobody asked for is recorded as a violation rather than
// panicking, because "the overview wrote nothing to Kitty except what an
// operator asked it to" is a claim these tests make about a whole session of use
// and a claim is better evidence than a stack trace.
//
// The CONFIGURATION is a TOML file written into the test's own directory and
// loaded through config.Load, so the profiles, colors, projects and preview
// cadence are the operator-shaped ones with real normalization applied, and no
// test can read the operator's own config.
//
// The CLOCK is frozen and advanced explicitly, and the two TICKERS are replaced
// by counters. Nothing here sleeps: a cadence is exercised by SENDING the tick
// message the ticker would have produced, which is what makes a two-hundred-tick
// burst a loop rather than a two-minute test.
//
// And the EFFECTS are PARKED. Each seam builds the real command and puts it in a
// FIFO the test owns instead of running it, so "a capture is still out" and "a
// refresh landed while the operator was paging" are ordinary statements about
// which job the test chose to run next rather than races to lose.
//
// Like the tests in tui/commands, these replace that package's StateQuery and
// LocalKittySocket variables, so none of them may run in parallel.
package overview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// acceptanceSocket is the Kitty instance every fixture below belongs to. The
// daemon's snapshot is stamped with it and the surface is told it is running
// inside it, which is what makes the daemon-first path usable at all: an
// unattributed snapshot is refused and falls back to a direct listing.
const acceptanceSocket = "unix:/tmp/kitty-acceptance-4242"

// kittyWorld is one Kitty instance that answers.
//
// It is deliberately not a fixed tree. Tabs close, a listing starts failing, a
// window stops answering GetText, and a focus is refused — each of those is a
// state the surface has to survive, and each is a mutation of this value made
// between two messages.
type kittyWorld struct {
	mu sync.Mutex

	tabs     []kitty.Tab
	text     map[int]string
	listErr  error
	textErr  map[int]error
	focusErr error

	lists   int
	reads   []int
	focuses []string
	// violations is every call to a method neither the retrieval nor the capture
	// is allowed to make, and which the scenario did not explicitly permit. It is
	// recorded rather than fatal because the assertion these tests want to make is
	// "over a whole session of use, none happened", and that reads better as an
	// empty slice than as a panic in a goroutine.
	violations []string
	// permitted names the write methods THIS scenario has asked for by running a
	// session action, and calls is what those methods were actually called with.
	//
	// The permit set is per scenario and starts empty, which is what keeps the
	// read-only claim intact now that the grid can mutate: a scenario that only
	// browses permits nothing, so any write it makes is still a violation, and a
	// scenario that closes a row permits exactly the methods a close performs and
	// then asserts on the recorded call rather than on the absence of one.
	permitted map[string]bool
	calls     []worldCall

	// closeErr, when set, is what CloseTab answers, so a close that Kitty refuses
	// can be told apart from one it accepted but was never seen to finish.
	closeErr error
	// nextTabID is the identity the next launched tab takes. It starts above
	// every fixture ID so a relaunched session is visibly a NEW tab rather than
	// the one that was closed.
	nextTabID int
}

// worldCall is one write the scenario permitted: which method, and the match
// expression or title it named its target with.
type worldCall struct {
	method string
	arg    string
}

func newKittyWorld(tabs []kitty.Tab) *kittyWorld {
	return &kittyWorld{
		tabs:      tabs,
		text:      map[int]string{},
		textErr:   map[int]error{},
		permitted: map[string]bool{},
		nextTabID: 900,
	}
}

// permit widens the write set for the rest of the scenario. It is called by the
// test that is about to run an action, never by the harness, so what a scenario
// is allowed to do to Kitty is stated in the scenario.
func (w *kittyWorld) permit(methods ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, method := range methods {
		w.permitted[method] = true
	}
}

// permitClose and permitLaunch are the two permit sets worth naming, because
// neither is one method: a close polls the tree after CloseTab, and a launch
// repaints the new tab's color and may focus a neighbour to place it.
func (w *kittyWorld) permitClose()  { w.permit("CloseTab") }
func (w *kittyWorld) permitLaunch() { w.permit("LaunchTab", "SetTabColor") }

func (w *kittyWorld) ListWindows() ([]kitty.OSWindow, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lists++
	if w.listErr != nil {
		return nil, w.listErr
	}
	return w.copyTree(), nil
}

func (w *kittyWorld) GetText(match, _ string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, tab := range w.tabs {
		for _, win := range tab.Windows {
			if match != kitty.MatchWindowByID(win.ID) {
				continue
			}
			w.reads = append(w.reads, win.ID)
			if err := w.textErr[win.ID]; err != nil {
				return "", err
			}
			return w.text[win.ID], nil
		}
	}
	// A read of a window that has closed since it was scheduled. Kitty answers
	// with a match failure rather than empty output, and so does this.
	return "", fmt.Errorf("kitten @ get-text: no matching window for %s", match)
}

func (w *kittyWorld) FocusTab(match string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.focuses = append(w.focuses, match)
	if w.focusErr != nil {
		return w.focusErr
	}
	for _, tab := range w.tabs {
		if match == kitty.MatchTabByID(tab.ID) {
			return nil
		}
	}
	return fmt.Errorf("kitten @ focus-tab: no matching tab for %s", match)
}

// record files one write: against the permit set if the scenario asked for it,
// against the violations if it did not.
func (w *kittyWorld) record(method, arg string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.permitted[method] {
		w.violations = append(w.violations, method)
		return false
	}
	w.calls = append(w.calls, worldCall{method: method, arg: arg})
	return true
}

// LaunchTab adds the tab the launch asked for, so the launcher's own
// find-the-new-tab-and-color-it step succeeds on its first look instead of
// backing off against a world that never grew one.
func (w *kittyWorld) LaunchTab(opts kitty.LaunchOpts) error {
	if !w.record("LaunchTab", opts.Title) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	id := w.nextTabID
	w.nextTabID++
	w.tabs = append(w.tabs, kitty.Tab{
		ID:    id,
		Title: opts.Title,
		Windows: []kitty.Window{{
			ID:       id * 10,
			IsActive: true,
			UserVars: kitty.UserVars{
				Profile:      opts.UserVars["amx_profile"],
				Harness:      opts.UserVars["amx_harness"],
				Project:      opts.UserVars["amx_project"],
				AMXDirectory: opts.UserVars["amx_directory"],
				LaunchID:     opts.UserVars["amx_launch_id"],
			},
		}},
	})
	return nil
}

// CloseTab removes the tab, which is what makes the close poll confirm on its
// first look and what makes the next refresh have to reconcile a selection onto
// a survivor.
func (w *kittyWorld) CloseTab(match string) error {
	if !w.record("CloseTab", match) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closeErr != nil {
		return w.closeErr
	}
	kept := make([]kitty.Tab, 0, len(w.tabs))
	for _, tab := range w.tabs {
		if kitty.MatchTabByID(tab.ID) == match {
			continue
		}
		kept = append(kept, tab)
	}
	w.tabs = kept
	return nil
}

func (w *kittyWorld) LaunchOverlay(opts kitty.LaunchOpts) error {
	w.record("LaunchOverlay", opts.Title)
	return nil
}
func (w *kittyWorld) MoveTab(match string, _ int) error { w.record("MoveTab", match); return nil }
func (w *kittyWorld) SetTabColor(match string, _, _, _, _ string) error {
	w.record("SetTabColor", match)
	return nil
}
func (w *kittyWorld) SetTabTitle(match, title string) error {
	w.record("SetTabTitle", match+" "+title)
	return nil
}
func (w *kittyWorld) SetUserVars(match string, _ map[string]string) error {
	w.record("SetUserVars", match)
	return nil
}
func (w *kittyWorld) FocusWindow(match string) error { w.record("FocusWindow", match); return nil }
func (w *kittyWorld) LaunchOverlayPager(match string) error {
	w.record("LaunchOverlayPager", match)
	return nil
}

// daemonTree is the same world seen through the daemon's published snapshot. It
// deliberately does NOT count as a listing: the whole point of the daemon-first
// path is that the daemon already ran `kitten @ ls` and the surface runs none of
// its own, and a helper that counted both would make that unassertable.
func (w *kittyWorld) daemonTree() ([]kitty.OSWindow, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.listErr != nil {
		return nil, w.listErr
	}
	return w.copyTree(), nil
}

// copyTree deep-copies to the window, which is the level every mutation below
// edits, so a reader holding an earlier answer never observes a later one.
func (w *kittyWorld) copyTree() []kitty.OSWindow {
	tabs := make([]kitty.Tab, len(w.tabs))
	for i, tab := range w.tabs {
		tabs[i] = tab
		tabs[i].Windows = append([]kitty.Window(nil), tab.Windows...)
	}
	return []kitty.OSWindow{{Tabs: tabs}}
}

// closeTab is a session ending: the tab and its windows leave the world, so the
// next listing, the next snapshot, and any capture already out for it all see
// the same disappearance.
func (w *kittyWorld) closeTab(tabID int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := make([]kitty.Tab, 0, len(w.tabs))
	for _, tab := range w.tabs {
		if tab.ID != tabID {
			kept = append(kept, tab)
		}
	}
	w.tabs = kept
}

func (w *kittyWorld) setListErr(err error)  { w.mu.Lock(); w.listErr = err; w.mu.Unlock() }
func (w *kittyWorld) setFocusErr(err error) { w.mu.Lock(); w.focusErr = err; w.mu.Unlock() }
func (w *kittyWorld) setText(windowID int, s string) {
	w.mu.Lock()
	w.text[windowID] = s
	w.mu.Unlock()
}

func (w *kittyWorld) readCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.reads)
}

func (w *kittyWorld) readsOf(windowID int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, id := range w.reads {
		if id == windowID {
			n++
		}
	}
	return n
}

func (w *kittyWorld) listCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lists
}

func (w *kittyWorld) focusCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.focuses)
}

func (w *kittyWorld) violationList() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.violations...)
}

// callsOf names the arguments one permitted method was called with, in order.
func (w *kittyWorld) callsOf(method string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []string{}
	for _, call := range w.calls {
		if call.method == method {
			out = append(out, call.arg)
		}
	}
	return out
}

// tabIDs is the world's tabs as they now stand, which is how a scenario says a
// close removed one and a relaunch added another.
func (w *kittyWorld) tabIDs() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int, 0, len(w.tabs))
	for _, tab := range w.tabs {
		out = append(out, tab.ID)
	}
	return out
}

// acceptanceProfiles are the three launch identities every fixture spreads its
// sessions across, so no scenario below is accidentally single-profile.
var acceptanceProfiles = []struct{ name, harnessName string }{
	{"work", "claude"},
	{"personal", "codex"},
	{"review", "copilot"},
}

// acceptanceProject is the project the nth session belongs to. The names are
// zero-padded because Order sorts groups by label, and a padded name makes the
// tile order equal the index order — which is what lets a paging assertion name
// the windows it expects rather than recomputing the sort.
func acceptanceProject(index int) string { return fmt.Sprintf("svc%02d", index+1) }

// acceptanceTab is one managed session: its own tab, its own window, its own
// project, and a profile drawn from the three.
func acceptanceTab(index int) kitty.Tab {
	p := acceptanceProfiles[index%len(acceptanceProfiles)]
	project := acceptanceProject(index)
	return kitty.Tab{
		ID:    100 + index,
		Title: project,
		Windows: []kitty.Window{{
			ID:       1000 + index,
			IsActive: true,
			UserVars: kitty.UserVars{
				Profile:      p.name,
				Harness:      p.harnessName,
				Project:      project,
				AMXDirectory: "/work/" + project,
				LaunchID:     project + "-launch",
			},
		}},
	}
}

func acceptanceTabs(n int) []kitty.Tab {
	out := make([]kitty.Tab, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, acceptanceTab(i))
	}
	return out
}

// acceptanceTabID and acceptanceWindowID name the nth session's Kitty
// identities, which is the vocabulary every assertion below is written in.
func acceptanceTabID(index int) int    { return 100 + index }
func acceptanceWindowID(index int) int { return 1000 + index }

// acceptanceConfig is an operator-shaped configuration in the test's own
// directory: three profiles with the colors the tiles are painted with, a
// project per profile, and an explicit preview cadence.
//
// It goes through config.Load rather than being constructed as a struct so the
// defaults these scenarios depend on — the refresh interval, the idle threshold,
// the preview floor — are the ones normalization actually produces.
func acceptanceConfig(t *testing.T) *config.Config {
	t.Helper()
	var b strings.Builder
	b.WriteString("[defaults]\nsort = \"alpha\"\nrefresh_interval = 2\nidle_threshold = 300\nconfirm_destructive = true\n\n")
	b.WriteString("[defaults.preview]\nrefresh_ms = 400\n\n")
	// The two writers the launch path runs beside Kitty. They are off because
	// this test owns a Kitty and a configuration, not a state directory; see the
	// XDG isolation in newStage for what happens if a default moves.
	b.WriteString("[defaults.diagnostics]\njournal_enabled = false\nnative_logging = false\n\n")
	for i, p := range acceptanceProfiles {
		fmt.Fprintf(&b, "[[profiles]]\nname = %q\nharness = %q\nhome_dir = %q\ncolor = %q\n\n",
			p.name, p.harnessName, filepath.Join(t.TempDir(), p.name), []string{"blue", "purple", "green"}[i])
	}
	for i := range acceptanceProfiles {
		project := acceptanceProject(i)
		fmt.Fprintf(&b, "[[projects]]\ntitle = %q\ndirectory = %q\nprofile = %q\n\n",
			project, "/work/"+project, acceptanceProfiles[i].name)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing the isolated config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading the isolated config: %v", err)
	}
	return cfg
}

// stubOverviewWorld points the shared retrieval at this test's Kitty: its
// snapshot comes from the world's own tree and its instance identity is the
// world's socket. Without both, a refresh would dial the operator's live daemon
// and name the operator's own Kitty.
func stubOverviewWorld(t *testing.T, world *kittyWorld) {
	t.Helper()
	previousQuery, previousSocket := commands.StateQuery, commands.LocalKittySocket
	commands.StateQuery = func() (protocol.Response, error) {
		tree, err := world.daemonTree()
		if err != nil {
			return protocol.Response{}, err
		}
		return protocol.Response{OK: true, KittySnapshot: &protocol.KittySnapshot{
			Ready:      true,
			Socket:     acceptanceSocket,
			Generation: 1,
			CapturedAt: viewNow.Unix(),
			AgeMS:      250,
			Windows:    tree,
		}}, nil
	}
	commands.LocalKittySocket = func() string { return acceptanceSocket }
	t.Cleanup(func() {
		commands.StateQuery, commands.LocalKittySocket = previousQuery, previousSocket
	})
}

// parkedJob is one real command the model asked for and the test has not run
// yet. The kind is what a scenario names when it decides which effect completes
// next.
type parkedJob struct {
	kind string
	run  tea.Cmd
}

const (
	jobTopology = "topology"
	jobCapture  = "capture"
	jobFocus    = "focus"
	jobAction   = "action"
	jobRelaunch = "relaunch"
)

// stage is one running overview: the model, the world it reads, the frozen
// clock, and the parked effects.
type stage struct {
	t     *testing.T
	world *kittyWorld
	cfg   *config.Config
	m     model
	now   time.Time

	jobs []parkedJob
	quit bool

	ticks        []time.Duration
	captureTicks []time.Duration

	// dispatches and relaunches are what the two mutating seams were asked for,
	// recorded when the seam is CALLED rather than when the parked command runs,
	// so a scenario can assert what an action aimed at without ever letting it
	// reach the world.
	dispatches []sessionactions.Dispatch
	relaunches []sessionactions.Snapshot
}

// newStage opens the overview over a world and hands back the surface as it
// stands after Init and the runtime's first size message — which is exactly the
// state an operator's first frame is drawn from, with the startup read out and
// no capture yet issued.
func newStage(t *testing.T, world *kittyWorld, width, height, sourceWindowID int) *stage {
	t.Helper()
	stubOverviewWorld(t, world)
	// The launch path writes a diagnostics journal and maintains native logs
	// under the XDG state directory. The configuration below disables both, and
	// this is the second half of that isolation: a default that flipped, or a
	// path that stopped consulting the configuration, would write into the
	// operator's own ~/.local/state rather than into a directory the test owns.
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s := &stage{t: t, world: world, cfg: acceptanceConfig(t), now: viewNow}
	s.m = newModel(s.cfg, world, sourceWindowID)
	// The REAL seams, kept and then parked. Building the command is the model's
	// decision and running it is the test's, which is the whole of "no sleeps".
	real := s.m.deps
	s.m.deps = deps{
		topology: func(seq, src int) tea.Cmd {
			s.park(jobTopology, real.topology(seq, src))
			return nil
		},
		focus: func(tabID int) tea.Cmd {
			s.park(jobFocus, real.focus(tabID))
			return nil
		},
		// The action is stamped HERE rather than left to the controller. The
		// controller stamps whatever the seam RETURNS, and this seam returns
		// nothing so the effect can be parked; stamping the parked command with
		// the request the dispatch already carries is what keeps a completion
		// delivered minutes later correlating with the dispatch that asked for it.
		action: func(d sessionactions.Dispatch) tea.Cmd {
			s.dispatches = append(s.dispatches, d)
			s.park(jobAction, commands.StampRequest(real.action(d), uint64(d.Request)))
			return nil
		},
		relaunch: func(request sessionactions.RequestID, snapshot sessionactions.Snapshot) tea.Cmd {
			s.relaunches = append(s.relaunches, snapshot)
			s.park(jobRelaunch, real.relaunch(request, snapshot))
			return nil
		},
		capture: func(req commands.CaptureRequest) tea.Cmd {
			s.park(jobCapture, real.capture(req))
			return nil
		},
		tick: func(d time.Duration) tea.Cmd {
			s.ticks = append(s.ticks, d)
			return nil
		},
		captureTick: func(d time.Duration) tea.Cmd {
			s.captureTicks = append(s.captureTicks, d)
			return nil
		},
		now: func() time.Time { return s.now },
	}

	// The read-only boundary, asserted once per scenario over everything that
	// scenario did rather than at each step.
	t.Cleanup(func() {
		if got := world.violationList(); len(got) > 0 {
			t.Errorf("the overview called %v on Kitty; it may only list, read text, focus a tab, and perform the writes this scenario permitted", got)
		}
	})

	s.exec(s.m.Init())
	s.resize(width, height)
	return s
}

func (s *stage) park(kind string, cmd tea.Cmd) {
	s.jobs = append(s.jobs, parkedJob{kind: kind, run: cmd})
}

// send runs one message through the real Update and then everything it produced.
func (s *stage) send(msg tea.Msg) {
	s.t.Helper()
	next, cmd := s.m.Update(msg)
	m, ok := next.(model)
	if !ok {
		s.t.Fatalf("Update returned %T, want the overview model", next)
	}
	s.m = m
	s.exec(cmd)
}

// exec is the whole Bubble Tea runtime these tests need: run the command, walk a
// batch, notice a quit, and feed anything else back in. It is recursive because
// a batch can contain a batch.
func (s *stage) exec(cmd tea.Cmd) {
	s.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, inner := range msg {
			s.exec(inner)
		}
	case tea.QuitMsg:
		s.quit = true
	default:
		s.send(msg)
	}
}

// pending counts the effects of one kind the world has not answered yet, which
// for captures is the same number as the scheduler's in-flight count.
func (s *stage) pending(kind string) int {
	n := 0
	for _, job := range s.jobs {
		if job.kind == kind {
			n++
		}
	}
	return n
}

// deliver runs exactly the jobs of one kind that are parked RIGHT NOW, leaving
// anything they schedule for the next call.
//
// Snapshotting first is what makes the helper terminate and what makes it
// meaningful: "the four reads that were out have now landed" is a different
// statement from "keep running captures until none are left", and the scenarios
// below depend on the first.
func (s *stage) deliver(kind string) int {
	s.t.Helper()
	var due, rest []parkedJob
	for _, job := range s.jobs {
		if job.kind == kind {
			due = append(due, job)
			continue
		}
		rest = append(rest, job)
	}
	s.jobs = rest
	for _, job := range due {
		s.exec(job.run)
	}
	return len(due)
}

// deliverOne answers the oldest outstanding job of a kind, which is how a
// scenario completes one read and leaves the rest hanging.
func (s *stage) deliverOne(kind string) bool {
	s.t.Helper()
	for i, job := range s.jobs {
		if job.kind != kind {
			continue
		}
		s.jobs = append(s.jobs[:i:i], s.jobs[i+1:]...)
		s.exec(job.run)
		return true
	}
	return false
}

func (s *stage) tick()        { s.t.Helper(); s.send(commands.TickMsg{}) }
func (s *stage) captureTick() { s.t.Helper(); s.send(commands.CaptureTickMsg{}) }

func (s *stage) resize(width, height int) {
	s.t.Helper()
	s.send(tea.WindowSizeMsg{Width: width, Height: height})
}

func (s *stage) press(name string) { s.t.Helper(); s.send(key(s.t, name)) }

func (s *stage) advance(d time.Duration) { s.now = s.now.Add(d) }

// refresh is the ordinary cadence in one call: the ticker fires and the read it
// asked for lands.
func (s *stage) refresh() {
	s.t.Helper()
	s.tick()
	s.deliver(jobTopology)
}

// frame is what an operator would see, styling stripped.
func (s *stage) frame() string { return frameOf(s.m) }

// previewWindows names the windows the surface is currently holding output for,
// in tile order, which is the form every retention claim below is written in.
func (s *stage) previewWindows() []int {
	out := make([]int, 0, len(s.m.previews))
	for _, sess := range s.m.sessions {
		if _, ok := s.m.previews[sess.WindowID]; ok {
			out = append(out, sess.WindowID)
		}
	}
	return out
}

// targetWindows names the windows the scheduler currently believes are visible,
// in tile order.
func (s *stage) targetWindows() []int {
	out := make([]int, 0, len(s.m.captures.targets))
	for _, sess := range s.m.sessions {
		if _, ok := s.m.captures.targets[sess.WindowID]; ok {
			out = append(out, sess.WindowID)
		}
	}
	return out
}

// windowRange names count consecutive sessions' windows starting at an index,
// which is what a page is in every assertion below.
func windowRange(first, count int) []int {
	out := make([]int, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, acceptanceWindowID(first+i))
	}
	return out
}

// TestAcceptanceTheSurfaceLoadsAWholeMultiProfileUniverse is the starting point
// every other scenario assumes: eleven sessions across three profiles and eleven
// projects, read through the daemon's snapshot, laid out four to a page, and
// drawn with the first page's four reads out and nothing else.
func TestAcceptanceCompletedAgentRemainsDoneDuringASLContinuation(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(1))
	s := newStage(t, world, 120, 30, 0)
	r := state.NewReducer(state.ReducerConfig{})
	parent := protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:parent"}
	run := protocol.SourceID{Kind: protocol.SourceASL, Key: "run"}
	r.Apply(protocol.ReducerEvent{Source: parent, Action: "stop", Seq: 1, At: viewNow.Add(-42 * time.Second).Unix(), TurnID: "first"})
	r.Apply(protocol.ReducerEvent{Source: run, Phase: state.ASLPhasePaused, Seq: 1, At: viewNow.Unix()})
	query := commands.StateQuery
	commands.StateQuery = func() (protocol.Response, error) {
		resp, err := query()
		effective := r.Reduce(s.now.Unix())
		resp.States = map[int]protocol.WindowStateSnapshot{acceptanceWindowID(0): {
			IconState: effective.Icon, Looping: effective.Looping,
			AgentTurn: r.AgentTurn(s.now.Unix()), ASLPhases: r.ASLPhases(),
		}}
		return resp, err
	}
	s.deliver(jobTopology)
	if frame := s.frame(); !strings.Contains(frame, "0:42 · Done") || strings.Contains(frame, "ASL paused") {
		t.Fatalf("paused process still appears to supervise the completed agent:\n%s", frame)
	}
	r.Apply(protocol.ReducerEvent{Source: run, Phase: state.ASLPhaseTurn, Seq: 2, At: viewNow.Unix() + 1})
	s.refresh()
	if frame := s.frame(); !strings.Contains(frame, "0:42 | ASL running · Done") {
		t.Fatalf("run heartbeat replaced agent completion:\n%s", frame)
	}
	s.resize(60, 15)
	if frame := s.frame(); !strings.Contains(frame, "0:42 | ASL running · Done") {
		t.Fatalf("resizing lost independent status:\n%s", frame)
	}
	r.Apply(protocol.ReducerEvent{Source: parent, Action: "active", Seq: 2, At: viewNow.Unix() + 2, TurnID: "second"})
	s.refresh()
	if frame := s.frame(); strings.Contains(frame, "Done") || !strings.Contains(frame, "Working") {
		t.Fatalf("new agent turn still appears done:\n%s", frame)
	}
}

func TestAcceptanceTheSurfaceLoadsAWholeMultiProfileUniverse(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	s := newStage(t, world, 120, 30, 0)

	if s.pending(jobTopology) != 1 {
		t.Fatalf("startup parked %d topology reads, want exactly 1", s.pending(jobTopology))
	}
	if strings.Contains(s.frame(), "Loading live sessions…") == false {
		t.Fatalf("the cold frame says nothing about loading:\n%s", s.frame())
	}
	s.deliver(jobTopology)

	if len(s.m.sessions) != 11 {
		t.Fatalf("loaded %d sessions, want the world's 11", len(s.m.sessions))
	}
	profiles := map[string]int{}
	projects := map[string]struct{}{}
	for _, sess := range s.m.sessions {
		profiles[sess.Profile]++
		projects[sess.Project] = struct{}{}
	}
	if len(profiles) != 3 || len(projects) != 11 {
		t.Fatalf("loaded %d profiles and %d projects, want 3 and 11: %v", len(profiles), len(projects), profiles)
	}
	if world.listCount() != 0 {
		t.Fatalf("the surface ran %d direct listings beside the daemon's snapshot, want 0", world.listCount())
	}

	// Four tiles on a 120x30 terminal, so four reads and three pages.
	if got := s.targetWindows(); !sameInts(got, windowRange(0, 4)) {
		t.Fatalf("targets = %v, want the first page's four windows %v", got, windowRange(0, 4))
	}
	if s.pending(jobCapture) != 4 {
		t.Fatalf("the first snapshot dispatched %d reads, want the page's 4", s.pending(jobCapture))
	}
	if frame := s.frame(); !strings.Contains(frame, "page 1/3") {
		t.Fatalf("the frame does not report page 1 of 3:\n%s", frame)
	}
}

// TestAcceptanceTheOperatorPagesWhileCapturesAreStillOut is the combined
// scenario: more sessions than fit, reads that have not answered, and a keyboard
// that does not wait for them.
//
// Three properties meet here and each would be invisible on its own. The BOUND
// holds across the page change, so paging away from four slow reads does not
// start four more. The GENERATION rule discards their text when it finally
// arrives, so a tile on page three never paints a terminal from page one. And
// the released slots immediately serve the page the operator is actually looking
// at, which is what keeps the surface from freezing behind reads nobody wants.
func TestAcceptanceTheOperatorPagesWhileCapturesAreStillOut(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	for i := 0; i < 11; i++ {
		world.setText(acceptanceWindowID(i), "output from "+acceptanceProject(i))
	}
	s := newStage(t, world, 120, 30, 0)
	s.deliver(jobTopology)

	if s.pending(jobCapture) != maxCapturesInFlight {
		t.Fatalf("page one dispatched %d reads, want %d", s.pending(jobCapture), maxCapturesInFlight)
	}

	// Page forward twice with all four reads still out.
	s.press("pgdown")
	s.press("pgdown")
	if s.m.selected != 8 {
		t.Fatalf("two page-downs from index 0 landed on %d, want 8", s.m.selected)
	}
	if got := s.targetWindows(); !sameInts(got, windowRange(8, 3)) {
		t.Fatalf("targets after paging = %v, want the last page's %v", got, windowRange(8, 3))
	}
	if s.pending(jobCapture) != maxCapturesInFlight {
		t.Fatalf("paging past four outstanding reads dispatched more; %d are out, want %d",
			s.pending(jobCapture), maxCapturesInFlight)
	}
	if world.readCount() != 0 {
		t.Fatalf("the world was read %d times before any reply was delivered, want 0", world.readCount())
	}

	// The four page-one replies land. Each releases its slot, none is painted,
	// and the freed slots go to the page on screen.
	s.deliver(jobCapture)
	if got := s.previewWindows(); len(got) != 0 {
		t.Fatalf("page-one text was painted after paging away: %v", got)
	}
	if s.pending(jobCapture) != 3 {
		t.Fatalf("the released slots dispatched %d reads, want the visible page's 3", s.pending(jobCapture))
	}

	s.deliver(jobCapture)
	if got := s.previewWindows(); !sameInts(got, windowRange(8, 3)) {
		t.Fatalf("previews = %v, want exactly the visible page's %v", got, windowRange(8, 3))
	}
	if got := s.m.previews[acceptanceWindowID(10)].Output; len(got) != 1 || !strings.Contains(got[0], "svc11") {
		t.Fatalf("the last tile shows %v, want its own terminal's output", got)
	}
	if world.readCount() != 7 {
		t.Fatalf("the world was read %d times, want the 4 stale plus the 3 visible", world.readCount())
	}
	if frame := s.frame(); !strings.Contains(frame, "page 3/3") {
		t.Fatalf("the frame does not report page 3 of 3:\n%s", frame)
	}
}

// TestAcceptanceAResizeDuringARefreshKeepsTheSelectionAndReaimsTheReads is the
// other interleaving that only exists in combination: the terminal changes shape
// while a window tree is in flight.
//
// The selection has to survive as an IDENTITY — the operator was on a session,
// not on a grid position — while everything derived from the geometry has to be
// recomputed: the page, the tiles on it, and the size every read is clipped to.
func TestAcceptanceAResizeDuringARefreshKeepsTheSelectionAndReaimsTheReads(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	s := newStage(t, world, 160, 45, 0) // 3x3, nine tiles
	s.deliver(jobTopology)
	s.deliver(jobCapture)

	s.press("end")
	selected := s.m.selectedTabID
	if selected != acceptanceTabID(10) {
		t.Fatalf("End selected tab %d, want the last session's %d", selected, acceptanceTabID(10))
	}
	if s.m.selected != 10 {
		t.Fatalf("End selected index %d, want 10", s.m.selected)
	}

	// A refresh goes out, and the terminal is dragged smaller before it lands.
	s.tick()
	if s.pending(jobTopology) != 1 {
		t.Fatalf("the tick parked %d reads, want 1", s.pending(jobTopology))
	}
	s.resize(120, 30) // 2x2, four tiles
	if got := s.targetWindows(); !sameInts(got, windowRange(8, 3)) {
		t.Fatalf("targets after the resize = %v, want the new last page %v", got, windowRange(8, 3))
	}

	// Now the in-flight tree lands on the resized surface.
	s.deliver(jobTopology)
	if s.m.selectedTabID != selected {
		t.Fatalf("the refresh moved the selection to tab %d, want the operator's %d", s.m.selectedTabID, selected)
	}
	if got := s.targetWindows(); !sameInts(got, windowRange(8, 3)) {
		t.Fatalf("targets after the refresh = %v, want the resized page %v", got, windowRange(8, 3))
	}

	// Every read now out is clipped to a tile of the NEW geometry, not the old.
	narrow, _ := outputExtent(mustTile(t, Measure(120, 30), 0))
	wide, _ := outputExtent(mustTile(t, Measure(160, 45), 0))
	if narrow == wide {
		t.Fatalf("the fixture's two sizes produce the same tile width %d; the assertion below proves nothing", narrow)
	}
	for windowID, target := range s.m.captures.targets {
		if target.width != narrow {
			t.Fatalf("window %d is targeted at width %d, want the resized tile's %d", windowID, target.width, narrow)
		}
	}
	s.deliver(jobCapture)
	for windowID, preview := range s.m.previews {
		for _, line := range preview.Output {
			if len([]rune(line)) > narrow {
				t.Fatalf("window %d kept a %d-cell line on a %d-cell tile", windowID, len([]rune(line)), narrow)
			}
		}
	}
	if frame := s.frame(); !strings.Contains(frame, "page 3/3") {
		t.Fatalf("the resized frame does not report page 3 of 3:\n%s", frame)
	}
}

func mustTile(t *testing.T, g Geometry, pos int) Tile {
	t.Helper()
	tile, ok := g.Tile(pos)
	if !ok {
		t.Fatalf("geometry %dx%d has no tile %d", g.Width, g.Height, pos)
	}
	return tile
}

// TestAcceptanceASessionDisappearsUnderACaptureAndUnderAFocus is the surface
// meeting the one event it cannot prevent: the pane it is describing goes away.
//
// It happens twice here, at the two moments that matter. Under a CAPTURE the
// read fails and the failure is confined to its own tile — the rest of the grid
// keeps updating — and the next tree drops the row and its retained text
// together. Under a FOCUS the operator stays on the overview with the reason on
// the feedback row, which is the only state from which retrying makes sense.
func TestAcceptanceASessionDisappearsUnderACaptureAndUnderAFocus(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(4))
	for i := 0; i < 4; i++ {
		world.setText(acceptanceWindowID(i), "output from "+acceptanceProject(i))
	}
	s := newStage(t, world, 120, 30, 0) // four tiles, one page
	s.deliver(jobTopology)
	if s.pending(jobCapture) != 4 {
		t.Fatalf("dispatched %d reads, want 4", s.pending(jobCapture))
	}

	// The second session's tab closes with every read still out.
	world.closeTab(acceptanceTabID(1))
	s.deliver(jobCapture)

	failed := s.m.previews[acceptanceWindowID(1)]
	if failed.Content != dashboardInspectorFailed() {
		t.Fatalf("the vanished window's tile shows %v, want the failed read", failed.Content)
	}
	if failed.Error == "" {
		t.Fatalf("the failed tile carries no reason")
	}
	for _, index := range []int{0, 2, 3} {
		if got := s.m.previews[acceptanceWindowID(index)].Output; len(got) == 0 {
			t.Fatalf("window %d lost its output because a sibling failed", acceptanceWindowID(index))
		}
	}

	// The next tree confirms the disappearance: the row goes, and so does the
	// text retained for it.
	s.refresh()
	if len(s.m.sessions) != 3 {
		t.Fatalf("the refresh left %d sessions, want 3", len(s.m.sessions))
	}
	if _, held := s.m.previews[acceptanceWindowID(1)]; held {
		t.Fatalf("the closed window's capture is still retained")
	}
	if got := s.targetWindows(); !sameInts(got, []int{acceptanceWindowID(0), acceptanceWindowID(2), acceptanceWindowID(3)}) {
		t.Fatalf("targets = %v, want the three surviving windows", got)
	}

	// Now a focus of a tab that closes during the round trip.
	s.press("home")
	target := s.m.selectedTabID
	s.press("enter")
	if s.pending(jobFocus) != 1 {
		t.Fatalf("Enter parked %d focus actions, want 1", s.pending(jobFocus))
	}
	world.closeTab(target)
	s.deliver(jobFocus)

	if s.quit {
		t.Fatal("a failed focus quit the overview; the operator is owed the surface and the reason")
	}
	if !strings.Contains(s.frame(), "Focus failed:") {
		t.Fatalf("the frame does not report the failed focus:\n%s", s.frame())
	}
	if s.m.selectedTabID != target {
		t.Fatalf("the failed focus moved the selection to %d, want the attempted %d", s.m.selectedTabID, target)
	}

	// And retrying is possible: the pending focus was cleared, so a second Enter
	// acts rather than being swallowed.
	s.press("enter")
	if s.pending(jobFocus) != 1 {
		t.Fatalf("a second Enter after a failure parked %d actions, want 1", s.pending(jobFocus))
	}
	s.deliver(jobFocus)
	if world.focusCount() != 2 {
		t.Fatalf("Kitty saw %d focus attempts, want 2", world.focusCount())
	}

	// A refusal from Kitty itself is the other way a focus fails, and it has to
	// have the same shape: the operator is owed the surface, the reason, and the
	// tile they aimed at. Only the sentence differs, which is what makes this one
	// path rather than two.
	s.refresh() // the closed tab leaves the list; aim at one that is still there
	s.press("home")
	live := s.m.selectedTabID
	world.setFocusErr(errors.New("kitty refused the focus"))
	s.press("enter")
	s.deliver(jobFocus)

	if s.quit {
		t.Fatal("a refused focus quit the overview; the operator is owed the surface and the reason")
	}
	if !strings.Contains(s.frame(), "Focus failed:") {
		t.Fatalf("the frame does not report the refusal:\n%s", s.frame())
	}
	if s.m.selectedTabID != live {
		t.Fatalf("the refusal moved the selection to %d, want the attempted %d", s.m.selectedTabID, live)
	}

	// And the refusal is not a state the surface gets stuck in: once Kitty
	// answers again, the same keypress works and the program leaves.
	world.setFocusErr(nil)
	s.press("enter")
	s.deliver(jobFocus)
	if !s.quit {
		t.Fatal("the retry after Kitty recovered did not leave the surface")
	}
}

// dashboardInspectorFailed names the failed content state without importing the
// dashboard package into this file's assertions, which would be a second
// vocabulary for a value the package already produces.
func dashboardInspectorFailed() any { return failedPreview("x").Content }

// TestAcceptanceASuccessfulFocusLeavesTheSurface is the control for the failure
// above: the one mutation this surface can perform, performed, and the program
// exiting because it worked.
func TestAcceptanceASuccessfulFocusLeavesTheSurface(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(5))
	s := newStage(t, world, 120, 30, 0)
	s.deliver(jobTopology)

	s.press("right")
	target := s.m.selectedTabID
	s.press("enter")
	s.deliver(jobFocus)

	if !s.quit {
		t.Fatal("a successful focus did not leave the overview")
	}
	if world.focusCount() != 1 {
		t.Fatalf("Kitty saw %d focus attempts, want 1", world.focusCount())
	}
	if world.focuses[0] != kitty.MatchTabByID(target) {
		t.Fatalf("focused %q, want the selected tab %d", world.focuses[0], target)
	}
	if !s.m.captures.stopped {
		t.Fatal("leaving on a successful focus left the capture scheduler running")
	}
}

// TestAcceptanceRepeatedTopologyFailuresRetainTheListAndThenRecover is an
// outage against a WARM surface, start to finish.
//
// Retention is the property: a surface that emptied itself because a subprocess
// timed out would be worse than one that visibly ages. So the rows stay, the
// previews keep updating from the panes that are still answering, and the
// failures do not accumulate into a retry storm — one read per tick, no more.
//
// What the operator is told is the AGE rather than the error, and that ranking
// is the point rather than an omission. A warm surface's error is about a read
// they did not ask for; what is actionable is that the tiles they are looking at
// are frozen, which the age says whatever the read failed with. So the frame
// stays quiet until the tree crosses the daemon's own staleness threshold and
// then says how old it has become.
func TestAcceptanceRepeatedTopologyFailuresRetainTheListAndThenRecover(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(6))
	for i := 0; i < 6; i++ {
		world.setText(acceptanceWindowID(i), "output from "+acceptanceProject(i))
	}
	s := newStage(t, world, 120, 30, 0)
	s.deliver(jobTopology)
	s.deliver(jobCapture)
	before := append([]int(nil), tabs(s.m)...)

	outage := errors.New("kitten @ ls: signal: killed")
	world.setListErr(outage)

	for round := 1; round <= 5; round++ {
		s.tick()
		if got := s.pending(jobTopology); got != 1 {
			t.Fatalf("outage round %d has %d reads parked, want the single coalesced one", round, got)
		}
		s.deliver(jobTopology)
		s.advance(time.Second)
		if !sameInts(tabs(s.m), before) {
			t.Fatalf("outage round %d replaced the list with %v, want the retained %v", round, tabs(s.m), before)
		}
		if s.m.err == nil {
			t.Fatalf("outage round %d cleared the error", round)
		}
	}
	if got := world.listCount(); got != 5 {
		t.Fatalf("five failing ticks ran %d listings, want exactly 5", got)
	}

	// Once the frozen tree is older than the daemon's own staleness threshold,
	// the row says how old it has become.
	s.advance(protocol.KittySnapshotStaleAfter)
	if frame := s.frame(); !strings.Contains(frame, "Kitty refresh delayed") {
		t.Fatalf("a tree frozen past the stale threshold is not reported as delayed:\n%s", frame)
	}

	// The surface stays live through the outage: the capture cadence is
	// independent of the topology cadence, so the panes that still answer keep
	// painting.
	world.setText(acceptanceWindowID(0), "still working")
	s.captureTick()
	s.deliver(jobCapture)
	if got := s.m.previews[acceptanceWindowID(0)].Output; len(got) != 1 || got[0] != "still working" {
		t.Fatalf("the first tile shows %v during the outage, want its newest output", got)
	}

	// Recovery is one tick. The newer tree resets the age, so the warning goes
	// with the outage that raised it.
	world.setListErr(nil)
	s.refresh()
	if s.m.err != nil {
		t.Fatalf("the recovery read left the error %v in place", s.m.err)
	}
	if !sameInts(tabs(s.m), before) {
		t.Fatalf("recovery listed %v, want %v", tabs(s.m), before)
	}
	if frame := s.frame(); strings.Contains(frame, "delayed") {
		t.Fatalf("the recovered frame still reports the outage:\n%s", frame)
	}
}

// TestAcceptanceAColdOutageNamesTheErrorAndStillOwesTheSourcePane is the same
// outage met before anything has ever loaded, which is the other ranking.
//
// With no tree there is no list to interpret and no age to report, so the error
// IS the whole truth about the surface and the row says it. The second property
// is what happens on the way out: the pane the overview was raised over is not
// spent by a read that resolved nothing, so whichever read succeeds first still
// marks the session they launched from while starting at the top-left card.
func TestAcceptanceAColdOutageNamesTheErrorAndStillOwesTheSourcePane(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	outage := errors.New("kitten @ ls: signal: killed")
	world.setListErr(outage)
	s := newStage(t, world, 120, 30, acceptanceWindowID(9))

	for round := 1; round <= 3; round++ {
		s.deliver(jobTopology)
		if frame := s.frame(); !strings.Contains(frame, "Kitty error: "+outage.Error()) {
			t.Fatalf("cold outage round %d does not name the failure:\n%s", round, frame)
		}
		if s.m.loaded {
			t.Fatalf("cold outage round %d reported a loaded surface", round)
		}
		if s.pending(jobCapture) != 0 {
			t.Fatalf("cold outage round %d started %d captures with no tree", round, s.pending(jobCapture))
		}
		s.tick()
	}

	world.setListErr(nil)
	s.deliver(jobTopology)
	if s.m.selected != 0 || s.m.sourceTabID != acceptanceTabID(9) {
		t.Fatalf("first successful read selected index %d and source tab %d, want top-left and source tab %d",
			s.m.selected, s.m.sourceTabID, acceptanceTabID(9))
	}
	if frame := s.frame(); strings.Contains(frame, "Kitty error") {
		t.Fatalf("the recovered frame still names the outage:\n%s", frame)
	}
	if got := s.targetWindows(); !sameInts(got, windowRange(0, 4)) {
		t.Fatalf("targets after recovery = %v, want the first page %v", got, windowRange(0, 4))
	}
}

// TestAcceptanceDismissalStopsEverythingAndReopeningStartsClean is the end of a
// session and the beginning of the next one.
//
// Dismissal has to stop the SCHEDULING, not merely the program: the surface is
// dismissed with reads outstanding and a queue behind them, and every one of
// those would otherwise complete against something that is quitting. And the
// overview that opens afterwards is a new program over the same Kitty, so it
// starts at the first card with a fresh source marker and nothing carried across.
func TestAcceptanceDismissalStopsEverythingAndReopeningStartsClean(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	first := newStage(t, world, 120, 30, 0)
	first.deliver(jobTopology)
	if first.pending(jobCapture) != 4 {
		t.Fatalf("dispatched %d reads, want 4", first.pending(jobCapture))
	}

	first.press("q")
	if !first.quit {
		t.Fatal("q did not quit the overview")
	}
	if !first.m.captures.stopped {
		t.Fatal("dismissal left the capture scheduler running")
	}
	if len(first.m.captures.queue) != 0 {
		t.Fatalf("dismissal left %d reads queued", len(first.m.captures.queue))
	}

	// The reads that were already out cannot be cancelled — Kitty offers no such
	// thing — so they complete, release their slots, and are ignored.
	readsBefore := world.readCount()
	first.deliver(jobCapture)
	if world.readCount() != readsBefore+4 {
		t.Fatalf("the outstanding reads did not complete: %d then %d", readsBefore, world.readCount())
	}
	if first.m.captures.inFlight() != 0 {
		t.Fatalf("%d slots are still held after every reply landed", first.m.captures.inFlight())
	}
	if len(first.m.previews) != 0 {
		t.Fatalf("a dismissed surface accepted %d captures", len(first.m.previews))
	}

	// And nothing schedules again.
	first.captureTick()
	first.captureTick()
	if got := first.pending(jobCapture); got != 0 {
		t.Fatalf("a dismissed surface dispatched %d further reads", got)
	}

	// Reopening over a different pane: a new program, a clean start, and the
	// first card under the cursor and the launching session marked separately.
	second := newStage(t, world, 120, 30, acceptanceWindowID(6))
	if len(second.m.previews) != 0 || second.m.loaded {
		t.Fatal("the reopened overview started with state carried over from the first")
	}
	second.deliver(jobTopology)
	if second.m.selected != 0 || second.m.sourceTabID != acceptanceTabID(6) {
		t.Fatalf("reopened overview selected index %d and source tab %d, want top-left and source tab %d",
			second.m.selected, second.m.sourceTabID, acceptanceTabID(6))
	}
	if got := second.targetWindows(); !sameInts(got, windowRange(0, 4)) {
		t.Fatalf("the reopened overview targets %v, want the first page %v", got, windowRange(0, 4))
	}
}

// TestAcceptanceSustainedTickBurstsStayBoundedAndResponsive is the endurance
// case, written as a loop rather than as a wait.
//
// Two hundred capture ticks arrive against a Kitty that answers roughly one read
// in three, with topology refreshes and keypresses interleaved. Four invariants
// are checked on every single iteration rather than at the end, so a violation
// names the tick that caused it: the global bound, the agreement between the
// scheduler's in-flight count and the world's outstanding reads, the retention
// bound, and the queue's bound. What the burst proves at the end is that the
// surface is still a surface — the keyboard still moves the cursor, the frame
// still renders, and the total cost is a small multiple of the tiles on screen
// rather than a multiple of the ticks.
func TestAcceptanceSustainedTickBurstsStayBoundedAndResponsive(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	for i := 0; i < 11; i++ {
		world.setText(acceptanceWindowID(i), "output from "+acceptanceProject(i))
	}
	s := newStage(t, world, 160, 45, 0) // 3x3, nine tiles, two pages
	s.deliver(jobTopology)

	const bursts = 200
	for i := 0; i < bursts; i++ {
		s.captureTick()
		// Roughly one read in three answers, so the queue genuinely backs up.
		if i%3 == 0 {
			s.deliverOne(jobCapture)
		}
		// A topology refresh every twentieth tick, and a page change every
		// fiftieth, so the target set moves underneath the outstanding reads.
		if i%20 == 0 {
			s.tick()
			s.deliver(jobTopology)
		}
		if i%50 == 0 {
			s.press("pgdown")
		}
		s.advance(50 * time.Millisecond)

		if got := s.m.captures.inFlight(); got > maxCapturesInFlight {
			t.Fatalf("tick %d: %d reads in flight, want at most %d", i, got, maxCapturesInFlight)
		}
		if got, want := s.pending(jobCapture), s.m.captures.inFlight(); got != want {
			t.Fatalf("tick %d: %d reads are outstanding in the world but the scheduler holds %d slots", i, got, want)
		}
		if got, want := len(s.m.previews), len(s.m.captures.targets); got > want {
			t.Fatalf("tick %d: %d captures retained for %d visible tiles", i, got, want)
		}
		if got := len(s.m.captures.queue); got > len(s.m.captures.targets) {
			t.Fatalf("tick %d: %d windows queued for %d visible tiles", i, got, len(s.m.captures.targets))
		}
	}

	// The cost is bounded by the replies the test chose to deliver, not by the
	// ticks or by the tiles: a read holds its slot until it answers, so the most
	// reads the surface can ever have started is one per completed read plus the
	// handful still outstanding. Two hundred ticks over nine tiles could have
	// meant eighteen hundred.
	completions := (bursts + 2) / 3
	if got, want := world.readCount(), completions+maxCapturesInFlight; got > want {
		t.Fatalf("%d ticks produced %d reads of Kitty; at most %d replies landed, so the coalescing is not holding", bursts, got, want)
	}
	if got := world.readCount(); got == 0 {
		t.Fatalf("%d ticks produced no reads at all; the burst proved nothing", bursts)
	}

	// And the reads follow the eye. The burst ends parked on the last page, and
	// the scheduler reads only what is on screen, so the two windows there absorb
	// the cost while every window the operator paged away from stops being read —
	// each keeps no more than the single page-worth of dispatches it got before
	// leaving the screen. Without this the bound above would still hold while the
	// surface refreshed nine tiles nobody is looking at.
	offPage := 0
	for i := 0; i < 9; i++ {
		offPage += world.readsOf(acceptanceWindowID(i))
	}
	if offPage > 9 {
		t.Fatalf("windows off the final page were read %d times, want at most the 9 tiles' worth dispatched before the page changed", offPage)
	}
	for _, index := range []int{9, 10} {
		if got := world.readsOf(acceptanceWindowID(index)); got <= offPage {
			t.Fatalf("the visible window %d was read %d times, no more than the %d reads spent off screen; the reads are not following the page", acceptanceWindowID(index), got, offPage)
		}
	}

	// Still a surface. The keyboard moves the cursor, the movement re-aims the
	// reads, and the frame renders.
	s.press("home")
	if s.m.selected != 0 {
		t.Fatalf("Home after the burst selected %d, want 0", s.m.selected)
	}
	if got := s.targetWindows(); !sameInts(got, windowRange(0, 9)) {
		t.Fatalf("targets after Home = %v, want the first page %v", got, windowRange(0, 9))
	}
	s.press("end")
	if s.m.selected != 10 {
		t.Fatalf("End after the burst selected %d, want 10", s.m.selected)
	}
	if frame := s.frame(); !strings.Contains(frame, "page 2/2") {
		t.Fatalf("the frame after the burst does not report page 2 of 2:\n%s", frame)
	}

	// And it still leaves: the surface that survived the burst can be dismissed.
	s.press("esc")
	if !s.quit || !s.m.captures.stopped {
		t.Fatalf("after the burst, esc left quit=%v stopped=%v", s.quit, s.m.captures.stopped)
	}
}

// TestAcceptanceAClosedSessionLeavesTheTreeAndIsRestoredByUndo is the acting
// half over the real world: the menu, the confirmation, the actual
// `kitten @ close-tab`, the reconcile onto a survivor, and the relaunch `u`
// performs — with no seam replaced anywhere along the way.
//
// It is the one scenario in this file that is ALLOWED to write to Kitty, and it
// says so twice: the permit set names exactly the methods a close and a launch
// perform, and each is then asserted on what it was CALLED with rather than on
// having happened. A write this scenario did not ask for is still a violation,
// so "the overview writes only what an operator asked it to" survives the
// arrival of twelve actions rather than being retired by it.
func TestAcceptanceAClosedSessionLeavesTheTreeAndIsRestoredByUndo(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(4))
	s := newStage(t, world, 120, 30, 0)
	s.deliver(jobTopology)

	// Aim at the second session, so the survivor the cursor lands on afterwards
	// is a genuine choice rather than the only row left.
	s.press("right")
	closing := s.m.selectedTabID
	if closing != acceptanceTabID(1) {
		t.Fatalf("the cursor is on tab %d, want the second session's %d", closing, acceptanceTabID(1))
	}

	s.press("a")
	if s.m.acts.surface != surfaceMenu {
		t.Fatalf("`a` left surface %v, want the menu", s.m.acts.surface)
	}
	s.press("X")
	if s.m.acts.surface != surfaceConfirm {
		t.Fatalf("`X` left surface %v, want the confirmation", s.m.acts.surface)
	}
	// The prompt itself is what is asserted, not a word that happens to appear
	// somewhere in the frame: the question names the session, the consequence
	// says what saying yes destroys, and the hint row — the prompt box carries
	// none of its own — says how to answer it.
	frame := s.frame()
	for _, want := range []string{
		`Close "svc02"?`,
		"The harness process exits; its transcript is preserved.",
		"y confirm",
		"cancel",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the confirmation does not say %q:\n%s", want, frame)
		}
	}
	if len(world.callsOf("CloseTab")) != 0 {
		t.Fatal("the confirmation had already closed the tab before it was answered")
	}

	world.permitClose()
	s.press("y")
	if s.pending(jobAction) != 1 {
		t.Fatalf("the confirmed close parked %d actions, want 1", s.pending(jobAction))
	}
	s.deliver(jobAction)

	if got := world.callsOf("CloseTab"); len(got) != 1 || got[0] != kitty.MatchTabByID(closing) {
		t.Fatalf("Kitty was asked to close %v, want exactly the confirmed tab %d", got, closing)
	}
	if got := world.tabIDs(); len(got) != 3 {
		t.Fatalf("the world holds %v, want the three surviving tabs", got)
	}

	// The close reported itself, so the next tree reconciles the cursor onto the
	// row that took the closed one's place.
	s.deliver(jobTopology)
	if len(s.m.sessions) != 3 {
		t.Fatalf("the refresh left %d sessions, want 3", len(s.m.sessions))
	}
	if s.m.selectedTabID != acceptanceTabID(2) {
		t.Fatalf("the cursor landed on tab %d, want the nearest survivor %d", s.m.selectedTabID, acceptanceTabID(2))
	}
	if !s.m.recoverable() {
		t.Fatal("a confirmed close left nothing to undo")
	}
	// The offer is on the FEEDBACK row rather than the footer: `u` is real for
	// five seconds and the footer would otherwise advertise it forever.
	if frame := s.frame(); !strings.Contains(frame, "u undo") {
		t.Fatalf("the frame does not offer the undo:\n%s", frame)
	}

	// And `u` relaunches it: a NEW tab, running the closed session's project
	// under the profile it had.
	world.permitLaunch()
	s.press("u")
	if s.pending(jobRelaunch) != 1 {
		t.Fatalf("`u` parked %d relaunches, want 1", s.pending(jobRelaunch))
	}
	if len(s.relaunches) != 1 || s.relaunches[0].Session.Title != acceptanceProject(1) {
		t.Fatalf("the relaunch names %v, want the closed project %q", s.relaunches, acceptanceProject(1))
	}
	s.deliver(jobRelaunch)

	if got := world.callsOf("LaunchTab"); len(got) != 1 || got[0] != acceptanceProject(1) {
		t.Fatalf("Kitty launched %v, want one tab titled %q", got, acceptanceProject(1))
	}
	if got := len(world.tabIDs()); got != 4 {
		t.Fatalf("the world holds %d tabs after the restore, want 4", got)
	}

	// The refresh the restore asked for shows it back, and the offer is spent.
	s.deliver(jobTopology)
	if len(s.m.sessions) != 4 {
		t.Fatalf("the restored surface lists %d sessions, want 4", len(s.m.sessions))
	}
	if s.m.recoverable() {
		t.Fatal("the claimed close is still offered; a second `u` would launch a second copy")
	}
	if frame := s.frame(); !strings.Contains(frame, "restored") {
		t.Fatalf("the frame does not report the restoration:\n%s", frame)
	}
}

// TestAcceptanceBrowsingTheMenusWritesNothing is the control for the scenario
// above, and the reason the permit set starts empty.
//
// Every overlay is opened, browsed, and backed out of — including both
// destructive confirmations and the profile picker — against a world that
// permits no write at all. What it asserts is not that nothing happened but that
// nothing was WRITTEN: the surface still lists, still reads text, and still
// draws, while the violations list stays empty through the stage's own cleanup.
func TestAcceptanceBrowsingTheMenusWritesNothing(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(6))
	for i := 0; i < 6; i++ {
		world.setText(acceptanceWindowID(i), "output from "+acceptanceProject(i))
	}
	s := newStage(t, world, 120, 30, 0)
	s.deliver(jobTopology)
	s.deliver(jobCapture)
	before := append([]int(nil), world.tabIDs()...)

	s.press("a")
	for _, name := range []string{"j", "j", "G", "g", "down", "up"} {
		s.press(name)
	}

	// Into the confirmation and back out, twice: a decline and an escape.
	s.press("X")
	s.press("n")
	s.press("r")
	s.press("esc")

	// Into the profile picker and back out.
	s.press("c")
	if s.m.acts.surface != surfaceProfile {
		t.Fatalf("`c` in the menu left surface %v, want the profile picker", s.m.acts.surface)
	}
	s.press("j")
	s.press("esc")
	s.press("esc")

	if s.m.acts.surface != surfaceGrid {
		t.Fatalf("backing out of every overlay left surface %v, want the grid", s.m.acts.surface)
	}
	if s.pending(jobAction) != 0 || s.pending(jobRelaunch) != 0 {
		t.Fatalf("browsing parked %d actions and %d relaunches", s.pending(jobAction), s.pending(jobRelaunch))
	}
	if len(s.dispatches) != 0 {
		t.Fatalf("browsing dispatched %v", s.dispatches)
	}
	if got := world.tabIDs(); !sameInts(got, before) {
		t.Fatalf("browsing changed the world's tabs to %v, want %v", got, before)
	}

	// Still a live surface afterwards: the cadence runs and the tiles repaint.
	world.setText(acceptanceWindowID(0), "still working")
	s.captureTick()
	s.deliver(jobCapture)
	if got := s.m.previews[acceptanceWindowID(0)].Output; len(got) != 1 || got[0] != "still working" {
		t.Fatalf("the first tile shows %v after the menus closed, want its newest output", got)
	}
	s.refresh()
	if len(s.m.sessions) != 6 {
		t.Fatalf("the refresh after browsing lists %d sessions, want 6", len(s.m.sessions))
	}
}

// TestAcceptanceEverySessionIsReachableAndReadableByKeyboardAlone walks the whole
// universe the way an operator would — page by page, with the reads for each page
// answered before moving on — and asserts the thing the surface exists for: every
// live session can be brought on screen and shown its own terminal's output,
// without a single write into Kitty.
func TestAcceptanceEverySessionIsReachableAndReadableByKeyboardAlone(t *testing.T) {
	world := newKittyWorld(acceptanceTabs(11))
	for i := 0; i < 11; i++ {
		world.setText(acceptanceWindowID(i), "output from "+acceptanceProject(i))
	}
	s := newStage(t, world, 120, 30, 0)
	s.deliver(jobTopology)

	seen := map[int]string{}
	for page := 0; page < 3; page++ {
		s.deliver(jobCapture)
		for windowID, preview := range s.m.previews {
			if len(preview.Output) > 0 {
				seen[windowID] = preview.Output[len(preview.Output)-1]
			}
		}
		if page < 2 {
			s.press("pgdown")
		}
	}

	if len(seen) != 11 {
		t.Fatalf("paged through the whole list and saw output for %d of 11 sessions", len(seen))
	}
	for i := 0; i < 11; i++ {
		line, ok := seen[acceptanceWindowID(i)]
		if !ok {
			t.Fatalf("session %s was never shown its own output", acceptanceProject(i))
		}
		if !strings.Contains(line, acceptanceProject(i)) {
			t.Fatalf("session %s was shown %q, which belongs to another pane", acceptanceProject(i), line)
		}
	}
	if world.listCount() != 0 {
		t.Fatalf("browsing the whole universe ran %d direct Kitty listings, want 0", world.listCount())
	}
}
