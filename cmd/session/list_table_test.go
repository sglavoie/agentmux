package session

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestSessionTableHasAHeaderAndRelativeAges(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "personal-claude", Project: "🐱amx", LastAt: now.Add(-3 * time.Minute), ID: "b03c41a5", Snippet: "Fix the parser"},
		{Harness: "codex", Profile: "work", LastAt: now.Add(-50 * time.Hour), ID: "01a1"},
	}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "HARNESS") {
		t.Fatalf("table = %q, want a header and two rows", out.String())
	}
	if !strings.Contains(lines[1], "3m ago") || !strings.HasSuffix(lines[1], "b03c41a5  Fix the parser") {
		t.Errorf("row 1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], " - ") || !strings.Contains(lines[2], "2d ago") || !strings.HasSuffix(lines[2], "01a1      -") {
		t.Errorf("row 2 = %q, want placeholder project and summary and a day age", lines[2])
	}
}

// The summary fills what a known terminal width leaves it, and falls back to
// the fixed cut when the width is unknown or leaves too little to read.
func TestSessionTableFitsTheSummaryToTheTerminal(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("word ", 40)
	views := []sessionView{{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "b03c41a5", Snippet: long}}
	// The columns before the summary take 47 cells: HARNESS, PROFILE, and
	// PROJECT are as wide as their headers, then "just now" and the ID.
	for _, tc := range []struct {
		width, want int
	}{
		{0, sessionSummaryWidth},
		{147, 100},
		{60, sessionSummaryWidth},
	} {
		var out bytes.Buffer
		if err := writeSessionTable(&out, views, nil, now, tc.width); err != nil {
			t.Fatal(err)
		}
		row := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")[1]
		summary := row[strings.Index(row, "word"):]
		if got := layout.Width(summary); got != tc.want {
			t.Errorf("width %d: summary is %d cells, want %d: %q", tc.width, got, tc.want, row)
		}
	}
}

func TestSessionTableSaysSoWhenEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := writeSessionTable(&out, nil, nil, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No indexed sessions match.\n" {
		t.Fatalf("empty table = %q", out.String())
	}
}

func TestFormatAgo(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "-"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(time.Minute), "just now"},
		{now.Add(-59 * time.Minute), "59m ago"},
		{now.Add(-23 * time.Hour), "23h ago"},
		{now.Add(-24 * time.Hour), "1d ago"},
	} {
		if got := formatAgo(tc.at, now); got != tc.want {
			t.Errorf("formatAgo(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}

// A printed session ID must resolve back to its conversation alone, so it is
// abbreviated against every indexed ID, grows past the minimum where IDs share
// a prefix, and stays whole where no prefix can tell two sessions apart.
func TestSessionIDAbbreviationsStayResolvable(t *testing.T) {
	metas := []transcript.SessionMeta{
		{Harness: "claude", ID: "b03c41a5-1111-4c07-b626-b4f973e40310"},
		{Harness: "codex", ID: "01a11272-2fca-72f2-a888-05539236fe3c"},
		{Harness: "codex", ID: "01a11272-13af-7072-9162-8b96dbe65356"},
		{Harness: "claude", ID: "shared-id-across-harnesses"},
		{Harness: "codex", ID: "shared-id-across-harnesses"},
		{Harness: "copilot", ID: "short"},
	}
	ids := abbreviateSessionIDs(metas)
	for id, want := range map[string]string{
		"b03c41a5-1111-4c07-b626-b4f973e40310": "b03c41a5",
		"01a11272-2fca-72f2-a888-05539236fe3c": "01a11272-2",
		"01a11272-13af-7072-9162-8b96dbe65356": "01a11272-1",
		"shared-id-across-harnesses":           "shared-id-across-harnesses",
		"short":                                "short",
		"not-indexed":                          "not-indexed",
	} {
		if got := ids.of(id); got != want {
			t.Errorf("of(%q) = %q, want %q", id, got, want)
		}
	}
	for _, meta := range metas {
		short := ids.of(meta.ID)
		for _, other := range metas {
			if other.ID != meta.ID && strings.HasPrefix(other.ID, short) {
				t.Errorf("%q also prefixes %q", short, other.ID)
			}
		}
	}
	if got := sessionIDAbbreviations(nil).of("b03c41a5-1111"); got != "b03c41a5-1111" {
		t.Errorf("nil abbreviations of = %q, want the whole ID", got)
	}
}
