//go:build tuitest

package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/cmdtest"
	"github.com/sglavoie/agentmux/tui/commands"
)

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
	case UITestActionMouse:
		return d.Mouse(action.X, action.Y, action.Event)
	case UITestActionCommand:
		return d.Command(commands.ID(action.CommandID))
	default:
		return fmt.Errorf("uitest: unknown action type %q", action.Type)
	}
}

// Mouse obtains the handler from the last rendered View, executes its command,
// and drains the resulting semantic message through Update. Scenarios therefore
// exercise the render-owned hit map rather than calling model helpers directly.
func (d *uitestDriver) Mouse(x, y int, event string) error {
	view := d.model.View()
	if view.OnMouse == nil {
		return nil
	}
	var msg tea.MouseMsg
	switch event {
	case UITestMouseLeft:
		msg = tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
	case UITestMouseWheelUp:
		msg = tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}
	case UITestMouseWheelDown:
		msg = tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
	default:
		return fmt.Errorf("uitest: unknown mouse event %q", event)
	}
	if err := d.drain(view.OnMouse(msg)); err != nil {
		return err
	}
	return d.liveAccess.Err()
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

// Command runs the catalog command named by id the way an operator reaching it
// by name does: it opens the palette with the dashboard's own ':' key, walks
// the palette cursor onto the row with the palette's own navigation keys,
// checks the row the way the enter handler checks it, and only then presses
// enter. Nothing here calls a command's Execute directly, so a scenario that
// names a capability still exercises every gate between a keypress and the
// operation — and a regression anywhere along that path fails the scenario
// rather than being stepped over.
//
// Every way the path can fail is reported as its own error, because from the
// screen they are one symptom: enter did nothing. A scenario that could not
// tell "this command no longer exists" from "this command is correctly
// disabled here" would be a scenario that passes for the wrong reason the day
// one of them changes.
func (d *uitestDriver) Command(id commands.ID) error {
	if id == "" {
		return fmt.Errorf("uitest: command action requires a command id")
	}
	if d.model.mode != ModeList {
		return fmt.Errorf("uitest: command %q is unreachable from mode %q: the palette opens from the session list", id, d.model.mode.String())
	}
	if err := d.Key(keyColon); err != nil {
		return err
	}
	if d.model.mode != ModeCommandPalette {
		return fmt.Errorf("uitest: pressing %q left the model in mode %q instead of opening the command palette", keyColon, d.model.mode.String())
	}
	index, err := uitestCommandRowIndex(d.model.commandResults, id, uitestCommandIDSet()[string(id)])
	if err != nil {
		return err
	}
	if err := d.moveCommandCursor(index); err != nil {
		return err
	}
	// Visibility was already settled by finding a row at all: the palette
	// filters hidden commands out of its results, so a hidden id never gets
	// this far. Availability is the gate the enter handler applies, and it is
	// checked here so a disabled command fails the scenario WITHOUT enter ever
	// being pressed — the operation must not be issued, not merely refused.
	candidate := d.model.commandResults[index]
	if availability := candidate.AvailabilityFor(d.model, d.model.commandContext); !availability.Available {
		return fmt.Errorf("uitest: command %q is disabled for the current selection: %s", id, availability.Reason)
	}
	if err := d.Key(keyEnter); err != nil {
		return err
	}
	if d.model.mode == ModeCommandPalette {
		return fmt.Errorf("uitest: command %q left the palette open, so enter did not run it", id)
	}
	return nil
}

// moveCommandCursor walks the palette cursor onto index using the palette's own
// up/down keys rather than assigning the field, so a scenario can only select a
// row the operator's own navigation reaches. The step bound is what turns a
// cursor that stops moving into an error instead of a hang.
func (d *uitestDriver) moveCommandCursor(index int) error {
	for steps := 0; d.model.commandCursor != index; steps++ {
		if steps > len(d.model.commandResults) {
			return fmt.Errorf("uitest: palette cursor stopped at %d walking to row %d", d.model.commandCursor, index)
		}
		key := keyDown
		if d.model.commandCursor > index {
			key = keyUp
		}
		if err := d.Key(key); err != nil {
			return err
		}
	}
	return nil
}

// uitestCommandRowIndex resolves the one palette row carrying id, and names
// every other outcome as the distinct defect it is: an id no catalog entry
// declares is absent, a declared id the palette filtered out is hidden here,
// and an id carried by more than one row is ambiguous — the palette would run
// whichever the cursor happened to land on, which is not a thing a scenario may
// depend on. declared is whether the catalog declares id at all, which is what
// separates the first two.
func uitestCommandRowIndex(rows []commands.Command[Model], id commands.ID, declared bool) (int, error) {
	found := -1
	matches := 0
	for i, row := range rows {
		if row.ID != id {
			continue
		}
		matches++
		if found < 0 {
			found = i
		}
	}
	switch {
	case matches > 1:
		return -1, fmt.Errorf("uitest: command %q is ambiguous: %d palette rows carry that id", id, matches)
	case matches == 1:
		return found, nil
	case declared:
		return -1, fmt.Errorf("uitest: command %q is hidden for the current selection, so the palette offers no row for it", id)
	default:
		return -1, fmt.Errorf("uitest: no command with id %q exists", id)
	}
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
		if cmds, isBatch := cmdtest.Flatten(msg); isBatch {
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
	if cmdtest.IsTimer(cmd) {
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
		// A command may return because Close canceled its context. If both
		// channels are ready, select can pick the result; cancellation still
		// wins so shutdown never turns canceled work into a successful effect.
		if err := d.ctx.Err(); err != nil {
			return nil, true, err
		}
		return msg, true, nil
	case <-d.ctx.Done():
		return nil, true, d.ctx.Err()
	}
}
