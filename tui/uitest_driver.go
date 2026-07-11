package tui

// This file drives the real Bubble Tea dashboard Model in-process, without a
// terminal or the runtime event loop. It feeds deterministic input (window
// sizes, keys, ticks) straight through Model.Update and drains the resulting
// commands so refresh-style flows settle before an observation is captured.
//
// Draining is the delicate part. Bubble Tea v2 aggregates commands into
// slice-shaped batch/sequence messages, so a single Update can fan out into
// many follow-up commands. Some of those commands are time-based (e.g.
// tea.Tick), which a real event loop would schedule for the future; running
// them inline would block. The driver therefore executes every command with a
// short timeout and abandons any that don't complete promptly, keeping the
// harness deterministic and non-blocking while still letting fast refresh
// commands (session fetches, launch progress) feed their messages back through
// the model.

import (
	"context"
	"fmt"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

type uitestActivityMarker struct{}

func (uitestActivityMarker) MarkWorkspaceRead(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}

func (uitestActivityMarker) MarkAllWorkspacesRead(context.Context, string) (int64, error) {
	return 1, nil
}

const (
	// uitestDrainMaxSteps bounds how many commands a single action may drain.
	// Refresh flows re-enqueue fetches, so the cap is a safety net against a
	// runaway chain rather than an expected limit.
	uitestDrainMaxSteps = 256
	// uitestCmdTimeout is how long the driver waits for one command to yield a
	// message before abandoning it as a scheduled/future effect.
	uitestCmdTimeout = 50 * time.Millisecond
)

// UITestDriver holds a live Model plus the terminal size it is rendered at, and
// applies scenario actions to it. It never talks to a live Kitty instance; all
// side effects flow through the injected kitty.KittyClient (a no-op recorder in
// scenario runs), so it stays independent of CLI flags and fixture setup.
type UITestDriver struct {
	model Model
	size  UITestSize
}

// NewUITestDriver builds a driver around a freshly constructed Model. The model
// is created from prepared config and an injected client (both supplied by the
// caller / Session 3), then sized with an initial tea.WindowSizeMsg.
func NewUITestDriver(cfg *config.Config, client kitty.KittyClient, size UITestSize) *UITestDriver {
	return NewUITestDriverModel(NewModel(cfg, client), size)
}

// NewUITestDriverModel wraps an existing Model — useful when a test has already
// loaded sessions or otherwise prepared state — and applies the initial size.
func NewUITestDriverModel(m Model, size UITestSize) *UITestDriver {
	// Fixture drivers must remain hermetic: their direct sessionsMsg updates
	// exercise the legacy renderer, while workspace projection tests inject a
	// completed message explicitly instead of opening real indexes or SQLite.
	m.workspaceProjectionDisabled = true
	m.workspaceActivityMarker = uitestActivityMarker{}
	d := &UITestDriver{model: m}
	d.applySize(size)
	return d
}

// NewUITestFixtureDriver builds a driver from a built-in fixture: it constructs
// the model with the fixture's config and a no-op recording Kitty client (which
// reports the fixture's windows), applies size, and loads the fixture's running
// sessions synchronously so the list is populated without the refresh ticker
// (whose tea.Tick commands the harness abandons). Each zero dimension falls back
// independently to the fixture's own default for that dimension, so a caller can
// override width alone (or height alone) without zeroing the other. The recording
// client is returned so callers can assert on the Kitty calls the model attempted.
func NewUITestFixtureDriver(fx UITestFixture, size UITestSize) (*UITestDriver, *UITestKittyClient) {
	if size.Width == 0 {
		size.Width = fx.Size.Width
	}
	if size.Height == 0 {
		size.Height = fx.Size.Height
	}
	client := NewUITestKittyClient(fx.Windows)
	d := NewUITestDriver(fx.Config, client, size)
	d.loadSessionsFrom(client, fx.Config)
	if fx.WorkspaceHome {
		d.model.mode = ModeWorkspace
		d.model.workspaceSnapshot = fx.WorkspaceSnapshot
		d.model.workspaceCursor = 0
		d.model.workspaceFocus = workspaceFocusProjects
		d.model.clampWorkspaceSelection()
	}
	return d, client
}

// NewUITestConfigDriver stages an isolated environment under root, loads the
// config at srcConfigPath from inside it, and builds a driver backed by a no-op
// recording Kitty client reporting windows. setenv applies the isolation env
// vars (pass t.Setenv in tests, os.Setenv in the CLI). It returns the driver,
// the recording client, and the staged environment description so callers can
// assert that no real user paths are used.
func NewUITestConfigDriver(root, srcConfigPath string, windows []kitty.OSWindow, size UITestSize, setenv func(string, string) error) (*UITestDriver, *UITestKittyClient, UITestConfigEnv, error) {
	env, err := StageUITestConfigEnv(root, srcConfigPath, setenv)
	if err != nil {
		return nil, nil, UITestConfigEnv{}, err
	}
	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, nil, env, fmt.Errorf("uitest: loading staged config: %w", err)
	}
	client := NewUITestKittyClient(windows)
	d := NewUITestDriver(cfg, client, size)
	d.loadSessionsFrom(client, cfg)
	return d, client, env, nil
}

// loadSessionsFrom populates the model's session list the way the production
// fetch command does, but synchronously: it enumerates managed sessions from
// client + cfg and feeds them through Model.Update as a sessionsMsg. This lets
// a fixture- or config-backed driver start with a populated list without
// running the refresh ticker. A query error leaves the model with an empty
// list rather than failing driver construction.
func (d *UITestDriver) loadSessionsFrom(client kitty.KittyClient, cfg *config.Config) {
	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		return
	}
	d.dispatch(sessionsMsg{sessions: session.SortRunning(sessions, d.model.sortMode)})
}

// Model returns a copy of the current model for assertions that need more than
// an observation exposes.
func (d *UITestDriver) Model() Model { return d.model }

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
		return nil
	case UITestActionTick:
		d.Tick(action.Count)
		return nil
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
	d.dispatch(msg)
	return nil
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
	d.applySize(UITestSize{Width: width, Height: height})
}

// Tick advances the model's timer count times by injecting tickMsg values, so
// scenarios can exercise deterministic refresh flows. A non-positive count is
// treated as one tick.
func (d *UITestDriver) Tick(count int) {
	if count <= 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		d.dispatch(tickMsg{})
	}
}

// Inject sends an arbitrary message through the model. It is the escape hatch
// for scenarios (and tests) that need to simulate an async result the harness
// does not model as a first-class action.
func (d *UITestDriver) Inject(msg tea.Msg) {
	d.dispatch(msg)
}

// applySize records the new size and feeds a tea.WindowSizeMsg through the model.
func (d *UITestDriver) applySize(size UITestSize) {
	d.size = size
	d.dispatch(tea.WindowSizeMsg{Width: size.Width, Height: size.Height})
}

// dispatch feeds one message through Model.Update and drains any resulting
// command so refresh flows settle before the caller observes the model.
func (d *UITestDriver) dispatch(msg tea.Msg) {
	updated, cmd := d.model.Update(msg)
	d.model = updated.(Model)
	d.drain(cmd)
}

// drain executes cmd and everything it fans out into, feeding leaf messages back
// through the model. Slice-shaped batch/sequence messages are expanded into
// their component commands; time-based commands that do not return within
// uitestCmdTimeout are abandoned as scheduled effects. The step cap bounds the
// worst case so a self-rescheduling flow can never loop forever.
func (d *UITestDriver) drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < uitestDrainMaxSteps; steps++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg, ok := runUITestCmd(next, uitestCmdTimeout)
		if !ok || msg == nil {
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
}

// runUITestCmd executes cmd with a timeout. It returns the produced message and
// true when the command completes in time, or a nil message and false when it is
// abandoned. The goroutine's channel is buffered so an abandoned command can
// still send its (discarded) result and exit cleanly rather than leaking.
func runUITestCmd(cmd tea.Cmd, timeout time.Duration) (tea.Msg, bool) {
	if cmd == nil {
		return nil, false
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg, true
	case <-time.After(timeout):
		return nil, false
	}
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
