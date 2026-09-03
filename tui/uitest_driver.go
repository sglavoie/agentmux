// This file is the scenario driver: the in-process program that stands in for
// a real Bubble Tea run. It builds a model, feeds it actions, and hands back
// the screen each action produced — for a hand-written config, for a named
// fixture, and for the hermetic environment a scenario runs inside.

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/commands"
)

// This file drives the real Bubble Tea dashboard Model in-process, without a
// terminal or the runtime event loop. It feeds deterministic input (window
// sizes, keys, ticks) straight through Model.Update and drains the resulting
// commands so refresh-style flows settle before an observation is captured.
//
// Draining is the delicate part. Bubble Tea v2 aggregates commands into
// slice-shaped batch/sequence messages, so a single Update can fan out into
// many follow-up commands. Scheduled tea.Tick/tea.Every effects are identified
// before launch and skipped. Every other command is tracked to completion (or
// cancellation) so no filesystem goroutine can outlive cleanup.
//
// The driver is built in uitest_driver_hermetic.go (which wires the offline
// command seams) and populated from a built-in fixture in
// uitest_driver_fixture.go.

const (
	// uitestDrainMaxSteps bounds how many commands a single action may drain.
	// Refresh flows re-enqueue fetches, so the cap is a safety net against a
	// runaway chain rather than an expected limit.
	uitestDrainMaxSteps = 256
)

// uitestDriver holds a live Model plus the terminal size it is rendered at, and
// applies scenario actions to it. It never talks to a live Kitty instance; all
// side effects flow through the injected kitty.KittyClient (a no-op recorder in
// scenario runs), so it stays independent of CLI flags and fixture setup.
type uitestDriver struct {
	model    Model
	size     UITestSize
	ctx      context.Context
	cancel   context.CancelFunc
	tempRoot string

	commandMu  sync.Mutex
	commands   sync.WaitGroup
	closed     bool
	closeOnce  sync.Once
	closeErr   error
	errMu      sync.Mutex
	err        error
	liveAccess uitestLiveAccessGuard

	workbenchMu    sync.Mutex
	workbenchOpens []uitestWorkbenchOpen

	aslActionMu sync.Mutex
	aslActions  []uitestASLAction
}

// uitestASLAction records one submitted agent-session-loop mutation. It carries
// the target and the operation ID but never the operator's answer, so a test
// that prints the recording cannot leak reply text.
type uitestASLAction struct {
	RowID       string
	Method      string
	OperationID string
	QuestionID  string
	ActionToken string
}

// ASLActions returns the mutations the model submitted, in order.
func (d *uitestDriver) ASLActions() []uitestASLAction {
	d.aslActionMu.Lock()
	defer d.aslActionMu.Unlock()
	return append([]uitestASLAction(nil), d.aslActions...)
}

func (d *uitestDriver) recordASLAction(action uitestASLAction) {
	d.aslActionMu.Lock()
	d.aslActions = append(d.aslActions, action)
	d.aslActionMu.Unlock()
}

// uitestWorkbenchOpen records one attempted Workbench deep link. It carries the
// row and the requested focus but no URL, because a fixture never mints a
// tokenized link: the recording surface cannot leak a bearer token even if a
// test prints it.
type uitestWorkbenchOpen struct {
	RowID string
	Focus string
}

// WorkbenchOpens returns the Workbench deep links the model attempted, in order.
func (d *uitestDriver) WorkbenchOpens() []uitestWorkbenchOpen {
	d.workbenchMu.Lock()
	defer d.workbenchMu.Unlock()
	return append([]uitestWorkbenchOpen(nil), d.workbenchOpens...)
}

func (d *uitestDriver) recordWorkbenchOpen(open uitestWorkbenchOpen) {
	d.workbenchMu.Lock()
	d.workbenchOpens = append(d.workbenchOpens, open)
	d.workbenchMu.Unlock()
}

type uitestLiveAccessGuard struct {
	mu  sync.Mutex
	err error
}

func (g *uitestLiveAccessGuard) reject(message string) error {
	err := errors.New(message)
	g.mu.Lock()
	if g.err == nil {
		g.err = err
	}
	g.mu.Unlock()
	return err
}

func (g *uitestLiveAccessGuard) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

// Model returns a copy of the current model for assertions that need more than
// an observation exposes.
func (d *uitestDriver) Model() Model { return d.model }

// Context is cancelled by Close and is the lifecycle signal captured by
// fixture commands that can block on I/O.
func (d *uitestDriver) Context() context.Context { return d.ctx }

// Close cancels pending work, joins every started command, and only then
// removes the temporary directory.
func (d *uitestDriver) Close() error {
	if d == nil {
		return nil
	}
	d.closeOnce.Do(func() {
		d.commandMu.Lock()
		d.closed = true
		d.cancel()
		d.commandMu.Unlock()
		d.commands.Wait()
		d.closeErr = os.RemoveAll(d.tempRoot)
	})
	return d.closeErr
}

// Size returns the terminal size the model is currently rendered at.
func (d *uitestDriver) Size() UITestSize { return d.size }

// Observe captures a normalized snapshot of the current model and size.
func (d *uitestDriver) Observe() UITestObservation {
	return observeUITest(d.model, d.size)
}

// Apply executes a single scenario action against the model.
func (d *uitestDriver) Apply(action UITestAction) error {
	switch action.Type {
	case UITestActionKey:
		return d.Key(action.Key)
	case UITestActionText:
		return d.Text(action.Text)
	case UITestActionResize:
		d.Resize(action.Width, action.Height)
		return d.Err()
	case UITestActionTick:
		d.Tick(action.Count)
		return d.Err()
	default:
		return fmt.Errorf("uitest: unknown action type %q", action.Type)
	}
}

// Key parses spec into a key-press message and sends it through the model.
func (d *uitestDriver) Key(spec string) error {
	msg, err := parseUITestKey(spec)
	if err != nil {
		return err
	}
	return d.dispatch(msg)
}

// Text types each rune of s as an individual key press, mirroring how a user
// entering a filter or search query would be seen by the model.
func (d *uitestDriver) Text(s string) error {
	for _, r := range s {
		if err := d.Key(string(r)); err != nil {
			return err
		}
	}
	return nil
}

// Resize changes the terminal viewport and re-renders. A non-positive dimension
// is left to the model to handle (it clamps its own layout math).
func (d *uitestDriver) Resize(width, height int) {
	d.recordErr(d.applySize(UITestSize{Width: width, Height: height}))
}

// Tick advances the model's timer count times by injecting commands.TickMsg values, so
// scenarios can exercise deterministic refresh flows. A non-positive count is
// treated as one tick.
func (d *uitestDriver) Tick(count int) {
	if count <= 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		d.recordErr(d.dispatch(commands.TickMsg{}))
	}
}

// Inject sends an arbitrary message through the model. It is the escape hatch
// for scenarios (and tests) that need to simulate an async result the harness
// does not model as a first-class action.
func (d *uitestDriver) Inject(msg tea.Msg) {
	d.recordErr(d.dispatch(msg))
}

// applySize records the new size and feeds a tea.WindowSizeMsg through the model.
func (d *uitestDriver) applySize(size UITestSize) error {
	d.size = size
	return d.dispatch(tea.WindowSizeMsg{Width: size.Width, Height: size.Height})
}

// dispatch feeds one message through Model.Update and drains any resulting
// command so refresh flows settle before the caller observes the model.
func (d *uitestDriver) dispatch(msg tea.Msg) error {
	updated, cmd := d.model.Update(msg)
	d.model = updated.(Model)
	if err := d.drain(cmd); err != nil {
		return err
	}
	return d.liveAccess.Err()
}

// drain executes cmd and everything it fans out into, feeding leaf messages back
// through the model. Slice-shaped batch/sequence messages are expanded into
// their component commands; time-based commands are skipped before launch. The step cap bounds the
// worst case so a self-rescheduling flow can never loop forever.
func (d *uitestDriver) drain(cmd tea.Cmd) error {
	if cmd == nil {
		return nil
	}
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < uitestDrainMaxSteps; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg, ran, err := d.runCommand(next)
		if err != nil {
			return err
		}
		if !ran || msg == nil {
			continue
		}
		if cmds, isBatch := uitestFlattenCmds(msg); isBatch {
			queue = append(queue, cmds...)
			continue
		}
		updated, follow := d.model.Update(msg)
		d.model = updated.(Model)
		if follow != nil {
			queue = append(queue, follow)
		}
	}
	if len(queue) > 0 {
		return fmt.Errorf("uitest: command drain exceeded %d steps", uitestDrainMaxSteps)
	}
	return nil
}

// runCommand skips known timer effects before they start. All other commands
// are tracked and must finish or observe driver cancellation.
func (d *uitestDriver) runCommand(cmd tea.Cmd) (tea.Msg, bool, error) {
	if cmd == nil {
		return nil, false, nil
	}
	if isUITestTimerCmd(cmd) {
		return nil, false, nil
	}
	d.commandMu.Lock()
	if d.closed {
		d.commandMu.Unlock()
		return nil, false, context.Canceled
	}
	d.commands.Add(1)
	d.commandMu.Unlock()
	ch := make(chan tea.Msg, 1)
	go func() {
		defer d.commands.Done()
		ch <- cmd()
	}()
	select {
	case msg := <-ch:
		return msg, true, nil
	case <-d.ctx.Done():
		return nil, true, d.ctx.Err()
	}
}

func isUITestTimerCmd(cmd tea.Cmd) bool {
	fn := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer())
	if fn == nil {
		return false
	}
	name := fn.Name()
	return strings.Contains(name, "bubbletea/v2.Tick") || strings.Contains(name, "bubbletea/v2.Every")
}

// Err reports the first asynchronous/void-operation driver error.
func (d *uitestDriver) Err() error {
	if d == nil {
		return nil
	}
	d.errMu.Lock()
	defer d.errMu.Unlock()
	return errors.Join(d.err, d.liveAccess.Err())
}

func (d *uitestDriver) recordErr(err error) {
	if err == nil {
		return
	}
	d.errMu.Lock()
	if d.err == nil {
		d.err = err
	}
	d.errMu.Unlock()
}

// uitestFlattenCmds reports whether msg is a slice-shaped batch/sequence message
// and, if so, returns the component commands it carries. It mirrors the
// reflection approach used by the launch-dispatch test helper so the harness
// never depends on Bubble Tea's unexported batch/sequence message types.
func uitestFlattenCmds(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return nil, false
	}
	out := make([]tea.Cmd, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		if c, ok := v.Index(i).Interface().(tea.Cmd); ok && c != nil {
			out = append(out, c)
		}
	}
	return out, true
}

// This file binds a built-in fixture to a driver: the fixture's windows, plans,
// worktrees, and agent-session-loop rows become the answers the offline command
// seams give back. The seams themselves are declared in
// uitest_driver_hermetic.go and the fixtures in uitest_fixtures.go.

// newUITestFixtureDriver builds a driver from a built-in fixture: it constructs
// the model with the fixture's config and a no-op recording Kitty client (which
// reports the fixture's windows), applies size, and loads the fixture's running
// sessions synchronously so the list is populated without the refresh ticker
// (whose tea.Tick commands the harness skips before launch). Each zero dimension falls back
// independently to the fixture's own default for that dimension, so a caller can
// override width alone (or height alone) without zeroing the other. The recording
// client is returned so callers can assert on the Kitty calls the model attempted.
func newUITestFixtureDriver(fx uitestFixture, size UITestSize) (*uitestDriver, *uitestKittyClient) {
	if size.Width == 0 {
		size.Width = fx.Size.Width
	}
	if size.Height == 0 {
		size.Height = fx.Size.Height
	}
	client := newUITestKittyClient(fx.Windows)
	d := newUITestDriver(fx.Config, client, size)
	d.model.backgroundCommands.topologyRefresh = hermeticTopologyRefresh(fx.States, fx.Watchdog)
	fixtureWorktrees := append([]actions.WorktreeView(nil), fx.Worktrees...)
	d.model.backgroundCommands.worktrees = func(_ Model, seq int) tea.Cmd {
		return func() tea.Msg {
			return worktreesLoadedMsg{seq: seq, entries: append([]actions.WorktreeView(nil), fixtureWorktrees...)}
		}
	}
	d.model.backgroundCommands.worktreeAction = func(_ Model, id actions.ID, request actions.Request) tea.Cmd {
		return fixtureWorktreeActionCmd(client, &fixtureWorktrees, id, request)
	}
	fixtureRows := append([]aslbridge.Row(nil), fx.ASLRows...)
	fixtureStatus := fx.ASLStatus
	d.model.backgroundCommands.aslSnapshot = func(_ Model, seq int) tea.Cmd {
		return func() tea.Msg {
			if !fixtureStatus.Available {
				return aslSnapshotLoadedMsg{seq: seq, status: fixtureStatus}
			}
			return aslSnapshotLoadedMsg{
				seq:      seq,
				snapshot: aslbridge.Snapshot{Rows: append([]aslbridge.Row(nil), fixtureRows...)},
				meta:     aslbridge.SnapshotMeta{ReceivedAt: time.Now()},
				status:   fixtureStatus,
			}
		}
	}
	d.model.backgroundCommands.aslWorkbench = func(_ Model, rowID, planDir, focus string) tea.Cmd {
		return func() tea.Msg {
			d.recordWorkbenchOpen(uitestWorkbenchOpen{RowID: rowID, Focus: focus})
			if planDir == "" {
				return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
			}
			return aslWorkbenchDoneMsg{rowID: rowID, focus: focus}
		}
	}
	fixtureBodies := fx.ASLQuestionBodies
	d.model.backgroundCommands.aslQuestion = func(_ Model, seq int, planDir, questionID string) tea.Cmd {
		return func() tea.Msg {
			body, ok := fixtureBodies[questionID]
			if !ok {
				return aslQuestionLoadedMsg{seq: seq, err: errors.New("no-open-question: fixture has no such question")}
			}
			if body.PlanDir == "" {
				body.PlanDir = planDir
			}
			if body.QuestionID == "" {
				body.QuestionID = questionID
			}
			return aslQuestionLoadedMsg{seq: seq, body: body}
		}
	}
	fixtureOutcomes := fx.ASLActionOutcomes
	d.model.backgroundCommands.aslAction = func(_ Model, seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
		return func() tea.Msg {
			d.recordASLAction(uitestASLAction{
				RowID:       rowID,
				Method:      req.Method,
				OperationID: req.OperationID,
				QuestionID:  req.QuestionID,
				ActionToken: req.ActionToken,
			})
			msg := aslActionDoneMsg{
				seq: seq, control: control, rowID: rowID, method: req.Method,
				operationID: req.OperationID, status: aslbridge.OperationAccepted,
			}
			// The fixture holds the token the real bridge would demand for this
			// question, so a submission carrying a spent one is refused here
			// rather than silently accepted.
			if body, ok := fixtureBodies[req.QuestionID]; ok && req.ActionToken != body.ActionToken {
				msg.err = errors.New(aslbridge.CodeStaleActionToken + ": fixture refusal")
				return msg
			}
			if outcome, ok := fixtureOutcomes[req.Method]; ok {
				if outcome.ErrorCode != "" {
					msg.err = errors.New(outcome.ErrorCode + ": fixture refusal")
					return msg
				}
				if outcome.Status != "" {
					msg.status = outcome.Status
				}
			}
			return msg
		}
	}
	d.loadSessionsFrom(client, fx.Config, fx.States, fx.Watchdog)
	return d, client
}

func fixtureWorktreeActionCmd(client *uitestKittyClient, entries *[]actions.WorktreeView, id actions.ID, request actions.Request) tea.Cmd {
	return func() tea.Msg {
		entry := request.Worktree
		if entry == nil {
			return worktreeActionDoneMsg{action: id, err: &actions.Error{Code: "not_found", Action: id, Message: "fixture worktree target is missing"}}
		}
		done := func(result actions.Result, err error) tea.Msg {
			result.Action = id
			return worktreeActionDoneMsg{action: id, result: result, err: err}
		}
		switch id {
		case actions.WorktreeReopen:
			switch entry.ID {
			case "wt-live":
				_ = client.FocusTab(kitty.MatchTabByID(71))
				return done(actions.Result{Changed: true, Message: "focused live worktree session"}, nil)
			case "wt-multi":
				if candidate := request.Parameters["session"]; candidate != "" {
					_ = client.FocusTab("id:" + candidate)
					return done(actions.Result{Changed: true, Message: "focused selected live worktree session"}, nil)
				}
				return done(actions.Result{}, &actions.Error{Code: "ambiguous_live_sessions", Action: id, Message: "multiple live sessions are inside this worktree", Candidates: []actions.Candidate{{ID: "81", DisplayName: "multi-project (work)"}, {ID: "82", DisplayName: "multi-project (personal)"}}})
			case "wt-resume":
				_ = client.LaunchTab(kitty.LaunchOpts{Title: "resume-project"})
				return done(actions.Result{Changed: true, Message: "resumed newest worktree conversation", Data: map[string]any{"continuation": "resumed"}}, nil)
			case "wt-fresh":
				_ = client.LaunchTab(kitty.LaunchOpts{Title: "fresh-project"})
				return done(actions.Result{Changed: true, Message: "launched fresh worktree session", Data: map[string]any{"continuation": "fresh"}}, nil)
			case "wt-profile":
				if request.Parameters["profile"] == "" {
					return done(actions.Result{}, &actions.Error{Code: "profile_required", Action: id, Message: "the recorded worktree profile is no longer configured", MissingParameters: []actions.ParameterSchema{{Name: "profile", Type: "string", Required: true}}})
				}
				_ = client.LaunchTab(kitty.LaunchOpts{Title: "profile-project"})
				return done(actions.Result{Changed: true, Message: "launched fresh worktree session with replacement profile"}, nil)
			}
			return done(actions.Result{Changed: true, Message: "launched fresh worktree session"}, nil)
		case actions.WorktreeRemove:
			mutation := worktree.MutationResult{Entry: *entry, CheckoutRemoved: true, MetadataForgotten: true}
			if request.Parameters["delete_branch"] == "true" {
				mutation.BranchDeleted = true
			}
			if entry.ID == "wt-partial" {
				mutation.MetadataForgotten = false
				for index := range *entries {
					if (*entries)[index].ID == entry.ID {
						(*entries)[index].Exists = false
						(*entries)[index].Classification = worktree.ClassificationMissing
						(*entries)[index].Actionable = false
					}
				}
				result := actions.Result{Changed: true, Message: "removed managed worktree partially", Data: map[string]any{"mutation": mutation}}
				return done(result, &actions.PartialResultError{ActionError: &actions.Error{Code: "metadata_invalid", Action: id, Message: "metadata removal failed"}})
			}
			removeFixtureWorktree(entries, entry.ID)
			return done(actions.Result{Changed: true, Message: "removed managed worktree", Data: map[string]any{"mutation": mutation}}, nil)
		case actions.WorktreeForget:
			mutation := worktree.MutationResult{Entry: *entry, MetadataForgotten: true}
			removeFixtureWorktree(entries, entry.ID)
			return done(actions.Result{Changed: true, Message: "forgot worktree metadata", Data: map[string]any{"mutation": mutation}}, nil)
		default:
			return done(actions.Result{Changed: true, Message: "fixture worktree action completed"}, nil)
		}
	}
}

func removeFixtureWorktree(entries *[]actions.WorktreeView, id string) {
	kept := (*entries)[:0]
	for _, entry := range *entries {
		if entry.ID != id {
			kept = append(kept, entry)
		}
	}
	*entries = kept
}

// This file constructs the driver. Every seam the production model uses to
// reach the filesystem, the daemon, or agent-session-loop is replaced here with
// an offline stand-in, so a driver built without a fixture reproduces an
// unreachable daemon exactly and can never touch the operator's own state. The
// driver itself lives in uitest_driver.go.

// uitestFrozenNow is the instant every harness run renders against. A
// seconds-resolution timer measured from the wall clock cannot be asserted
// exactly, so the driver pins the model's render clock here and fixtures
// anchor their daemon-owned timestamps at fixed offsets from it.
var uitestFrozenNow = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// newUITestDriver builds a driver around a freshly constructed Model. The model
// is created from prepared config and an injected client (both supplied by the
// caller / Session 3), then sized with an initial tea.WindowSizeMsg.
func newUITestDriver(cfg *config.Config, client kitty.KittyClient, size UITestSize) *uitestDriver {
	return newUITestDriverModel(NewModel(cfg, client), size)
}

// newUITestDriverModel wraps an existing Model — useful when a test has already
// loaded sessions or otherwise prepared state — and applies the initial size.
func newUITestDriverModel(m Model, size UITestSize) *uitestDriver {
	d, err := newUITestDriverUnder(m, size, "")
	if err != nil {
		panic(err)
	}
	return d
}

func newUITestDriverUnder(m Model, size UITestSize, tempParent string) (*uitestDriver, error) {
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, tempRoot, err := newHermeticUITestSnapshot(tempParent)
	if err != nil {
		cancel()
		return nil, err
	}
	m.transcriptIndex = snapshot.transcriptIndex
	m.transcriptReaders = snapshot.transcriptReaders
	m.sessionResults = append([]transcript.Result(nil), snapshot.transcriptResults...)
	m.plansIndex = snapshot.planIndex
	m.backgroundCommands = hermeticModelBackgroundCommands()
	m.now = func() time.Time { return uitestFrozenNow }
	// Plan fixtures exercise navigation and layout, not filesystem scan
	// latency. Supply deterministic command results so the driver never relies
	// on a 50ms wall-clock race or the operator's real plan index.
	m.planRefreshCommand = func(_ *plans.Index, _ *config.Config, seq int) tea.Cmd {
		return func() tea.Msg { return plansRefreshDoneMsg{seq: seq} }
	}
	m.planSearchCommand = func(seq int, index *plans.Index, query plans.Query) tea.Cmd {
		return func() tea.Msg {
			results, err := plans.Search(index, query)
			return planSearchDoneMsg{seq: seq, results: results, err: err}
		}
	}
	m.sessionWarmRefreshCommand = func(seq int, query transcript.Query) tea.Cmd {
		return func() tea.Msg {
			results, err := transcript.Search(snapshot.transcriptIndex, snapshot.transcriptReaders, query)
			return sessionWarmRefreshDoneMsg{seq: seq, results: results, searchErr: err}
		}
	}
	m.sessionRefreshCommand = func(seq int) tea.Cmd {
		return func() tea.Msg { return sessionsRefreshDoneMsg{seq: seq} }
	}
	m.sessionSearchCommand = func(seq int, query transcript.Query) tea.Cmd {
		return func() tea.Msg {
			results, err := transcript.Search(snapshot.transcriptIndex, snapshot.transcriptReaders, query)
			return sessionSearchDoneMsg{seq: seq, results: results, err: err}
		}
	}
	d := &uitestDriver{model: m, ctx: ctx, cancel: cancel, tempRoot: tempRoot}
	d.model.daemonCommand = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, d.liveAccess.reject("uitest: live daemon access is disabled")
	}
	if err := d.applySize(size); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// hermeticTopologyRefresh enumerates sessions from the no-op Kitty client and
// overlays the fixture's daemon-owned state, which no Kitty title can carry
// (the busy anchor above all). Both maps are nil for a driver built without a
// fixture, which reproduces an unreachable daemon exactly as before. The
// daemon is never contacted either way, so the no-live-daemon guarantee holds.
//
// It carries the refresh sequence and the freshness metadata the real command
// does, so the model's one-refresh-in-flight controller settles here exactly as
// it does in production; a fixture fetch that dropped the sequence would leave
// every scenario stuck after its first refresh. The fixture's own listing stands
// in for a direct fallback, which is what it is: no daemon is reachable.
func hermeticTopologyRefresh(states map[int]protocol.WindowStateSnapshot, watchdog map[int]protocol.WatchdogState) func(Model, int, int) tea.Cmd {
	return func(m Model, seq, _ int) tea.Cmd {
		return func() tea.Msg {
			windows, err := m.client.ListWindows()
			if err != nil {
				return commands.ErrMsg{Seq: seq, Err: err}
			}
			sessions, _ := session.ListFromOSWindowsWithConfig(windows, m.cfg)
			if states != nil {
				commands.ApplyDaemonStates(sessions, states)
			}
			return commands.SessionsMsg{
				Seq:      seq,
				Sessions: session.SortRunning(sessions, m.sortMode),
				Watchdog: watchdog,
				States:   states,
				Topology: commands.TopologyFreshness{
					Source:     commands.TopologyFromDirect,
					CapturedAt: m.now(),
				},
			}
		}
	}
}

func hermeticModelBackgroundCommands() modelBackgroundCommands {
	return modelBackgroundCommands{
		topologyRefresh: hermeticTopologyRefresh(nil, nil),
		activeLoops: func(seq int) tea.Cmd {
			return func() tea.Msg { return commands.ActiveLoopsMsg{Seq: seq} }
		},
		health: func(_ Model, checkDaemon, checkHooks bool) tea.Cmd {
			return func() tea.Msg { return commands.HealthMsg{DaemonChecked: checkDaemon, HookChecked: checkHooks} }
		},
		detail: func(windowID int) tea.Cmd {
			return func() tea.Msg {
				return commands.DetailEventsMsg{WindowID: windowID, Err: errors.New("uitest: live daemon access is disabled")}
			}
		},
		worktrees: func(_ Model, seq int) tea.Cmd {
			return func() tea.Msg { return worktreesLoadedMsg{seq: seq, entries: []actions.WorktreeView{}} }
		},
		worktreeAction: func(_ Model, id actions.ID, _ actions.Request) tea.Cmd {
			return func() tea.Msg {
				return worktreeActionDoneMsg{action: id, err: errors.New("uitest: live worktree action access is disabled")}
			}
		},
		aslSnapshot: func(_ Model, seq int) tea.Cmd {
			return func() tea.Msg {
				return aslSnapshotLoadedMsg{seq: seq, status: aslbridge.Status{Reason: "uitest: live agent-session-loop access is disabled"}}
			}
		},
		aslWorkbench: func(_ Model, rowID, _, focus string) tea.Cmd {
			return func() tea.Msg {
				return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
			}
		},
		aslQuestion: func(_ Model, seq int, _, _ string) tea.Cmd {
			return func() tea.Msg {
				return aslQuestionLoadedMsg{seq: seq, err: errors.New("uitest: live agent-session-loop access is disabled")}
			}
		},
		aslAction: func(_ Model, seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
			return func() tea.Msg {
				return aslActionDoneMsg{
					seq: seq, control: control, rowID: rowID, method: req.Method,
					operationID: req.OperationID,
					err:         errors.New("uitest: live agent-session-loop access is disabled"),
				}
			}
		},
	}
}

// newUITestConfigDriver stages an isolated environment under root, loads the
// config at srcConfigPath from inside it, and builds a driver backed by a no-op
// recording Kitty client reporting windows. setenv applies the isolation env
// vars (pass t.Setenv in tests, os.Setenv in the CLI). It returns the driver,
// the recording client, and the staged environment description so callers can
// assert that no real user paths are used.
func newUITestConfigDriver(root, srcConfigPath string, windows []kitty.OSWindow, size UITestSize, setenv func(string, string) error) (*uitestDriver, *uitestKittyClient, uitestConfigEnv, error) {
	env, err := stageUITestConfigEnv(root, srcConfigPath, setenv)
	if err != nil {
		return nil, nil, uitestConfigEnv{}, err
	}
	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, nil, env, fmt.Errorf("uitest: loading staged config: %w", err)
	}
	client := newUITestKittyClient(windows)
	d, err := newUITestDriverUnder(NewModel(cfg, client), size, root)
	if err != nil {
		return nil, nil, env, err
	}
	d.loadSessionsFrom(client, cfg, nil, nil)
	return d, client, env, nil
}

// loadSessionsFrom populates the model's session list the way the production
// fetch command does, but synchronously: it enumerates managed sessions from
// client + cfg, overlays the caller's daemon-owned state, and feeds the result
// through Model.Update as a commands.SessionsMsg. This lets a fixture- or config-backed
// driver start with a populated list without running the refresh ticker, and
// keeps that first list identical to what a later tick's hermetic initial fetch
// would produce. A query error leaves the model with an empty list rather than
// failing driver construction.
func (d *uitestDriver) loadSessionsFrom(client kitty.KittyClient, cfg *config.Config, states map[int]protocol.WindowStateSnapshot, watchdog map[int]protocol.WatchdogState) {
	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		return
	}
	if states != nil {
		commands.ApplyDaemonStates(sessions, states)
	}
	// This load stands in for the startup refresh the constructor armed, so it
	// carries that refresh's sequence: settling it is what lets the first tick
	// start a refresh instead of collapsing into the pending bit.
	d.recordErr(d.dispatch(commands.SessionsMsg{
		Seq:      d.model.topologyRefreshActiveSeq,
		Sessions: session.SortRunning(sessions, d.model.sortMode),
		Watchdog: watchdog,
		States:   states,
		Topology: commands.TopologyFreshness{
			Source:     commands.TopologyFromDirect,
			CapturedAt: d.model.now(),
		},
	}))
}
