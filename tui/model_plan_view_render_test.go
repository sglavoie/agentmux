package tui

import (
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestRenderPlanViewBody_WrapsLongLineWithoutTruncation(t *testing.T) {
	m := Model{
		width:           8,
		planViewerLines: []string{"abcdefghijklmnopqrstuvwxTAIL"},
	}

	out := stripAnsi(renderPlanViewBody(m, 10))
	if strings.Contains(out, "…") {
		t.Fatalf("rendered plan view used truncation ellipsis:\n%s", out)
	}
	for _, want := range []string{"abcdefgh", "ijklmnop", "qrstuvwx", "TAIL"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered plan view missing wrapped content %q:\n%s", want, out)
		}
	}
}

func TestRenderPlanViewBody_VisualScrollShowsWrappedTail(t *testing.T) {
	m := Model{
		width:            8,
		planViewerScroll: 3,
		planViewerLines:  []string{"abcdefghijklmnopqrstuvwxTAIL"},
	}

	out := stripAnsi(renderPlanViewBody(m, 3))
	if !strings.Contains(out, "TAIL") {
		t.Fatalf("visual scroll did not reveal wrapped tail:\n%s", out)
	}
	if strings.Contains(out, "abcdefgh") {
		t.Fatalf("visual scroll rendered the first wrapped row unexpectedly:\n%s", out)
	}
}

func TestRenderPlanViewBody_IndicatorsUseWrappedRowCount(t *testing.T) {
	m := Model{
		width:           8,
		planViewerLines: []string{"abcdefghijklmnopqrstuvwxTAIL"},
	}

	top := stripAnsi(renderPlanViewBody(m, 3))
	if !strings.Contains(top, "▼") {
		t.Fatalf("top window missing bottom indicator for wrapped rows:\n%s", top)
	}
	if strings.Contains(top, "▲") {
		t.Fatalf("top window showed unexpected top indicator:\n%s", top)
	}

	m.planViewerScroll = 1
	middle := stripAnsi(renderPlanViewBody(m, 3))
	if !strings.Contains(middle, "▲") || !strings.Contains(middle, "▼") {
		t.Fatalf("middle window should show both wrapped-row indicators:\n%s", middle)
	}
}

func TestRenderPlanViewBody_StylesEveryWrappedRowBySourceLine(t *testing.T) {
	m := Model{
		width:           8,
		planViewerLines: []string{"--- abcdefghijklmnop ---", "needleabcdefghijklmnop", "selectedabcdefghijklmnop"},
		planViewerMatchLines: []int{
			1,
		},
		planViewerCursor: 2,
	}

	out := renderPlanViewBody(m, 20)
	rows := planViewerRows(m)
	for _, row := range rows {
		var want string
		switch row.SourceLine {
		case 0:
			want = styleDim.Render(row.Text)
		case 1:
			want = styleSearch.Render(row.Text)
		case 2:
			want = styleSelected.Render(row.Text)
		default:
			t.Fatalf("unexpected source line in row %#v", row)
		}
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing styled row %q for source line %d:\n%q",
				row.Text, row.SourceLine, out)
		}
	}
}

func TestRenderPlanViewBody_RowsFitWithinModelWidth(t *testing.T) {
	m := Model{
		width: 12,
		planViewerLines: []string{
			"abcdefghijklmnopqrstuvwx",
			"こんにちこんにちは",
			"short",
		},
		planViewerMatchLines: []int{0, 1},
		planViewerCursor:     2,
	}

	out := stripAnsi(renderPlanViewBody(m, 20))
	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if w := runewidth.StringWidth(line); w > m.width {
			t.Errorf("rendered row %d width = %d, want <= %d: %q", i, w, m.width, line)
		}
	}
}

func TestPlanViewerRows_ZeroWidthFallsBackToBoundedWrap(t *testing.T) {
	line := strings.Repeat("x", layout.FallbackWidth+1)
	rows := planViewerRows(Model{
		width:           0,
		planViewerLines: []string{line},
	})

	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2 from fallback width: %#v", len(rows), rows)
	}
	if got := len(rows[0].Text); got != layout.FallbackWidth {
		t.Errorf("first row len = %d, want fallback width %d", got, layout.FallbackWidth)
	}
	if rows[1].Text != "x" {
		t.Errorf("second row = %q, want final x", rows[1].Text)
	}
}
