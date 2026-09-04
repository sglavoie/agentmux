package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the fail-closed proof for the item layer. Session 2 introduces a
// selectable kind that stands for no single session; every row-targeted action
// in ModeList must therefore refuse to act when the cursor is parked on one,
// because a summary that resolved to "some member" would let a close, a
// restart, a mute, or a status clear land on a row the operator never selected.
//
// BuildItems does not emit summaries yet, so the fixture below installs one
// directly. That is deliberate, and it is why m.items is a stored field rather
// than a value derived on read: the guarantee has to be provable before the
// projection can produce the kind, otherwise the change that first emits a
// summary is also the change that discovers which action was unguarded.

// summaryModel returns a model whose list holds one family summary followed by
// the two sessions it speaks for, with the cursor parked on the summary. It
// also returns the Kitty runner behind the client, so a caller can prove no
// Kitty call was made, and the number of calls already spent on setup.
func summaryModel(t *testing.T) (Model, *kitty.MockRunner, int) {
	t.Helper()
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		// A configured project matching both rows, so the `o` and `O`
		// directory/editor actions would resolve a target if the summary let
		// them through. Without it those two would refuse for the wrong reason
		// and the assertion would pass vacuously.
		Sessions: []config.Session{{Title: "api", Directory: "/repo", Account: "work"}},
	}
	runner := kitty.NewMockRunner(nil)
	m := NewModel(cfg, kitty.NewClientWithSocket(runner, ""))
	m.width, m.height = 120, 40

	rows := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", Project: "api", Directory: "/repo", State: "key", Harness: "claude", FamilyKey: "git:/repo", FamilyLabel: "repo"},
		{TabID: 2, WindowID: 12, Base: "web", Account: "work", Project: "api", Directory: "/repo", State: "bolt", Harness: "claude", FamilyKey: "git:/repo", FamilyLabel: "repo"},
	}
	m = loadSessions(m, rows)

	// Splice a summary in ahead of the two leaves it speaks for. The rows half
	// stays untouched: a summary adds a line, not a session.
	m.items = dashboard.ItemList{
		Rows: m.items.Rows,
		Items: append([]dashboard.Item{{
			Kind:        dashboard.ItemFamilySummary,
			Row:         -1,
			Members:     []int{0, 1},
			FamilyKey:   "git:/repo",
			FamilyLabel: "repo",
		}}, m.items.Items...),
	}
	m.cursor = 0

	if _, ok := m.selectedItem(); !ok {
		t.Fatal("setup: no item under the cursor")
	}
	if s, ok := m.selectedSession(); ok {
		t.Fatalf("setup: the summary resolved to session %+v; the fixture is not testing what it claims", s)
	}
	return m, runner, len(runner.Calls)
}

// forbidDaemonMutations fails the test if any command reaches the daemon while
// it is installed. Kitty is only half the blast radius: the status clear goes
// out over the daemon request seam, not over kitten.
func forbidDaemonMutations(t *testing.T) {
	t.Helper()
	previous := commands.DaemonRequest
	commands.DaemonRequest = func(req protocol.Request) (protocol.Response, error) {
		t.Errorf("a daemon request escaped with the cursor on a family summary: %+v", req)
		return protocol.Response{}, errors.New("forbidden")
	}
	t.Cleanup(func() { commands.DaemonRequest = previous })
}

// summaryRowActionKeys is every ModeList key whose handler resolves a session
// under the cursor and then acts on it. A new row-targeted key that is not
// added here is left unproven, which is the one way this guarantee decays.
var summaryRowActionKeys = []string{
	keyEnter,                // focus the selected tab
	keySpace,                // focus the selected tab
	keyA,                    // open the actions overlay against the selection
	keyE,                    // peek at the selected window
	keyR,                    // restart the selected session
	keyClose,                // close the selected session
	keyC,                    // reprofile the selected session
	keyDashboardLaunch,      // launch from the selected session's identity
	keyClearStatus,          // clear the selected session's icon (daemon)
	keyLock,                 // toggle the selected session's lock
	keyM,                    // toggle the selected session's mute
	keyO,                    // open the selected session's directory
	keyOpenEditor,           // open the selected session's directory in the editor
	keyY,                    // copy the selected session's title
	keyD,                    // open the selected session's detail pane
	keyOpenTimeline,         // open the selected session's timeline
	keyProjectSessionSearch, // search the selected session's project history
}

// TestSummaryItemRefusesEveryRowAction is the session's central safety claim: a
// synthetic summary cannot trigger a Kitty or daemon mutation. Each key is
// driven against a freshly built fixture, and each must dispatch no command at
// all — the action is not merely harmless, it is never issued.
func TestSummaryItemRefusesEveryRowAction(t *testing.T) {
	for _, key := range summaryRowActionKeys {
		t.Run("key="+key, func(t *testing.T) {
			forbidDaemonMutations(t)
			m, runner, setupCalls := summaryModel(t)

			updated, cmd := m.Update(keyPress(key))
			after := updated.(Model)

			if cmd != nil {
				// Run it so the failure names what would have happened rather
				// than only that something would have.
				t.Errorf("key %q dispatched a command with the cursor on a family summary; it produced %#v", key, cmd())
			}
			if after.mode != ModeList {
				t.Errorf("key %q left mode %d; a summary must not open a session-targeted overlay", key, after.mode)
			}
			if id := after.targetSession.TabID; id != 0 {
				t.Errorf("key %q captured target session tab %d from a summary", key, id)
			}
			if got := len(runner.Calls); got != setupCalls {
				t.Errorf("key %q made %d Kitty call(s) with the cursor on a family summary: %+v", key, got-setupCalls, runner.Calls[setupCalls:])
			}
		})
	}
}

// TestSummaryItemRefusesEveryPaletteCommand covers the second entry point to the
// same actions. The palette re-derives availability from the live model at
// render time rather than freezing it when the palette opens, so a summary must
// make every session command unavailable — and Run must stay a no-op even if a
// caller ignores that verdict.
func TestSummaryItemRefusesEveryPaletteCommand(t *testing.T) {
	forbidDaemonMutations(t)
	m, runner, setupCalls := summaryModel(t)

	ctx := captureCommandContext(m)
	if ctx.HasTarget {
		t.Fatalf("captureCommandContext claimed a target on a family summary: %+v", ctx.Target)
	}
	if ctx.Target.TabID != 0 || ctx.Target.WindowID != 0 {
		t.Errorf("captureCommandContext carried session %+v alongside HasTarget=false", ctx.Target)
	}

	for _, cmd := range commandCatalog() {
		if cmd.Group != commands.GroupSession {
			continue
		}
		if cmd.AvailabilityFor(m, ctx).Available {
			t.Errorf("session command %q is available with the cursor on a family summary", cmd.ID)
			continue
		}
		if _, teaCmd := cmd.Run(m, ctx); teaCmd != nil {
			t.Errorf("session command %q dispatched %#v despite being unavailable", cmd.ID, teaCmd())
		}
	}
	if got := len(runner.Calls); got != setupCalls {
		t.Errorf("the palette made %d Kitty call(s) for a family summary: %+v", got-setupCalls, runner.Calls[setupCalls:])
	}
}

// TestSummaryItemFooterOffersNoRowAction asserts the footer agrees with the key
// handlers: every row-section entry renders unavailable, so the operator is not
// invited to press a key that will refuse.
func TestSummaryItemFooterOffersNoRowAction(t *testing.T) {
	m, _, _ := summaryModel(t)

	if hasRow(m) {
		t.Fatal("hasRow reported a row with the cursor on a family summary")
	}
	for _, e := range listFooterBindings() {
		if e.section != footerSectionRow {
			continue
		}
		if e.availFn(m) {
			t.Errorf("footer entry %q (%s) is available with the cursor on a family summary", e.key, e.label)
		}
	}
}

// TestSummaryItemPreviewTracksNoWindow asserts the preview refresh does not
// reach into Kitty for a summary. Window 0 is the "nothing running" sentinel
// fetchPreviewCmd short-circuits, so the pane paints its placeholder instead of
// whichever member happened to be first.
func TestSummaryItemPreviewTracksNoWindow(t *testing.T) {
	m, runner, setupCalls := summaryModel(t)
	m.previewEnabled = true
	m.previewWindowID = 11 // as if the cursor had just left the first member

	cmd := m.refreshPreviewForCursor()
	if m.previewWindowID != 0 {
		t.Errorf("preview still tracks window %d; a summary owns no window", m.previewWindowID)
	}
	if cmd == nil {
		t.Fatal("expected the preview to be retargeted at the empty window")
	}
	msg, ok := cmd().(previewContentMsg)
	if !ok {
		t.Fatalf("preview refresh produced %#v, want previewContentMsg", msg)
	}
	if msg.windowID != 0 || msg.text != "" || msg.err != nil {
		t.Errorf("preview fetch = %+v, want the empty-window short circuit", msg)
	}
	if got := len(runner.Calls); got != setupCalls {
		t.Errorf("preview refresh made %d Kitty call(s) for a summary: %+v", got-setupCalls, runner.Calls[setupCalls:])
	}
}

// TestSummaryItemNavigationReachesItsMembers asserts the refusal above is about
// acting, not about moving: the cursor still steps off the summary onto the
// sessions it speaks for, and those sessions still resolve and still act.
func TestSummaryItemNavigationReachesItsMembers(t *testing.T) {
	m, _, _ := summaryModel(t)

	if got, want := m.listItemCount(), 3; got != want {
		t.Fatalf("listItemCount = %d, want %d (one summary plus two leaves)", got, want)
	}
	m = pressKey(m, "j")
	if m.cursor != 1 {
		t.Fatalf("j from the summary left the cursor at %d, want 1", m.cursor)
	}
	s, ok := m.selectedSession()
	if !ok {
		t.Fatal("the first member did not resolve to a session")
	}
	if s.TabID != 1 {
		t.Errorf("selected member TabID = %d, want 1", s.TabID)
	}
	if !hasRow(m) {
		t.Error("hasRow refused a session leaf sitting below a summary")
	}
	if ctx := captureCommandContext(m); !ctx.HasTarget || ctx.Target.TabID != 1 {
		t.Errorf("command context on a member = %+v, want the first member as target", ctx)
	}

	// G lands on the last item, which is the second leaf: the item count, not
	// the session count, is what the jump is bounded by.
	m = pressKey(m, "G")
	if m.cursor != 2 {
		t.Errorf("G left the cursor at %d, want 2", m.cursor)
	}
	if s, ok := m.selectedSession(); !ok || s.TabID != 2 {
		t.Errorf("last item resolved to %+v (ok=%v), want the second member", s, ok)
	}
}

// TestSummaryItemRendersOneLine pins the invariant the fit walk rests on: an
// item costs exactly one rendered line whatever its kind, so the line budget
// pagination charges is the one the renderer spends. The claim is made
// differentially — the same model rendered with and without the summary — so it
// does not have to encode how many lines the group labels and separators around
// the rows happen to cost.
func TestSummaryItemRendersOneLine(t *testing.T) {
	m, _, _ := summaryModel(t)

	withSummary := stripAnsi(renderListAt(m, m.width, 0))

	sessionsOnly := m
	sessionsOnly.rebuildItems() // back to one item per session
	if got, want := sessionsOnly.listItemCount(), m.listItemCount()-1; got != want {
		t.Fatalf("baseline has %d items, want %d", got, want)
	}
	baseline := stripAnsi(renderListAt(sessionsOnly, sessionsOnly.width, 0))

	got, want := renderedLineCount(withSummary), renderedLineCount(baseline)+1
	if got != want {
		t.Errorf("the summary added %d lines, want exactly 1\nwith summary:\n%s\nwithout:\n%s",
			got-renderedLineCount(baseline), withSummary, baseline)
	}
	if !strings.Contains(withSummary, "repo") {
		t.Errorf("expected the family label in the rendered list; got:\n%s", withSummary)
	}
	// The grouping key is built from filesystem paths and must never reach a
	// rendered surface.
	if strings.Contains(withSummary, "git:/repo") {
		t.Errorf("the family key leaked into a rendered row:\n%s", withSummary)
	}
}

func renderedLineCount(rendered string) int {
	return len(strings.Split(strings.TrimRight(rendered, "\n"), "\n"))
}
