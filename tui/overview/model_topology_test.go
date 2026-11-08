package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestAResizeKeepsTheSelectedSessionAndBringsItsPage is the resize rule. The page
// is derived from the selection, so shrinking the terminal until the selected tile
// is on a later page must move the PAGE rather than the cursor.
func TestAResizeKeepsTheSelectedSessionAndBringsItsPage(t *testing.T) {
	sessions := viewSessions(9)
	m, _ := loadedAt(t, sessions, 8)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	tabID := m.selectedTabID
	if page := Measure(160, 45).PageOf(m.selected); page != 0 {
		t.Fatalf("nine sessions on a 160x45 grid put the last one on page %d, want a single page", page)
	}

	for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 15}, {200, 6}, {160, 45}} {
		m = only(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if m.selectedTabID != tabID {
			t.Fatalf("resizing to %dx%d moved the selection to tab %d, want the retained %d", size[0], size[1], m.selectedTabID, tabID)
		}
		if m.width != size[0] || m.height != size[1] {
			t.Fatalf("the model is %dx%d, want %dx%d", m.width, m.height, size[0], size[1])
		}
		if got := strings.Split(Render(overviewInput(m)), "\n"); len(got) != size[1] {
			t.Fatalf("%dx%d rendered %d lines", size[0], size[1], len(got))
		}
		// The page follows the selection: the selected session is on the frame the
		// renderer produced, on whatever page it now falls.
		if Measure(size[0], size[1]).Capacity() > 0 {
			if frame := frameOf(m); !strings.Contains(frame, m.sessions[m.selected].Base) {
				t.Fatalf("the %dx%d frame does not show the selected session %q:\n%s", size[0], size[1], m.sessions[m.selected].Base, frame)
			}
		}
	}
}

// TestAnAnchorChangeKeepsTheSelectionAndDropsTheStaleCapture is the anchor rule,
// and the reason the tab and the window are two separate fields. A restarted
// harness keeps its tab and gets a new anchor window; the cursor must not move,
// and the capture of the window that is gone must not survive to be painted as if
// it were live.
//
// It runs on a real terminal size because eviction is the VISIBILITY rule: a
// surface with no dimensions has no page, so every buffer is off-page and the
// claim about the two survivors would be true for the wrong reason.
func TestAnAnchorChangeKeepsTheSelectionAndDropsTheStaleCapture(t *testing.T) {
	before := viewSessions(3)
	m, _ := loadedAt(t, before, 1)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	selected := m.selectedTabID
	oldWindow := m.selectedWindowID

	m.previews = map[session.PaneTarget]Preview{}
	for _, s := range before {
		m.previews[paneTargetForWindow(s.WindowID)] = Preview{Output: []string{"captured"}}
	}

	after := viewSessions(3)
	for i := range after {
		if after[i].TabID == selected {
			after[i].WindowID = oldWindow + 1000
			after[i].Pane = paneTargetForWindow(after[i].WindowID).Pane
		}
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, after))

	if m.selectedTabID != selected {
		t.Fatalf("the anchor change moved the selection to tab %d, want the retained %d", m.selectedTabID, selected)
	}
	if m.selectedWindowID != oldWindow+1000 {
		t.Fatalf("the selection's anchor window is %d, want the new %d", m.selectedWindowID, oldWindow+1000)
	}
	if _, ok := m.previews[paneTargetForWindow(oldWindow)]; ok {
		t.Fatalf("the capture of the retired window %d survived; it would be painted as a live terminal", oldWindow)
	}
	if len(m.previews) != 2 {
		t.Fatalf("%d captures survived, want the two whose windows are unchanged", len(m.previews))
	}
}

// TestPruningDoesNotReachBackIntoAnEarlierModel is the aliasing rule. Every handler
// takes the model by value, so a map mutated in place would be shared with the
// generation it was called on — harmless while Bubble Tea discards the old value,
// and exactly what would make two generations indistinguishable here.
func TestPruningDoesNotReachBackIntoAnEarlierModel(t *testing.T) {
	sessions := viewSessions(3)
	m, _ := loadedAt(t, sessions, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	m.previews = map[session.PaneTarget]Preview{}
	for _, s := range sessions {
		m.previews[paneTargetForWindow(s.WindowID)] = Preview{Output: []string{"captured"}}
	}
	previous := m

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, sessions[:1]))
	if len(m.previews) != 1 {
		t.Fatalf("the new generation holds %d captures, want 1", len(m.previews))
	}
	if len(previous.previews) != 3 {
		t.Fatalf("the earlier generation now holds %d captures; the prune mutated a shared map", len(previous.previews))
	}
}

// TestAClosedSessionTakesItsCaptureWithIt is the same pruning rule reached by the
// other event.
func TestAClosedSessionTakesItsCaptureWithIt(t *testing.T) {
	before := viewSessions(3)
	m, _ := loadedAt(t, before, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	m.previews = map[session.PaneTarget]Preview{}
	for _, s := range before {
		m.previews[paneTargetForWindow(s.WindowID)] = Preview{Output: []string{"captured"}}
	}
	gone := before[2]

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, before[:2]))
	if _, ok := m.previews[paneTargetForWindow(gone.WindowID)]; ok {
		t.Fatalf("the closed session's capture at window %d survived", gone.WindowID)
	}
	if len(m.previews) != 2 {
		t.Fatalf("%d captures survived, want 2", len(m.previews))
	}
}

// TestTheSelectedSessionVanishingSelectsTheNearestSurvivor is the removal rule.
// The nearest survivor is the row that took its PLACE, which is what an operator
// watching one tile close expects the cursor to land on.
func TestTheSelectedSessionVanishingSelectsTheNearestSurvivor(t *testing.T) {
	all := viewSessions(5)

	t.Run("a middle session closes", func(t *testing.T) {
		m, _ := loadedAt(t, all, 2)
		survivors := append(append([]session.RunningSession{}, all[:2]...), all[3:]...)
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, survivors))
		if m.selected != 2 || m.selectedTabID != all[3].TabID {
			t.Fatalf("selected index %d/tab %d, want index 2 on the session that took its place (tab %d)", m.selected, m.selectedTabID, all[3].TabID)
		}
	})

	t.Run("the last session closes", func(t *testing.T) {
		m, _ := loadedAt(t, all, 4)
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, all[:3]))
		if m.selected != 2 || m.selectedTabID != all[2].TabID {
			t.Fatalf("selected index %d/tab %d, want the new last session at index 2 (tab %d)", m.selected, m.selectedTabID, all[2].TabID)
		}
	})

	t.Run("everything before the selection closes", func(t *testing.T) {
		m, _ := loadedAt(t, all, 4)
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, snapshot(m.refreshActiveSeq, 0, all[4:]))
		if m.selected != 0 || m.selectedTabID != all[4].TabID {
			t.Fatalf("selected index %d/tab %d, want the surviving tab %d found by identity at index 0", m.selected, m.selectedTabID, all[4].TabID)
		}
	})
}

// TestTheEmptyTransitionsBothWays covers the list emptying and filling again. The
// cursor has nothing to be on in between, and it must not carry a dead identity
// across the gap.
func TestTheEmptyTransitionsBothWays(t *testing.T) {
	all := viewSessions(4)
	m, _ := loadedAt(t, all, 3)
	m.previews = map[session.PaneTarget]Preview{paneTargetForWindow(all[3].WindowID): {Output: []string{"captured"}}}

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, nil))
	if len(m.sessions) != 0 {
		t.Fatalf("%d sessions survived an empty read", len(m.sessions))
	}
	if m.selected != 0 || m.selectedTabID != 0 || m.selectedWindowID != 0 {
		t.Fatalf("an empty list left the cursor at %d/tab %d/window %d, want it cleared", m.selected, m.selectedTabID, m.selectedWindowID)
	}
	if len(m.previews) != 0 {
		t.Fatalf("%d captures survived an empty list", len(m.previews))
	}
	if got := m.feedback(); got != "No running sessions" {
		t.Fatalf("an empty warm list says %q, want the no-sessions sentence", got)
	}
	if _, ok := m.selectedSession(); ok {
		t.Fatal("an empty list still reports a selected session")
	}

	// Navigation and Enter on an empty list are no-ops rather than panics.
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, name := range []string{"tab", "end", "down", "pgdown", "home"} {
		m = press(t, m, name)
	}
	next, cmd := send(t, m, key(t, "enter"))
	m = next
	if cmd != nil {
		t.Fatal("Enter on an empty list started a command")
	}

	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, all))
	if m.selected != 0 || m.selectedTabID != m.sessions[0].TabID {
		t.Fatalf("refilling selected index %d/tab %d, want the first session", m.selected, m.selectedTabID)
	}
}
