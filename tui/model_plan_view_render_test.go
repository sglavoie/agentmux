package tui

import (
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"
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
		switch row.sourceLine {
		case 0:
			want = styleDim.Render(row.text)
		case 1:
			want = styleSearch.Render(row.text)
		case 2:
			want = styleSelected.Render(row.text)
		default:
			t.Fatalf("unexpected source line in row %#v", row)
		}
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing styled row %q for source line %d:\n%q",
				row.text, row.sourceLine, out)
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

func TestPlanViewerRows_WrapsLongProseAndMapsSourceLine(t *testing.T) {
	lines := []string{
		"short",
		"the quick brown foxxxxxxxxxxxxxxxx jumped over the lazy dog.",
	}

	rows := planViewerRowsForWidth(lines, 16)
	want := []planViewerRow{
		{text: "short", sourceLine: 0},
		{text: "the quick brown", sourceLine: 1},
		{text: "foxxxxxxxxxxxxxx", sourceLine: 1},
		{text: "xx jumped over", sourceLine: 1},
		{text: "the lazy dog.", sourceLine: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %#v, want %#v", i, rows[i], want[i])
		}
	}
}

func TestPlanViewerRows_WrapsLongUnbrokenTokens(t *testing.T) {
	rows := planViewerRowsForWidth([]string{"abcdefghijkl"}, 5)
	want := []string{"abcde", "fghij", "kl"}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i, wantText := range want {
		if rows[i].text != wantText {
			t.Errorf("row %d text = %q, want %q", i, rows[i].text, wantText)
		}
		if rows[i].sourceLine != 0 {
			t.Errorf("row %d sourceLine = %d, want 0", i, rows[i].sourceLine)
		}
	}
}

func TestPlanViewerRows_PreservesEmptyLines(t *testing.T) {
	rows := planViewerRowsForWidth([]string{"", "after"}, 80)
	want := []planViewerRow{
		{text: "", sourceLine: 0},
		{text: "after", sourceLine: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %#v, want %#v", i, rows[i], want[i])
		}
	}
}

func TestPlanViewerRows_SeparatorsUseLogicalSourceLine(t *testing.T) {
	lines := []string{
		"--- very-long-plan-file-name.md ---",
		"ordinary content",
	}

	rows := planViewerRowsForWidth(lines, 8)
	if len(rows) < 3 {
		t.Fatalf("rows len = %d, want separator to wrap: %#v", len(rows), rows)
	}
	seenContent := false
	for _, row := range rows {
		switch row.sourceLine {
		case 0:
			if !row.isSeparator(lines) {
				t.Errorf("separator row %#v was not detected from source line", row)
			}
		case 1:
			seenContent = true
			if row.isSeparator(lines) {
				t.Errorf("content row %#v was detected as a separator", row)
			}
		default:
			t.Errorf("row sourceLine = %d, want 0 or 1", row.sourceLine)
		}
	}
	if !seenContent {
		t.Fatal("wrapped rows did not include the content line")
	}
}

func TestPlanViewerRows_MatchesUseLogicalSourceLine(t *testing.T) {
	lines := []string{
		"needle appears in a long enough line to wrap",
		"plain row",
	}
	rows := planViewerRowsForWidth(lines, 12)
	matches := computePlanViewerMatches(lines, "needle")
	if len(matches) != 1 || matches[0] != 0 {
		t.Fatalf("matches = %v, want logical source line 0", matches)
	}

	for _, row := range rows {
		got := row.isMatch(matches)
		want := row.sourceLine == 0
		if got != want {
			t.Errorf("row %#v isMatch = %v, want %v", row, got, want)
		}
	}
}

func TestPlanViewerRows_WrapsUnicodeWideCharacters(t *testing.T) {
	rows := planViewerRowsForWidth([]string{"こんにち"}, 7)
	want := []string{"こんに", "ち"}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i, wantText := range want {
		if rows[i].text != wantText {
			t.Errorf("row %d text = %q, want %q", i, rows[i].text, wantText)
		}
		if w := runewidth.StringWidth(rows[i].text); w > 7 {
			t.Errorf("row %d width = %d, want <= 7", i, w)
		}
	}
}

func TestPlanViewerRows_ZeroWidthFallsBackToBoundedWrap(t *testing.T) {
	line := strings.Repeat("x", planViewerFallbackWidth+1)
	rows := planViewerRows(Model{
		width:           0,
		planViewerLines: []string{line},
	})

	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2 from fallback width: %#v", len(rows), rows)
	}
	if got := len(rows[0].text); got != planViewerFallbackWidth {
		t.Errorf("first row len = %d, want fallback width %d", got, planViewerFallbackWidth)
	}
	if rows[1].text != "x" {
		t.Errorf("second row = %q, want final x", rows[1].text)
	}
}
