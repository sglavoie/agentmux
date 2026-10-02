package plans

import (
	"reflect"
	"testing"

	runewidth "github.com/mattn/go-runewidth"
)

func TestPlanViewerRows_WrapsLongProseAndMapsSourceLine(t *testing.T) {
	lines := []string{
		"short",
		"the quick brown foxxxxxxxxxxxxxxxx jumped over the lazy dog.",
	}

	rows := ViewerRows(lines, 16)
	want := []ViewerRow{
		{Text: "short", SourceLine: 0},
		{Text: "the quick brown", SourceLine: 1},
		{Text: "foxxxxxxxxxxxxxx", SourceLine: 1},
		{Text: "xx jumped over", SourceLine: 1},
		{Text: "the lazy dog.", SourceLine: 1},
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
	rows := ViewerRows([]string{"abcdefghijkl"}, 5)
	want := []string{"abcde", "fghij", "kl"}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i, wantText := range want {
		if rows[i].Text != wantText {
			t.Errorf("row %d text = %q, want %q", i, rows[i].Text, wantText)
		}
		if rows[i].SourceLine != 0 {
			t.Errorf("row %d sourceLine = %d, want 0", i, rows[i].SourceLine)
		}
	}
}

func TestPlanViewerRows_PreservesEmptyLines(t *testing.T) {
	rows := ViewerRows([]string{"", "after"}, 80)
	want := []ViewerRow{
		{Text: "", SourceLine: 0},
		{Text: "after", SourceLine: 1},
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

	rows := ViewerRows(lines, 8)
	if len(rows) < 3 {
		t.Fatalf("rows len = %d, want separator to wrap: %#v", len(rows), rows)
	}
	seenContent := false
	for _, row := range rows {
		switch row.SourceLine {
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
			t.Errorf("row sourceLine = %d, want 0 or 1", row.SourceLine)
		}
	}
	if !seenContent {
		t.Fatal("wrapped rows did not include the content line")
	}
}

// TestPlanViewerMatchesStepToAnyQueryWord pins the viewer half of the
// multi-word contract: the plan matched because it holds every word, and each
// line showing any one of them is a place n/N should reach.
func TestPlanViewerMatchesStepToAnyQueryWord(t *testing.T) {
	lines := []string{"alpha here", "nothing", "then Beta", "alpha beta"}
	if got := ViewerMatches(lines, "alpha beta"); !reflect.DeepEqual(got, []int{0, 2, 3}) {
		t.Fatalf("matches = %v, want [0 2 3]", got)
	}
	if got := ViewerMatches(lines, `"alpha beta"`); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("phrase matches = %v, want [3]", got)
	}
}

func TestPlanViewerRows_MatchesUseLogicalSourceLine(t *testing.T) {
	lines := []string{
		"needle appears in a long enough line to wrap",
		"plain row",
	}
	rows := ViewerRows(lines, 12)
	matches := ViewerMatches(lines, "needle")
	if len(matches) != 1 || matches[0] != 0 {
		t.Fatalf("matches = %v, want logical source line 0", matches)
	}

	for _, row := range rows {
		got := row.isMatch(matches)
		want := row.SourceLine == 0
		if got != want {
			t.Errorf("row %#v isMatch = %v, want %v", row, got, want)
		}
	}
}

func TestPlanViewerRows_WrapsUnicodeWideCharacters(t *testing.T) {
	rows := ViewerRows([]string{"こんにち"}, 7)
	want := []string{"こんに", "ち"}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i, wantText := range want {
		if rows[i].Text != wantText {
			t.Errorf("row %d text = %q, want %q", i, rows[i].Text, wantText)
		}
		if w := runewidth.StringWidth(rows[i].Text); w > 7 {
			t.Errorf("row %d width = %d, want <= 7", i, w)
		}
	}
}
