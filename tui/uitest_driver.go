package tui

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

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

const (
	// uitestDrainMaxSteps bounds how many commands a single action may drain.
	// Refresh flows re-enqueue fetches, so the cap is a safety net against a
	// runaway chain rather than an expected limit.
	uitestDrainMaxSteps = 256
)

// UITestDriver holds a live Model plus the terminal size it is rendered at, and
// applies scenario actions to it. It never talks to a live Kitty instance; all
// side effects flow through the injected kitty.KittyClient (a no-op recorder in
// scenario runs), so it stays independent of CLI flags and fixture setup.
type UITestDriver struct {
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
	workbenchOpens []UITestWorkbenchOpen

	aslActionMu sync.Mutex
	aslActions  []UITestASLAction
}

// UITestASLAction records one submitted agent-session-loop mutation. It carries
// the target and the operation ID but never the operator's answer, so a test
// that prints the recording cannot leak reply text.
type UITestASLAction struct {
	RowID       string
	Method      string
	OperationID string
	QuestionID  string
	ActionToken string
}

// ASLActions returns the mutations the model submitted, in order.
func (d *UITestDriver) ASLActions() []UITestASLAction {
	d.aslActionMu.Lock()
	defer d.aslActionMu.Unlock()
	return append([]UITestASLAction(nil), d.aslActions...)
}

func (d *UITestDriver) recordASLAction(action UITestASLAction) {
	d.aslActionMu.Lock()
	d.aslActions = append(d.aslActions, action)
	d.aslActionMu.Unlock()
}

// UITestWorkbenchOpen records one attempted Workbench deep link. It carries the
// row and the requested focus but no URL, because a fixture never mints a
// tokenized link: the recording surface cannot leak a bearer token even if a
// test prints it.
type UITestWorkbenchOpen struct {
	RowID string
	Focus string
}

// WorkbenchOpens returns the Workbench deep links the model attempted, in order.
func (d *UITestDriver) WorkbenchOpens() []UITestWorkbenchOpen {
	d.workbenchMu.Lock()
	defer d.workbenchMu.Unlock()
	return append([]UITestWorkbenchOpen(nil), d.workbenchOpens...)
}

func (d *UITestDriver) recordWorkbenchOpen(open UITestWorkbenchOpen) {
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
func (d *UITestDriver) Model() Model { return d.model }

// Context is cancelled by Close and is the lifecycle signal captured by
// fixture commands that can block on I/O.
func (d *UITestDriver) Context() context.Context { return d.ctx }

// Close cancels pending work, joins every started command, and only then
// removes the temporary directory.
func (d *UITestDriver) Close() error {
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
func (d *UITestDriver) Size() UITestSize { return d.size }

// Observe captures a normalized snapshot of the current model and size.
func (d *UITestDriver) Observe() UITestObservation {
	return observeUITest(d.model, d.size)
}

// Apply executes a single scenario action against the model.
func (d *UITestDriver) Apply(action UITestAction) error {
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
func (d *UITestDriver) Key(spec string) error {
	msg, err := ParseUITestKey(spec)
	if err != nil {
		return err
	}
	return d.dispatch(msg)
}

// Text types each rune of s as an individual key press, mirroring how a user
// entering a filter or search query would be seen by the model.
func (d *UITestDriver) Text(s string) error {
	for _, r := range s {
		if err := d.Key(string(r)); err != nil {
			return err
		}
	}
	return nil
}

// Resize changes the terminal viewport and re-renders. A non-positive dimension
// is left to the model to handle (it clamps its own layout math).
func (d *UITestDriver) Resize(width, height int) {
	d.recordErr(d.applySize(UITestSize{Width: width, Height: height}))
}

// Tick advances the model's timer count times by injecting tickMsg values, so
// scenarios can exercise deterministic refresh flows. A non-positive count is
// treated as one tick.
func (d *UITestDriver) Tick(count int) {
	if count <= 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		d.recordErr(d.dispatch(tickMsg{}))
	}
}

// Inject sends an arbitrary message through the model. It is the escape hatch
// for scenarios (and tests) that need to simulate an async result the harness
// does not model as a first-class action.
func (d *UITestDriver) Inject(msg tea.Msg) {
	d.recordErr(d.dispatch(msg))
}

// applySize records the new size and feeds a tea.WindowSizeMsg through the model.
func (d *UITestDriver) applySize(size UITestSize) error {
	d.size = size
	return d.dispatch(tea.WindowSizeMsg{Width: size.Width, Height: size.Height})
}

// dispatch feeds one message through Model.Update and drains any resulting
// command so refresh flows settle before the caller observes the model.
func (d *UITestDriver) dispatch(msg tea.Msg) error {
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
func (d *UITestDriver) drain(cmd tea.Cmd) error {
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
func (d *UITestDriver) runCommand(cmd tea.Cmd) (tea.Msg, bool, error) {
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
func (d *UITestDriver) Err() error {
	if d == nil {
		return nil
	}
	d.errMu.Lock()
	defer d.errMu.Unlock()
	return errors.Join(d.err, d.liveAccess.Err())
}

func (d *UITestDriver) recordErr(err error) {
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
