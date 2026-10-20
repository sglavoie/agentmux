package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

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
			Base:      "an-unusually-long-project-title-that-no-band-can-hold",
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
// given size, view, and inspector intent.
//
// The boolean is intent, not outcome. It used to be the preview's single
// on/off bit, which a resize was allowed to clear; it now selects between the
// automatic default and an explicit hide, and whether a pane is actually drawn
// is left to the geometry the case is exercising. A caller asking for the
// inspector at 36 cells gets a queue-only dashboard, which is the case worth
// walking rather than one to skip.
func responsiveModel(width, height int, view dashboard.ViewMode, inspector bool) Model {
	m := loadSessions(testModel(), responsiveRows())
	m.listView = view
	m.rebuildItems()
	m.width = width
	m.height = height
	if inspector {
		m.inspectorDisplay = inspectorAutomatic
	} else {
		m.inspectorDisplay = inspectorHidden
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
				for _, inspector := range []bool{false, true} {
					name := view.name + "/w" + itoa(width) + "h" + itoa(height)
					if inspector {
						name += "/inspector"
					}
					t.Run(name, func(t *testing.T) {
						fn(t, responsiveModel(width, height, view.mode, inspector))
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
	t.Parallel()

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
	t.Parallel()

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

// TestResponsiveSplitNeverStarvesTheListColumn pins the split fallback rule:
// a split is drawn only when the queue column still clears the compact row
// floor, so there is no width at which the operator trades a readable list for
// an inspector beside an elided one.
func TestResponsiveSplitNeverStarvesTheListColumn(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		g := dashboardGeometry(m)
		if !g.split || g.fullScreen {
			return
		}
		if g.listWidth < dashboard.MinRowWidth() {
			t.Fatalf("split left a %d-cell queue column, below the compact floor %d",
				g.listWidth, dashboard.MinRowWidth())
		}
		if g.listWidth+1+g.previewWidth > m.width {
			t.Fatalf("queue %d + divider + inspector %d exceeds the viewport %d",
				g.listWidth, g.previewWidth, m.width)
		}
	})
}

// TestResponsiveGeometryIsStableUnderRepeatedResolution is the anti-oscillation
// guard, and with the inspector it has a sharper edge than it did under the
// preview. The body-row floor the split is gated on is measured from a chrome
// budget that includes the footer, and the footer's own row count is decided by
// what it prints — so a token that asked whether the inspector fit would make
// resolution a fixpoint search rather than a computation. Resolving twice and
// demanding the same answer is how that shows up as a failure instead of as a
// flickering pane.
//
// It asserts nothing about a detail strip: there is no longer one. The two rows
// under the list that used to restate the selection's metadata were retired
// with dashboard `p`, because the inspector carries the same facts at every
// width and those rows cost two sessions the list could have shown.
func TestResponsiveGeometryIsStableUnderRepeatedResolution(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		first := dashboardGeometry(m)
		if second := dashboardGeometry(m); second != first {
			t.Fatalf("geometry is not deterministic: %+v then %+v", first, second)
		}
		if got := footerBudgetLines(m); got != footerBudgetLines(m) {
			t.Fatal("footer budget is not deterministic")
		}
		if got := listChromeLines(m); got != listChromeLines(m) {
			t.Fatal("list chrome budget is not deterministic")
		}
	})
}

// TestResponsiveBandsAndSplitGeometry is what survived the project rail's
// removal. The rail was the third vertical strip and the only pane whose
// visibility had a width threshold of its own, so what is left is the two-strip
// sum and the band the queue's own width falls into — the facts that decide what
// a session row is allowed to spell.
func TestResponsiveBandsAndSplitGeometry(t *testing.T) {
	t.Parallel()

	base := responsiveModel(160, 40, dashboard.ViewUrgency, false)

	bands := []struct {
		width int
		want  dashboard.Band
	}{{79, dashboard.BandCompact}, {100, dashboard.BandStandard}, {160, dashboard.BandSpacious}}
	for _, tc := range bands {
		m := base
		m.width = tc.width
		g := dashboardGeometry(m)
		if got := dashboard.BandFor(g.listWidth); got != tc.want {
			t.Errorf("terminal width %d produced list width %d band %v, want %v", tc.width, g.listWidth, got, tc.want)
		}
	}

	// Two vertical strips on one row: queue and inspector. The sum is the
	// assertion — an off-by-one in the divider is a column of the terminal
	// nobody owns, which the renderer would either leave blank or overrun.
	split := responsiveModel(240, 40, dashboard.ViewUrgency, true)
	sg := dashboardGeometry(split)
	if !sg.split || sg.fullScreen ||
		sg.listWidth+1+sg.previewWidth != split.width || sg.listWidth < 80 {
		t.Fatalf("queue/inspector geometry = %+v", sg)
	}

	// Full screen is the inspector alone: the queue column is gone, so nothing
	// but the pane owns the row.
	full := split
	full.inspectorDisplay = inspectorFullScreen
	if g := dashboardGeometry(full); !g.fullScreen || g.previewWidth != full.width {
		t.Fatalf("full-screen inspector geometry = %+v", g)
	}
}

// TestProjectSelectorIsAlwaysOfferedInTheHeader is the header half of the
// switcher's "no geometry" claim. The rail's control appeared and disappeared
// with the strip it toggled, and reconciling that against the body's own
// visibility rule was the bug this matrix was written for. An overlay costs the
// body no columns, so the scope is unconditional.
//
// What the header states is the SCOPE, not the key that changes it: the key is
// named once, by the footer, and a header that repeated it would be the
// duplication the quiet chrome exists to remove. The scope stays a click target,
// which is what keeps the control reachable without the wording.
func TestProjectSelectorIsAlwaysOfferedInTheHeader(t *testing.T) {
	t.Parallel()

	for width := 90; width <= 200; width++ {
		for height := 8; height <= 30; height++ {
			m := responsiveModel(width, height, dashboard.ViewUrgency, true)
			header := stripAnsi(layout.SectionLines(renderHeader(m))[0])
			if !strings.Contains(header, "all projects") {
				t.Fatalf("%dx%d header omits the scope: %q", width, height, header)
			}
			if strings.Contains(header, keyProjectSwitcher+": ") {
				t.Fatalf("%dx%d header repeats the switcher key the footer names: %q", width, height, header)
			}
			var clickable bool
			for _, hit := range renderHeaderWithHits(m).hits {
				if hit.kind == mouseHitProjectSwitcher {
					clickable = true
				}
			}
			if !clickable {
				t.Fatalf("%dx%d scope is not a click target", width, height)
			}
		}
	}

	// An applied scope is named by the control rather than implied by it.
	m := responsiveModel(106, 9, dashboard.ViewUrgency, true)
	m.activeFilter = dashboard.Parse("#api")
	header := stripAnsi(layout.SectionLines(renderHeader(m))[0])
	if !strings.Contains(header, "api") {
		t.Fatalf("applied scope missing from header: %q", header)
	}

	// And the key itself is on screen in the footer, once there is room for it:
	// it is a primary-band hint, so a narrow row sheds it like any other and the
	// help sheet keeps it.
	wide := responsiveModel(160, 24, dashboard.ViewUrgency, true)
	if got := stripAnsi(rootFooterLines(wide)[0]); !strings.Contains(got, keyProjectSwitcher+" projects") {
		t.Fatalf("footer does not name the switcher key: %q", got)
	}
}

// TestResponsiveInspectorDrawsExactlyWhatItReserved is the budget/render
// agreement the detail strip's used to be. The strip is gone, and the pane that
// replaced it is charged against the same body-row budget the queue is, so the
// claim transfers rather than disappearing: whatever dashboardGeometry handed
// the inspector is what the inspector has to fill, to the row and to the cell.
//
// A pane that under-drew would leave the composed frame short of the viewport;
// one that over-drew would push the footer off the bottom of a terminal the
// scroll math had already clamped the cursor against.
func TestResponsiveInspectorDrawsExactlyWhatItReserved(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		g := dashboardGeometry(m)
		if !g.split {
			return
		}
		lines := layout.SectionLines(renderPreviewPane(m, g.previewWidth, g.previewHeight))
		if len(lines) > g.previewHeight {
			t.Fatalf("inspector drew %d rows against a reservation of %d", len(lines), g.previewHeight)
		}
		for i, line := range lines {
			if w := layout.StyledWidth(line); w > g.previewWidth {
				t.Fatalf("inspector line %d is %d cells wide, column is %d: %q",
					i, w, g.previewWidth, stripAnsi(line))
			}
		}
	})
}

// TestResponsiveChromeBudgetMatchesRenderedChrome walks the height budget against
// what the renderer actually emits above and below the list. Every term is
// render-and-measure now, so a chrome element that grew a line without telling
// the budget is a mismatch here rather than a clipped footer at run time.
func TestResponsiveChromeBudgetMatchesRenderedChrome(t *testing.T) {
	t.Parallel()

	responsiveCases(t, func(t *testing.T, m Model) {
		above := len(layout.SectionLines(renderHeader(m))) +
			len(layout.SectionLines(renderChips(m, m.width))) +
			1 /* spacer */
		if got := chromeAboveListLines(m); got != above {
			t.Fatalf("chrome above the list: budget %d, rendered %d", got, above)
		}
		if got, want := footerBudgetLines(m), len(rootFooterLines(m)); got != want {
			t.Fatalf("footer budget %d, rendered footer rows %d", got, want)
		}
		// Nothing is drawn between the panes and the padding row, so the total
		// chrome charge is exactly what is above the list plus what is below the
		// footer. A reintroduced strip that charged rows without rendering them
		// would move the cursor off the bottom of the window it was clamped to.
		if got, want := listChromeLines(m), above+dashboardPaddingLines+len(rootFooterLines(m))+statusBarLines; got != want {
			t.Fatalf("total chrome budget %d, rendered chrome %d", got, want)
		}
	})
}

// TestResponsiveCursorStaysVisibleInShortTerminals is the cursor/scroll check
// the brief calls for: after jumping to the last item in a terminal too short to
// show the whole list, the offset the scroll math settled on has to place the
// cursor inside the window the renderer was given.
func TestResponsiveCursorStaysVisibleInShortTerminals(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	// Each required control, in the labelled spelling and the bare-key one the
	// row falls back to. The row must carry one of the two for every control.
	required := [][2]string{
		{"j/k up/down", "j/k"},
		{"enter open", "↵"},
		{": commands", ":"},
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
	t.Parallel()

	for _, width := range []int{dashboard.MinRowWidth(), 24, 36, 48, 60, 79} {
		m := responsiveModel(width, 24, dashboard.ViewUrgency, false)
		plain := stripAnsi(rootFooterLines(m)[0])
		labelled := strings.Contains(plain, "j/k up/down")
		for _, label := range []string{"enter open", ": commands", "? help"} {
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

// TestStandardAndSpaciousOutspendCompactWithinTwoRows is the complement: the
// standard and spacious bands are capped too, but at two rows rather than one,
// and they spend that budget on the labelled palette rather than on the compact
// row's abbreviations. Extra width buys more of the palette, never a third row.
func TestStandardAndSpaciousOutspendCompactWithinTwoRows(t *testing.T) {
	t.Parallel()

	compact := rootFooterLines(responsiveModel(70, 30, dashboard.ViewUrgency, false))
	if len(compact) != 1 {
		t.Fatalf("compact band spent %d rows, want exactly 1: %q", len(compact), stripAnsi(strings.Join(compact, " / ")))
	}

	prev := 0
	for _, width := range []int{80, 119, 120, 160} {
		m := responsiveModel(width, 30, dashboard.ViewUrgency, false)
		rows := rootFooterLines(m)
		if len(rows) < 1 || len(rows) > footerHintMaxLines {
			t.Errorf("width %d: footer spent %d rows, want 1..%d", width, len(rows), footerHintMaxLines)
		}
		plain := stripAnsi(strings.Join(rows, " "))
		// The labelled spelling, not the compact row's `j/k move` / bare keys.
		for _, want := range []string{"j/k up/down", "a actions", ": commands", "/ filter", "? help"} {
			if !strings.Contains(plain, want) {
				t.Errorf("width %d: footer is missing essential %q: %q", width, want, plain)
			}
		}
		// Wider terminals reveal strictly more of the palette. Counting
		// separators counts hints without naming which ones a width affords.
		hints := strings.Count(plain, strings.TrimSpace(footerHintSeparator))
		if hints < prev {
			t.Errorf("width %d carries fewer hints (%d) than the narrower band did (%d): %q",
				width, hints, prev, plain)
		}
		prev = hints
	}
}

// TestHeaderNamesTheActiveView is the orientation the compact band costs the
// most: with group borders and half the row fields gone, the header line is the
// only thing left saying which projection the list is under.
func TestHeaderNamesTheActiveView(t *testing.T) {
	t.Parallel()

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
