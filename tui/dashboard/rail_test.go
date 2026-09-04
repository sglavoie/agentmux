package dashboard

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestBuildRailRowsAggregatesPriorityCasingTalliesAndAll(t *testing.T) {
	now := time.Now()
	rows := []session.RunningSession{
		{Project: "api", State: "bolt", PresentationChangedAt: now.Add(-time.Minute)},
		{FamilyLabel: "API", Project: "checkout", State: "error", PresentationChangedAt: now},
		{Project: "docs", State: "stop"},
		{Project: "DOCS", State: "", WindowID: 4},
		{State: "key"}, // contributes to All but has no project aggregate
	}
	last := map[int]time.Time{4: now.Add(-time.Hour)}
	got := BuildRailRows(rows, last, time.Minute)
	if len(got) != 3 {
		t.Fatalf("BuildRailRows() returned %d rows, want All plus two projects", len(got))
	}
	if got[0].Label != "All" || got[0].Project != "" || got[0].Counts != [3]int{2, 1, 2} {
		t.Fatalf("All row = %+v", got[0])
	}
	if got[1].Label != "API" || got[1].Project != "API" || got[1].Lead.State != "error" || got[1].Counts != [3]int{1, 1, 0} {
		t.Fatalf("first project row = %+v", got[1])
	}
	if got[2].Label != "docs" || got[2].Counts != [3]int{0, 0, 2} {
		t.Fatalf("second project row = %+v", got[2])
	}
}

func TestBuildRailRowsEmptyContextStillOffersAll(t *testing.T) {
	got := BuildRailRows(nil, nil, time.Minute)
	if len(got) != 1 || got[0].Label != "All" || got[0].Counts != [3]int{} {
		t.Fatalf("empty rail = %+v", got)
	}
}

func TestRailWidthAndRenderingClampWithoutBreakingUnicode(t *testing.T) {
	rows := []RailRow{{Label: "All"}, {Project: "long", Label: strings.Repeat("🧬project", 12), Counts: [3]int{12, 3, 456}}}
	if got := RailWidth(rows); got != RailMaxWidth {
		t.Fatalf("RailWidth() = %d, want max %d", got, RailMaxWidth)
	}
	for _, width := range []int{RailMinWidth, RailMaxWidth} {
		out := RenderRail(RailInput{Rows: rows, Cursor: 1, Width: width, AppliedProject: "LONG", Focused: true})
		if !utf8.ValidString(out) {
			t.Fatalf("render at width %d is invalid UTF-8", width)
		}
		for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if got := layout.Width(ansi.Strip(line)); got > width {
				t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, ansi.Strip(line))
			}
		}
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "N A I") || !strings.Contains(plain, "›●") {
			t.Fatalf("render lacks headings/cursor+scope markers: %q", plain)
		}
	}
}

func TestRailPaginationAndCursorScrollingAreIndependent(t *testing.T) {
	rows := make([]RailRow, 10)
	for i := range rows {
		rows[i] = RailRow{Project: string(rune('a' + i)), Label: string(rune('A' + i))}
	}
	offset := ScrollRailToCursor(len(rows), 8, 0, 5)
	start, end, top, bottom := RailVisibleRange(len(rows), offset, 5)
	if offset == 0 || 8 < start || 8 >= end || !top {
		t.Fatalf("scrolled range = offset %d [%d,%d), top=%v bottom=%v", offset, start, end, top, bottom)
	}
	out := ansi.Strip(RenderRail(RailInput{Rows: rows, Cursor: 8, Offset: offset, Width: RailMinWidth, Height: 5, Focused: true}))
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) > 5 {
		t.Fatalf("rail rendered %d lines into height 5: %q", len(lines), out)
	}
	if !strings.Contains(out, "▲") {
		t.Fatalf("scrolled rail lacks independent top marker: %q", out)
	}
}
