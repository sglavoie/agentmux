package tui

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	tuiworktrees "github.com/sglavoie/agentmux/tui/worktrees"
)

func cleanupUITestDriver(t testing.TB, d *UITestDriver) {
	t.Helper()
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("closing UI test driver: %v", err)
		}
	})
}

// TestUITestDriverOpenActionsAndResize drives the real model through a key press
// that opens the actions overlay and a resize, verifying an observation is
// captured after each step and reflects the new mode/size.
func TestUITestDriverOpenActionsAndResize(t *testing.T) {
	m := testModelWithSessions()
	m = loadSessions(m, makeSessions())
	d := NewUITestDriverModel(m, UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)

	obs0 := d.Observe()
	if obs0.Mode != "list" {
		t.Fatalf("expected initial mode list, got %q", obs0.Mode)
	}
	if obs0.Size != (UITestSize{Width: 80, Height: 24}) {
		t.Fatalf("expected initial size 80x24, got %+v", obs0.Size)
	}
	if len(obs0.Screen.Lines) == 0 {
		t.Fatal("expected initial screen to have lines")
	}

	if err := d.Key("a"); err != nil {
		t.Fatalf("Key(a) error: %v", err)
	}
	obs1 := d.Observe()
	if obs1.Mode != "actions" {
		t.Fatalf("expected mode actions after 'a', got %q", obs1.Mode)
	}

	d.Resize(100, 30)
	obs2 := d.Observe()
	if obs2.Size != (UITestSize{Width: 100, Height: 30}) {
		t.Fatalf("expected size 100x30 after resize, got %+v", obs2.Size)
	}
	// Resize must not close the overlay.
	if obs2.Mode != "actions" {
		t.Fatalf("expected mode to remain actions after resize, got %q", obs2.Mode)
	}
	if obs2.Screen.Height != 30 {
		t.Errorf("expected 30 rendered lines after resize, got %d", obs2.Screen.Height)
	}
}

func TestUITestDriverWorktreeOpenUsesHermeticActionSeam(t *testing.T) {
	fx := worktreesFixture()
	client := NewUITestKittyClient(fx.Windows)
	d := NewUITestDriver(fx.Config, client, UITestSize{Width: 120, Height: 30})
	cleanupUITestDriver(t, d)

	d.model.mode = ModeWorktrees
	d.model.worktreeFiltered = tuiworktrees.SortViews(fx.Worktrees)
	found := false
	for i, entry := range d.model.worktreeFiltered {
		if entry.ID == "wt-clean" {
			d.model.worktreeCursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("worktrees fixture is missing wt-clean")
	}

	if err := d.Key("o"); err != nil {
		t.Fatalf("Key(o) error: %v", err)
	}
	if got := d.Model().actionErr; got == nil || got.Error() != "uitest: live worktree action access is disabled" {
		t.Fatalf("shell action error = %v, want hermetic seam refusal", got)
	}
	if got := len(client.CallsFor("LaunchTab")); got != 0 {
		t.Fatalf("LaunchTab calls = %d, want none from refused hermetic action", got)
	}
}

// TestUITestDriverTextTypesEachRune verifies Text enters a search query one key
// at a time, matching how the model sees per-rune input.
func TestUITestDriverTextTypesEachRune(t *testing.T) {
	m := testModelWithSessions()
	m = loadSessions(m, makeSessions())
	d := NewUITestDriverModel(m, UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)

	if err := d.Key("/"); err != nil {
		t.Fatalf("Key(/) error: %v", err)
	}
	if d.Model().mode != ModeSearch {
		t.Fatalf("expected ModeSearch after '/', got %d", d.Model().mode)
	}
	if err := d.Text("api"); err != nil {
		t.Fatalf("Text error: %v", err)
	}
	if got := d.Model().filterText; got != "api" {
		t.Fatalf("expected filterText 'api', got %q", got)
	}
}

// TestUITestDriverTickDrainsWithoutBlocking verifies a tick action flows through
// the drain path — which runs fast refresh commands and abandons the
// re-scheduled 2s tick — without hanging or leaving list mode.
func TestUITestDriverTickDrainsWithoutBlocking(t *testing.T) {
	m := testModelWithSessions()
	d := NewUITestDriverModel(m, UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)

	done := make(chan struct{})
	go func() {
		d.Tick(1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Tick drained a blocking command instead of abandoning it")
	}

	if got := d.Observe().Mode; got != "list" {
		t.Errorf("expected mode list after tick, got %q", got)
	}
}

// TestUITestDriverKeyRejectsBadSpec verifies an invalid key spec surfaces an
// error rather than mutating the model.
func TestUITestDriverKeyRejectsBadSpec(t *testing.T) {
	d := NewUITestDriver(testModelWithSessions().cfg, testModel().client, UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)
	if err := d.Key("nope"); err == nil {
		t.Fatal("expected error for multi-rune key spec")
	}
}

// TestUITestDriverFlattenCmds verifies slice-shaped batch messages are expanded
// into their component commands and non-slice messages are left alone.
func TestUITestDriverFlattenCmds(t *testing.T) {
	leaf := func() tea.Msg { return tickMsg{} }
	batch := tea.Batch(leaf, leaf)
	if batch == nil {
		t.Fatal("expected a non-nil batch command")
	}
	cmds, ok := uitestFlattenCmds(batch())
	if !ok {
		t.Fatal("expected batch message to be recognized as a slice")
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 component commands, got %d", len(cmds))
	}

	if _, ok := uitestFlattenCmds(tickMsg{}); ok {
		t.Fatal("expected a plain message not to be treated as a batch")
	}
}

// TestUITestDriverSkipsTimersBeforeLaunch verifies scheduled effects are the
// only commands omitted by the harness and never start a goroutine.
func TestUITestDriverSkipsTimersBeforeLaunch(t *testing.T) {
	d := NewUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)

	fast := func() tea.Msg { return tickMsg{} }
	if msg, ran, err := d.runCommand(fast); err != nil || !ran || msg == nil {
		t.Fatalf("expected fast command to return a message, got ran=%v msg=%v err=%v", ran, msg, err)
	}

	started := false
	timer := tea.Tick(time.Hour, func(time.Time) tea.Msg {
		started = true
		return tickMsg{}
	})
	if msg, ran, err := d.runCommand(timer); err != nil || ran || msg != nil {
		t.Fatalf("timer command = ran=%v msg=%v err=%v, want skipped", ran, msg, err)
	}
	if started {
		t.Fatal("timer callback unexpectedly started")
	}
	if _, ran, err := d.runCommand(nil); err != nil || ran {
		t.Fatalf("nil command = ran=%v err=%v", ran, err)
	}
}

func TestUITestDriverCloseCancelsAndJoinsStartedCommandBeforeRemovingRoot(t *testing.T) {
	d := NewUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	root := d.tempRoot
	started := make(chan struct{})
	finished := make(chan struct{})
	runDone := make(chan error, 1)
	go func() {
		_, _, err := d.runCommand(func() tea.Msg {
			close(started)
			<-d.Context().Done()
			close(finished)
			return nil
		})
		runDone <- err
	}()
	<-started
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Close returned before the tracked command finished")
	}
	if err := <-runDone; err == nil {
		t.Fatal("cancelled command unexpectedly returned no error")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("fixture root still exists after Close: %v", err)
	}
}
