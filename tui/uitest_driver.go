//go:build tuitest

// This file is the scenario driver: the in-process program that stands in for
// a real Bubble Tea run. It builds a model, feeds it actions, and hands back
// the screen each action produced — for a hand-written config, for a named
// fixture, and for the hermetic environment a scenario runs inside.

package tui

import (
	"context"
	"errors"
	"os"
	"sync"
)

// This file drives the real Bubble Tea dashboard Model in-process, without a
// terminal or the runtime event loop. It feeds deterministic input (window
// sizes, keys, ticks, and rendered mouse targets) through the Model and drains
// the resulting commands so refresh-style flows settle before an observation
// is captured.
//
// Draining is the delicate part. Bubble Tea v2 aggregates commands into
// slice-shaped batch/sequence messages, so a single Update can fan out into
// many follow-up commands. Scheduled tea.Tick/tea.Every effects are identified
// before launch and skipped. Every other command is tracked to completion (or
// cancellation) so no filesystem goroutine can outlive cleanup.
//
// Construction lives in uitest_driver_fixture.go and uitest_driver_setup.go;
// action dispatch and command draining live in uitest_driver_actions.go.

const (
	// uitestDrainMaxSteps bounds how many commands a single action may drain.
	// Refresh flows re-enqueue fetches, so the cap is a safety net against a
	// runaway chain rather than an expected limit.
	uitestDrainMaxSteps = 256
)

// errUITestPreviewRead is what a fixture's TextFailures answers a read with. The
// wording is fixed because a scenario asserts on the sentence the inspector
// paints, and a message that varied by run would make that assertion untestable.
var errUITestPreviewRead = errors.New("terminal read failed")

// uitestDriver holds a live Model plus the terminal size it is rendered at, and
// applies scenario actions to it. Fixture commands use in-memory data and
// record terminal actions, so scenarios stay independent of CLI flags.
type uitestDriver struct {
	model    Model
	size     UITestSize
	ctx      context.Context
	cancel   context.CancelFunc
	tempRoot string
	// pagedText is every text a pager was asked to show, in order.
	pagedText []string

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

	aslActionMu      sync.Mutex
	aslActions       []uitestASLAction
	terminalActionMu sync.Mutex
	terminalActions  []uitestTerminalAction
}

type uitestTerminalAction struct {
	Kind   string
	Target string
	Title  string
}

func (d *uitestDriver) recordTerminalAction(action uitestTerminalAction) {
	d.terminalActionMu.Lock()
	d.terminalActions = append(d.terminalActions, action)
	d.terminalActionMu.Unlock()
}

func (d *uitestDriver) TerminalActions(kind string) []uitestTerminalAction {
	d.terminalActionMu.Lock()
	defer d.terminalActionMu.Unlock()
	var matches []uitestTerminalAction
	for _, action := range d.terminalActions {
		if action.Kind == kind {
			matches = append(matches, action)
		}
	}
	return matches
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

// Observe captures a normalized snapshot of the current model and size.
func (d *uitestDriver) Observe() UITestObservation {
	return observeUITest(d.model, d.size)
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
