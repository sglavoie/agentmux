//go:build tuitest

package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty/kittytest"
)

// The two dashboard edge cases the committed scenarios cannot reach, and why
// they are here rather than in docs/tui-testing/scenarios/.
//
// A scenario is a JSON file over a static fixture. That buys reproducibility at
// the cost of two things: it cannot make a trapped Kitty call FAIL, because the
// recording client answers every focus and mutation with nil, and it cannot make
// the world CHANGE ITS MIND between refreshes, because the fixture's overlay is
// replayed identically on every tick. These two cases need exactly one of those
// each. They are still driven through the same harness — the same fixture, the
// same frozen clock, the same hermetic seams — so what they exercise is the real
// model reached through real keys, not a hand-assembled one.

// uitestFocusFailureClient is the recording client with one operation broken.
// Embedding rather than reimplementing is the point: every other call, including
// the ListWindows a refresh runs on, stays exactly what every other harness run
// gets, so a difference in the rendered list can only have come from the focus.
type uitestFocusFailureClient struct {
	*kittytest.Client
	err error
}

// FocusTab records the attempt and then refuses it. The attempt is still logged
// because "the dashboard tried and was told no" is a different state from "the
// dashboard never tried", and a test that could not tell them apart would keep
// passing against a build that had silently stopped dispatching.
func (c *uitestFocusFailureClient) FocusTab(matchExpr string) error {
	c.Calls = append(c.Calls, kittytest.Call{Op: "FocusTab", Match: matchExpr})
	return c.err
}

// TestAFailedFocusLeavesTheRowUnacknowledged is the unhappy half of enter.
//
// A successful focus ends the program: the operator is now looking at the pane,
// and the daemon clears the unseen bit from that observation itself. That path
// is unobservable from here by construction — there is no model left to assert
// against. The failing path is the one with something to say, and what it says
// is that nothing was consumed: the row keeps its marker, keeps its place at the
// head of Needs you, and the dashboard is still on screen with an error to
// report. A focus that acknowledged the row optimistically would throw away the
// one signal saying this session is still waiting on a human.
func TestAFailedFocusLeavesTheRowUnacknowledged(t *testing.T) {
	t.Parallel()

	d, client := newUITestFixtureDriver(unseenStopsFixture(), UITestSize{Width: 120, Height: 36})
	defer func() { _ = d.Close() }()

	refused := errors.New("kitty: no matching tab")
	d.model.client = &uitestFocusFailureClient{Client: client, err: refused}

	if before := uitestScreenText(d.Observe()); !strings.Contains(before, "• ▌ unread") {
		t.Fatalf("fixture did not start with the unread row marked:\n%s", before)
	}
	if err := d.Key("enter"); err != nil {
		t.Fatalf("enter: %v", err)
	}

	if got := len(client.CallsFor("FocusTab")); got != 1 {
		t.Fatalf("FocusTab calls = %d, want exactly one attempt", got)
	}
	if !errors.Is(d.model.actionErr, refused) {
		t.Fatalf("actionErr = %v, want the refusal surfaced", d.model.actionErr)
	}

	obs := d.Observe()
	if obs.Mode != "list" {
		t.Fatalf("mode = %q, want the dashboard still on screen", obs.Mode)
	}
	screen := strings.Join(obs.Screen.Lines, "\n")
	if !strings.Contains(screen, "Needs you (1)") {
		t.Errorf("the attention group did not survive the refusal:\n%s", screen)
	}
	if !strings.Contains(screen, "• ▌ unread") {
		t.Errorf("the refused focus acknowledged the row anyway:\n%s", screen)
	}
	if strings.Contains(screen, "• ▌ read") {
		t.Errorf("the refusal marked a row that had no news:\n%s", screen)
	}
}

// TestClearingTheUnseenBitDemotesTheRowOnTheNextRefresh is the transition a
// static fixture cannot express: the same window, same icon, same title, seen on
// the second look.
//
// The bit is daemon-owned — no key sets it and the dashboard never writes it —
// so the only honest way to drive the change is to answer the next refresh
// differently, which is exactly what the daemon does once the operator has read
// the pane. The overlay is derived from the fixture's own states with that one
// field cleared, so nothing else can drift: if the row moves for any reason
// other than seenness, the icon or the title would have had to move with it, and
// neither does.
func TestClearingTheUnseenBitDemotesTheRowOnTheNextRefresh(t *testing.T) {
	t.Parallel()

	fx := unseenStopsFixture()
	d, _ := newUITestFixtureDriver(fx, UITestSize{Width: 120, Height: 36})
	defer func() { _ = d.Close() }()

	before := uitestScreenText(d.Observe())
	if !strings.Contains(before, "Needs you (1)") || !strings.Contains(before, "• ▌ unread") {
		t.Fatalf("fixture did not start with an unread row leading Needs you:\n%s", before)
	}
	if !strings.Contains(before, "Idle (1)") {
		t.Fatalf("fixture did not start with exactly one idle row:\n%s", before)
	}

	seen := make(map[int]protocol.WindowStateSnapshot, len(fx.States))
	for window, state := range fx.States {
		state.Unseen = false
		seen[window] = state
	}
	d.model.backgroundCommands.topologyRefresh = hermeticTopologyRefresh(uitestSessionOverlay{
		States:   seen,
		Watchdog: fx.Watchdog,
		Families: fx.Families,
	})
	d.Tick(1)
	if err := d.Err(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	after := uitestScreenText(d.Observe())
	if strings.Contains(after, "Needs you") {
		t.Errorf("the attention group outlived its last unread row:\n%s", after)
	}
	if !strings.Contains(after, "Idle (2)") {
		t.Errorf("the demoted row did not join the other seen stop in Idle:\n%s", after)
	}
	if strings.Contains(after, "• ▌") {
		t.Errorf("a row marker survived a list with nothing unread in it:\n%s", after)
	}
	for _, row := range []string{"unread", "read", "working"} {
		if !strings.Contains(after, row) {
			t.Errorf("row %q went missing across the refresh:\n%s", row, after)
		}
	}
}
