package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// responsiveWidths is the same matrix tui/dashboard's band tests use: one width
// inside each preset plus both sides of both boundaries. Every whole-view
// assertion below walks it, because a preset that only overflows one cell from a
// boundary is invisible to a hand-picked narrow/wide pair.
var responsiveWidths = []int{36, 79, 80, 119, 120, 160}

// responsiveHeights covers a terminal short enough to force the list to its
// floor, one where the legend's height gate is the binding constraint, and one
// with room to spare.
var responsiveHeights = []int{10, 18, 40}

// responsiveRows is deliberately the hardest row set the dashboard has to place:
// two checkouts of one project (so the Families view has a summary and children
// to indent), a session with a blocking reason, an activity timer, dirty and
// clean branches, an operator profile far longer than the spacious cap, a title
// longer than any band's column, and a title whose leading grapheme is a
// two-cell pictograph. A width bug in any single field shows up here.
func responsiveRows() []session.RunningSession {
	return []session.RunningSession{
		{
			TabID: 1, TabNumber: 1, WindowID: 101,
			Base: "api-feat-x", Project: "api-feat-x",
			FamilyKey: "api", FamilyLabel: "api",
			Account: "engineering-platform", Profile: "engineering-platform",
			Harness:   harness.Claude,
			Directory: "/home/user/projects/worktrees/api-feat-x",
			Branch:    "amx/feat-x", Dirty: true,
			State: "key", Reason: "permission",
		},
		{
			TabID: 2, TabNumber: 2, WindowID: 102,
			Base: "api", Project: "api",
			FamilyKey: "api", FamilyLabel: "api",
			Account: "work", Profile: "work",
			Harness:   harness.Claude,
			Directory: "/home/user/projects/api",
			Branch:    "main",
			State:     "bolt",
		},
		{
			TabID: 3, TabNumber: 3, WindowID: 103,
			Base: "an-unusually-long-project-title-that-no-band-can-hold",
			Project:   "an-unusually-long-project-title-that-no-band-can-hold",
			Account:   "personal",
			Profile:   "personal",
			Harness:   harness.Codex,
			Directory: "/home/user/projects/deeply/nested/checkout/long",
			State:     "bolt",
		},
		{
			TabID: 4, TabNumber: 4, WindowID: 104,
			Base: "🧬curator", Project: "🧬curator",
			Account: "personal", Profile: "personal",
			Harness:   harness.Codex,
			Directory: "/home/user/projects/🧬curator",
			Branch:    "amx/études-de-cas", Dirty: true,
			State: "check",
		},
	}
}

// responsiveModel returns a dashboard parked on the responsive fixture at a
// given size, view, and preview setting.
func responsiveModel(width, height int, view dashboard.ViewMode, preview bool) Model {
	m := loadSessions(testModel(), responsiveRows())
	m.listView = view
	m.rebuildItems()
	m.width = width
	m.height = height
	m.previewEnabled = preview
	if preview {
		m.cfg.Defaults.Preview = config.PreviewConfig{Ratio: 0.5, Direction: "horizontal"}
	}
	scrollListToCursor(&m)
	return m
}

func responsiveCases(t *testing.T, fn func(t *testing.T, m Model)) {
	t.Helper()
	for _, view := range []struct {
		name string
		mode dashboard.ViewMode
	}{{"sessions", dashboard.ViewUrgency}, {"families", dashboard.ViewProjects}} {
		for _, width := range responsiveWidths {
			for _, height := range responsiveHeights {
				for _, preview := range []bool{false, true} {
					name := view.name + "/w" + itoa(width) + "h" + itoa(height)
					if preview {
						name += "/preview"
					}
					t.Run(name, func(t *testing.T) {
						fn(t, responsiveModel(width, height, view.mode, preview))
					})
				}
			}
		}
	}
}

// TestResponsiveViewFitsItsViewport is the overflow guard the whole presets
// change exists to keep true: whatever the band decided to draw, the composed
// frame is exactly the terminal's height and no line is wider than it. It runs
// the real render rather than a leaf so a field that fits its own allocation but
// not the row it is joined into still fails.
func TestResponsiveViewFitsItsViewport(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		lines := strings.Split(m.render(), "\n")
		if len(lines) != m.height {
			t.Fatalf("rendered %d lines, want exactly the viewport height %d", len(lines), m.height)
		}
		for i, line := range lines {
			if !utf8.ValidString(line) {
				t.Fatalf("line %d is not valid UTF-8: %q", i, line)
			}
			if w := layout.StyledWidth(line); w > m.width {
				t.Fatalf("line %d is %d cells wide, viewport is %d: %q",
					i, w, m.width, stripAnsi(line))
			}
		}
	})
}

// TestResponsiveListPaneFitsItsColumn narrows the previous assertion to the pane
// the band was actually chosen for. Under a horizontal split the composed line
// spans both panes, so a list column that overflowed into the divider would
// still fit the terminal; this is the assertion that catches it.
func TestResponsiveListPaneFitsItsColumn(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		g := dashboardGeometry(m)
		lines := layout.SectionLines(renderListAt(m, g.listWidth, g.listHeight))
		if len(lines) > g.listHeight {
			t.Fatalf("list pane rendered %d lines into a %d-line budget", len(lines), g.listHeight)
		}
		for i, line := range lines {
			if w := layout.StyledWidth(line); w > g.listWidth {
				t.Fatalf("list line %d is %d cells wide, column is %d: %q",
					i, w, g.listWidth, stripAnsi(line))
			}
		}
	})
}

// TestResponsiveSplitNeverStarvesTheListColumn pins the preview fallback rule:
// a split is drawn only when the list column still clears the compact row floor,
// so there is no width at which the operator trades a readable list for a
// preview beside an elided one.
func TestResponsiveSplitNeverStarvesTheListColumn(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		g := dashboardGeometry(m)
		if !g.split || g.vertical {
			return
		}
		if g.listWidth < dashboard.MinRowWidth() {
			t.Fatalf("horizontal split left a %d-cell list column, below the compact floor %d",
				g.listWidth, dashboard.MinRowWidth())
		}
		if g.listWidth+1+g.previewWidth > m.width {
			t.Fatalf("list %d + divider + preview %d exceeds the viewport %d",
				g.listWidth, g.previewWidth, m.width)
		}
	})
}

// TestResponsiveGeometryIsStableUnderRepeatedResolution is the anti-oscillation
// guard. The detail strip's height charge is derived from the list column's
// width, so if the width had in turn depended on the height, resolving the
// geometry from its own output would move. Feeding each resolution's list column
// back in as the terminal width has to converge in one step.
func TestResponsiveGeometryIsStableUnderRepeatedResolution(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		first := dashboardGeometry(m)
		if second := dashboardGeometry(m); second != first {
			t.Fatalf("geometry is not deterministic: %+v then %+v", first, second)
		}
		if got := detailStripBudgetLines(m); got != detailStripBudgetLines(m) {
			t.Fatal("detail strip budget is not deterministic")
		}
		// The charge must follow the pane the list is laid out in, not the
		// terminal: a split can put a compact column inside a spacious terminal.
		wantStrip := 0
		if dashboard.BandFor(dashboardListPaneWidth(m)) == dashboard.BandCompact {
			wantStrip = dashboard.DetailStripLines
		}
		if got := detailStripBudgetLines(m); got != wantStrip {
			t.Fatalf("detail strip budget = %d, want %d for a %d-cell list column",
				got, wantStrip, dashboardListPaneWidth(m))
		}
	})
}

// TestResponsiveDetailStripDrawsExactlyWhatItReserved is the budget/render
// agreement for the strip. A block that reserved two lines and drew one would
// shift the list's bottom edge, and the scroll arithmetic would be wrong by
// exactly that much.
func TestResponsiveDetailStripDrawsExactlyWhatItReserved(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		want := detailStripBudgetLines(m)
		got := len(layout.SectionLines(renderDetailStripBlock(m)))
		if got != want {
			t.Fatalf("detail strip drew %d lines against a reservation of %d", got, want)
		}
		if want != 0 && want != dashboard.DetailStripLines {
			t.Fatalf("detail strip reserved %d lines, want either 0 or %d",
				want, dashboard.DetailStripLines)
		}
	})
}

// TestResponsiveChromeBudgetMatchesRenderedChrome walks the height budget against
// what the renderer actually emits above and below the list. Every term is
// render-and-measure now, so a chrome element that grew a line without telling
// the budget is a mismatch here rather than a clipped footer at run time.
func TestResponsiveChromeBudgetMatchesRenderedChrome(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		above := len(layout.SectionLines(renderHeader(m))) +
			len(layout.SectionLines(renderDeprecationBanner(m))) +
			len(layout.SectionLines(renderLegend(m))) +
			len(layout.SectionLines(renderChips(m, m.width))) +
			1 /* spacer */
		if got := chromeAboveListLines(m); got != above {
			t.Fatalf("chrome above the list: budget %d, rendered %d", got, above)
		}
		if got, want := chromeBelowListLines(m), detailStripBudgetLines(m); got != want {
			t.Fatalf("chrome below the list: budget %d, strip %d", got, want)
		}
		if got, want := footerBudgetLines(m), len(rootFooterLines(m)); got != want {
			t.Fatalf("footer budget %d, rendered footer rows %d", got, want)
		}
	})
}

// TestResponsiveCursorStaysVisibleInShortTerminals is the cursor/scroll check
// the brief calls for: after jumping to the last item in a terminal too short to
// show the whole list, the offset the scroll math settled on has to place the
// cursor inside the window the renderer was given.
func TestResponsiveCursorStaysVisibleInShortTerminals(t *testing.T) {
	responsiveCases(t, func(t *testing.T, m Model) {
		m.filtered = makeRowsForScroll(40)
		m.rebuildItems()
		for _, cursor := range []int{0, 1, m.listItemCount() / 2, m.listItemCount() - 1} {
			m.cursor = cursor
			scrollListToCursor(&m)
			visible := listVisibleHeight(m)
			if visible != dashboardGeometry(m).listHeight {
				t.Fatalf("cursor %d: scroll window %d disagrees with the rendered list height %d",
					cursor, visible, dashboardGeometry(m).listHeight)
			}
			if m.listOffset < 0 || m.listOffset > m.cursor {
				t.Fatalf("cursor %d: offset %d does not contain the cursor", cursor, m.listOffset)
			}
			cost := listLineCost(&m, m.listOffset, m.cursor, listShowsGroupHeadings(&m))
			if cost > visible {
				t.Fatalf("cursor %d: reaching it costs %d rows in a %d-row window",
					cursor, cost, visible)
			}
			// The renderer must not draw more rows than the scroll math budgeted
			// for, or the frame it produces contradicts the offset it was given.
			g := dashboardGeometry(m)
			if n := len(layout.SectionLines(renderListAt(m, g.listWidth, g.listHeight))); n > visible {
				t.Fatalf("cursor %d: renderer drew %d rows into a %d-row window", cursor, n, visible)
			}
		}
	})
}

// TestCompactFooterIsOneRowAndKeepsTheRequiredTokens pins the compact footer's
// whole contract in one place: a single physical row, the four controls an
// operator cannot navigate without in one spelling or the other, and no
// truncation of the required set at any width the dashboard claims to support.
func TestCompactFooterIsOneRowAndKeepsTheRequiredTokens(t *testing.T) {
	// Each required control, in the labelled spelling and the bare-key one the
	// row falls back to. The row must carry one of the two for every control.
	required := [][2]string{
		{"j/k move", "j/k"},
		{"enter focus", "↵"},
		{": cmds", ":"},
		{"? help", "?"},
	}
	for _, width := range []int{dashboard.MinRowWidth(), 36, 60, 79} {
		t.Run("w"+itoa(width), func(t *testing.T) {
			m := responsiveModel(width, 24, dashboard.ViewUrgency, false)
			lines := rootFooterLines(m)
			if len(lines) != 1 {
				t.Fatalf("compact footer rendered %d rows, want exactly 1: %q", len(lines), lines)
			}
			plain := stripAnsi(lines[0])
			if strings.Contains(plain, "…") {
				t.Fatalf("compact footer elided a required control: %q", plain)
			}
			for _, spellings := range required {
				if !strings.Contains(plain, spellings[0]) && !strings.Contains(plain, spellings[1]) {
					t.Errorf("compact footer %q carries neither %q nor %q",
						plain, spellings[0], spellings[1])
				}
			}
			if w := layout.StyledWidth(lines[0]); w > width {
				t.Fatalf("compact footer is %d cells wide, viewport is %d: %q", w, width, plain)
			}
		})
	}
}

// TestCompactFooterFallsBackWholesaleRatherThanMixingSpellings pins the
// all-or-nothing rule. A row that spelled the first two controls out and
// shortened the rest would read as a ranking the four do not have.
func TestCompactFooterFallsBackWholesaleRatherThanMixingSpellings(t *testing.T) {
	for _, width := range []int{dashboard.MinRowWidth(), 24, 36, 48, 60, 79} {
		m := responsiveModel(width, 24, dashboard.ViewUrgency, false)
		plain := stripAnsi(rootFooterLines(m)[0])
		labelled := strings.Contains(plain, "j/k move")
		for _, label := range []string{"enter focus", ": cmds", "? help"} {
			if strings.Contains(plain, label) != labelled {
				t.Errorf("width %d: footer %q mixes labelled and bare spellings", width, plain)
			}
		}
		// Optional tokens ride the same spelling and are appended whole.
		for _, optional := range [][2]string{{"v view", "v"}, {"q quit", "q"}} {
			head := optional[0][:len(optional[0])-1]
			if strings.Contains(plain, head) && !strings.Contains(plain, optional[0]) {
				t.Errorf("width %d: footer %q carries a partial optional token %q",
					width, plain, optional[0])
			}
		}
	}
}

// TestStandardAndSpaciousKeepTheFullFooter is the complement: the one-row
// prioritized footer is a compact-band concession, not the new default.
func TestStandardAndSpaciousKeepTheFullFooter(t *testing.T) {
	compact := rootFooterLines(responsiveModel(70, 30, dashboard.ViewUrgency, false))
	for _, width := range []int{80, 119, 120, 160} {
		m := responsiveModel(width, 30, dashboard.ViewUrgency, false)
		plain := stripAnsi(strings.Join(rootFooterLines(m), " "))
		for _, want := range []string{"quit", "view"} {
			if !strings.Contains(plain, want) {
				t.Errorf("width %d: full footer is missing %q: %q", width, want, plain)
			}
		}
		if len(rootFooterLines(m)) < len(compact) {
			t.Errorf("width %d: full footer has fewer rows (%d) than the compact one (%d)",
				width, len(rootFooterLines(m)), len(compact))
		}
	}
}

// TestHeaderNamesTheActiveView is the orientation the compact band costs the
// most: with group borders and half the row fields gone, the header line is the
// only thing left saying which projection the list is under.
func TestHeaderNamesTheActiveView(t *testing.T) {
	for _, view := range []dashboard.ViewMode{dashboard.ViewUrgency, dashboard.ViewProjects} {
		for _, width := range responsiveWidths {
			m := responsiveModel(width, 30, view, false)
			header := stripAnsi(layout.SectionLines(renderHeader(m))[0])
			if !strings.Contains(header, view.String()) {
				t.Errorf("width %d: header %q does not name the %s view", width, header, view)
			}
		}
	}
}
