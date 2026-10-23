package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestRootFrameIsTheOnlyComposition pins the property the frame exists for:
// the live View, the string render the scenario harness captures, and the frame
// the hit map was clipped against are one composition. Before the unification
// these were three walks over the same seven children, and only the first
// carried hit regions — so a committed scenario could assert a screen no
// operator would ever be shown.
func TestRootFrameIsTheOnlyComposition(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		// These are equivalent renders of one instant. A real clock can
		// cross an age/animation boundary between the three calls under -race.
		now := modelNow(m)
		m.now = func() time.Time { return now }
		frame := renderRootFrame(m)
		if got := m.render(); got != frame.text {
			t.Fatal("the scenario harness composes a different screen from renderRootFrame")
		}
		if got := m.View().Content; got != frame.text {
			t.Fatal("View composes a different screen from renderRootFrame")
		}
		// And the body it composed is the canonical dashboard frame's, row for
		// row, not merely a screen of the same height.
		body := layout.SectionLines(renderDashboardFrame(m).text)
		if n := newRootChrome(m).bodyHeight(); len(body) > n {
			body = body[:n]
		}
		composed := strings.Split(frame.text, "\n")
		for i, want := range body {
			if composed[i] != want {
				t.Fatalf("composed row %d is not the dashboard body's:\nwant %q\ngot  %q",
					i, stripAnsi(want), stripAnsi(composed[i]))
			}
		}
	})
}

// TestRootChromeBodyHeightMatchesTheComposedBody is the single-measurement
// invariant. The rows the body is clipped to and the rows an overlay is centred
// in used to be two calculations over two separate measurements of the same
// footer; now both read one rootChrome, and this asserts that the number it
// reports is the number the composition actually leaves for the body.
func TestRootChromeBodyHeightMatchesTheComposedBody(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		chrome := newRootChrome(m)
		lines := strings.Split(chrome.compose(renderDashboardFrame(m).text), "\n")
		if len(lines) != m.height {
			t.Fatalf("composed %d lines, want the viewport height %d", len(lines), m.height)
		}
		if got := len(lines) - len(chrome.footer) - statusBarLines; got != chrome.bodyHeight() {
			t.Fatalf("composition left %d body rows, bodyHeight reports %d", got, chrome.bodyHeight())
		}
		if got := rootBodyHeight(m); got != chrome.bodyHeight() {
			t.Fatalf("rootBodyHeight %d disagrees with the frame's chrome %d", got, chrome.bodyHeight())
		}
	})
}

// TestDashboardHitsStayInsideTheComposedBody guards the clip the frame applies.
// A region below the body is a click that lands on the footer or the status row
// and still moves the cursor; a region past the right edge is one that lands
// outside the terminal. Both are only reachable if the clip and the composition
// disagree about the body rectangle.
func TestDashboardHitsStayInsideTheComposedBody(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		frame := renderRootFrame(m)
		bodyHeight := newRootChrome(m).bodyHeight()
		for _, hit := range frame.hitMap.clicks {
			r := hit.rect
			if r.X < 0 || r.Y < 0 || r.X+r.Width > m.width || r.Y+r.Height > bodyHeight {
				t.Fatalf("hit kind %v at %+v escapes the %dx%d body", hit.kind, r, m.width, bodyHeight)
			}
		}
		if frame.hitMap.hasList {
			r := frame.hitMap.listPane
			if r.X+r.Width > m.width || r.Y+r.Height > bodyHeight {
				t.Fatalf("list pane %+v escapes the %dx%d body", r, m.width, bodyHeight)
			}
		}
	})
}

// TestSplitPanesAreDrawnWhereTheHitMapSaysTheyAre is the column half of the
// coordinate check below, and the one the panes can fail on their own.
//
// A pane is drawn by joining blocks left to right, and a block joined at its
// natural content width ends wherever its widest row happened to stop — while
// the hit map published in the very same pass says the queue is g.listWidth
// wide and the inspector begins one column after it. The queue rendered short
// by 34 columns at 120x40 and the two disagreed by exactly that much: a click
// near the right of the queue selected nothing, and a click on the inspector's
// leading columns was charged to the queue row behind it.
//
// The divider is the witness, because it is the one glyph whose column is
// decided by the join rather than by the content.
func TestSplitPanesAreDrawnWhereTheHitMapSaysTheyAre(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, m Model) {
		t.Helper()
		frame := renderRootFrame(m)
		if !frame.hitMap.hasList {
			return
		}
		lines := strings.Split(frame.text, "\n")
		list := frame.hitMap.listPane
		row := lines[list.Y]

		if !frame.hitMap.hasInspector {
			return
		}
		inspector := frame.hitMap.inspectorPane
		if got := cellAt(row, list.X+list.Width); got != "│" {
			t.Fatalf("queue %+v ends at column %d, which draws %q not the divider",
				list, list.X+list.Width, got)
		}
		if inspector.X != list.X+list.Width+1 {
			t.Fatalf("inspector starts at %d, one past the queue divider is %d", inspector.X, list.X+list.Width+1)
		}
		// And the split spans the terminal rather than stopping wherever the
		// inspector's own content did.
		if got := layout.StyledWidth(row); got != m.width {
			t.Fatalf("split row is %d cells wide in a %d-column terminal", got, m.width)
		}
	}

	responsiveCases(t, check)
	// The widest terminal the matrix reaches is still the one where a stale
	// queue width is most visible, because the inspector's column is largest
	// there and a short queue leaves the divider furthest from where the hit map
	// says it is.
	t.Run("wide split", func(t *testing.T) {
		m := responsiveModel(240, 40, dashboard.ViewUrgency, true)
		if g := dashboardGeometry(m); !g.split {
			t.Fatalf("no split to check: %+v", g)
		}
		check(t, m)
	})
}

// cellAt is the glyph drawn at a display column of a rendered row. It walks the
// row in display cells rather than indexing it, because a session title whose
// first grapheme is two cells wide makes byte and rune offsets lie about which
// column anything is in.
func cellAt(line string, col int) string {
	x := 0
	for _, r := range stripAnsi(line) {
		w := layout.Width(string(r))
		if col < x+w {
			return string(r)
		}
		x += w
	}
	return ""
}

// TestDashboardHitRowsNameTheRowTheyLandOn is the coordinate check the frame's
// single pass is supposed to make trivial. Every region is a terminal-cell
// rectangle in the composed screen, so the row a session's region names has to
// be the row that session's text was written to — through a right-aligned
// header measured in display cells, chips that may have wrapped, an inspector
// joined to the right of the list, and a title whose first grapheme is two cells
// wide. A region reconstructed from the finished string instead of emitted
// beside it is exactly what drifts here.
func TestDashboardHitRowsNameTheRowTheyLandOn(t *testing.T) {
	t.Parallel()

	for _, inspector := range []bool{false, true} {
		m := responsiveModel(160, 40, dashboard.ViewUrgency, inspector)
		m.activeFilter = dashboard.Parse("@personal busy")
		frame := renderRootFrame(m)
		composed := strings.Split(frame.text, "\n")

		seen := 0
		wide := false
		for _, hit := range frame.hitMap.clicks {
			if hit.kind != mouseHitSession || hit.identity.Base == "" {
				continue
			}
			seen++
			line := stripAnsi(composed[hit.rect.Y])
			// A row wide enough to be clicked can still be too narrow to spell
			// the whole title, so the row is identified by the prefix that
			// survives truncation rather than by the full base.
			want := rowPrefix(hit.identity.Base)
			if !strings.Contains(line, want) {
				t.Fatalf("inspector=%v: session %q has its region on row %d, which reads %q",
					inspector, hit.identity.Base, hit.rect.Y, line)
			}
			if strings.HasPrefix(hit.identity.Base, "🧬") {
				wide = true
			}
		}
		if seen == 0 {
			t.Fatalf("inspector=%v: no session regions to check", inspector)
		}
		// The wide-glyph row is the reason this test names a fixture instead of
		// running the responsive matrix; a fixture that lost it would leave the
		// assertion above passing on nothing but ASCII.
		if !wide {
			t.Fatalf("inspector=%v: the fixture no longer contributes a two-cell title", inspector)
		}
	}
}

// rowPrefix is the part of a session base name a row is still expected to spell
// after the renderer has truncated it to the column it was given.
func rowPrefix(base string) string {
	const keep = 8
	r := []rune(base)
	if len(r) <= keep {
		return base
	}
	return string(r[:keep])
}

// TestDashboardChromeIsOneVerticalBudget pins the consolidation of what used to
// be three spellings of one subtraction: the pane rows the geometry lays out
// from, the chrome total the scroll arithmetic charges, and the candidate
// measurement the footer fixpoint takes. A term added to one of them and not
// the others is what this catches.
func TestDashboardChromeIsOneVerticalBudget(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		chrome := dashboardChromeFor(m)
		if got, want := chrome.total()+chrome.paneRows(m.height), m.height; got != want {
			t.Fatalf("chrome %d + panes %d = %d, want the viewport height %d",
				chrome.total(), chrome.paneRows(m.height), got, want)
		}
		if got := listChromeLines(m); got != chrome.total() {
			t.Fatalf("listChromeLines %d disagrees with the chrome total %d", got, chrome.total())
		}
		// The footer fixpoint's substitution has to be the identity when it is
		// handed the footer the model settled on. If it were not, the budget the
		// position indicator is measured against would not be the budget the
		// renderer draws into.
		substituted := dashboardChromeWithFooter(m, footerBudgetLines(m))
		if substituted != chrome {
			t.Fatalf("withFooter(settled) = %+v, want %+v", substituted, chrome)
		}
	})
}

// TestDashboardGeometryReadsTheSharedChrome checks the geometry against the
// budget rather than against a re-derivation of it, in the plain-list case where
// the pane rows and the list rows are the same number.
func TestDashboardGeometryReadsTheSharedChrome(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		g := dashboardGeometry(m)
		if g.split {
			return
		}
		if want := clampListHeight(dashboardChromeFor(m).paneRows(m.height)); g.listHeight != want {
			t.Fatalf("plain-list height %d, shared chrome says %d", g.listHeight, want)
		}
	})
}

// TestOverlayBackdropIsTheCanonicalDashboardFrame pins that a dimmed backdrop is
// the dashboard rather than a second drawing that resembles it. renderDashboard
// used to compose the same seven children itself, so a chip that wrapped or a
// rail that was responsively suppressed had to be taught to it separately from
// the frame the operator was actually looking at.
func TestOverlayBackdropIsTheCanonicalDashboardFrame(t *testing.T) {
	t.Parallel()

	for _, mode := range []Mode{ModeActions, ModeConfirm, ModeDetail, ModeCommandPalette, ModeLiveSessionPicker} {
		m := testModelWithSessions()
		m.width, m.height = 120, 40
		m.mode = mode
		if got, want := rootBaseView(m), renderDashboardFrame(m).text; got != want {
			t.Fatalf("mode %v backdrop is not the canonical frame", mode)
		}
	}
}

// TestOverlayBackdropOwnershipIsOneDispatch is the other half of the same
// cleanup. Help recorded its return mode and dimmed that view; every other
// overlay went through a shorter switch that fell through to the dashboard, so
// a confirm raised from Past Sessions dimmed a screen the operator was not
// looking at while a help raised from the very same screen dimmed the right
// one. The two answers to "which view owns the backdrop" now come from one
// dispatch, and this asserts they agree for every mode either can return to —
// and that a non-dashboard mode really does keep its own view.
func TestOverlayBackdropOwnershipIsOneDispatch(t *testing.T) {
	t.Parallel()

	for _, mode := range []Mode{
		ModeList, ModePicker, ModeSessionSearch, ModeSessionView,
		ModePlanSearch, ModePlanView, ModeWorktrees, ModeASLRuns,
	} {
		m := testModelWithSessions()
		m.width, m.height = 120, 40

		help := m
		help.mode = ModeHelp
		help.helpReturnMode = mode

		confirm := m
		confirm.mode = ModeConfirm
		confirm.confirmReturnMode = mode

		// Rendered on the same model so the comparison isolates the dispatch
		// from the mode-sensitive chrome the two overlay modes legitimately
		// differ in.
		fromHelp := rootBaseViewFor(help, help.helpReturnMode)
		fromConfirm := rootBaseViewFor(help, confirm.confirmReturnMode)
		if fromHelp != fromConfirm {
			t.Fatalf("help and confirm disagree about the backdrop for %v", mode)
		}
		if helpBaseView(help) != fromHelp {
			t.Fatalf("help backdrop for %v bypasses the shared dispatch", mode)
		}
		if rootBaseView(confirm) != rootBaseViewFor(confirm, mode) {
			t.Fatalf("confirm backdrop for %v bypasses the shared dispatch", mode)
		}
		if fromHelp == "" {
			t.Fatalf("backdrop for %v rendered nothing", mode)
		}
		if mode != ModeList && fromHelp == rootBaseViewFor(help, ModeList) {
			t.Fatalf("backdrop for %v fell through to the dashboard", mode)
		}
	}
}

// TestOverlayBackdropRetainsTheViewItWasRaisedFrom is the behavior that
// unification bought, stated in terms an operator would recognize: an overlay
// raised from Worktrees dims Worktrees no matter which overlay it is.
func TestOverlayBackdropRetainsTheViewItWasRaisedFrom(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m.width, m.height = 120, 40
	m.mode = ModeConfirm
	m.confirmReturnMode = ModeWorktrees
	if got := stripAnsi(rootBaseView(m)); !strings.Contains(got, "Worktrees") {
		t.Fatalf("confirm raised from Worktrees dimmed something else:\n%s", got)
	}
	m.mode = ModeConfigPicker
	m.configPickerReturnMode = ModePicker
	if got := stripAnsi(rootBaseView(m)); got != stripAnsi(renderPicker(m)) {
		t.Fatal("config picker raised from the launcher dimmed something else")
	}
}
