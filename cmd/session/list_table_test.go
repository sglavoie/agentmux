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
	if err := writeSessionTable(&out, views, nil, now, 0, false); err != nil {
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
	// The columns before the summary take 57 cells: HARNESS, PROFILE, and
	// PROJECT are as wide as their headers, then "just now", MESSAGES, and
	// the ID.
	for _, tc := range []struct {
		width, want int
	}{
		{0, sessionSummaryWidth},
		{157, 100},
		{60, sessionSummaryWidth},
	} {
		var out bytes.Buffer
		if err := writeSessionTable(&out, views, nil, now, tc.width, false); err != nil {
			t.Fatal(err)
		}
		row := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")[1]
		summary := row[strings.Index(row, "word"):]
		if got := layout.Width(summary); got != tc.want {
			t.Errorf("width %d: summary is %d cells, want %d: %q", tc.width, got, tc.want, row)
		}
	}
}

// A session opened by a command on a long path is told apart from its
// siblings by the path's last element, which the row keeps in view.
func TestSessionTableCutsLongPathsInTheSummary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "b03c41a5",
		Snippet: "/asl run " + home + "/.local/share/agent-plans/4f4e36c66116-93448e2a42c6/ --harness claude"}}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0, false); err != nil {
		t.Fatal(err)
	}
	if want := "b03c41a5  /asl run …/4f4e36c66116-93448e2a42c6/ --harness claude\n"; !strings.HasSuffix(out.String(), want) {
		t.Fatalf("table = %q, want the row to end %q", out.String(), want)
	}
}

func TestSessionTableSaysSoWhenEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := writeSessionTable(&out, nil, nil, time.Now(), 0, false); err != nil {
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

// The harness's title names a conversation better than its opening prompt,
// which several sessions often share; the prompt stands in when there is none.
func TestSessionTableSummaryPrefersTheTitle(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "b03c41a5", Snippet: "Look at the project", Title: "Parser cleanup"},
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "c14d52b6", Snippet: "Look at the project"},
	}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if !strings.HasSuffix(lines[1], "b03c41a5  Parser cleanup") || !strings.HasSuffix(lines[2], "c14d52b6  Look at the project") {
		t.Fatalf("table = %q, want the title, else the opening prompt", out.String())
	}
}

// Off a terminal the title is appended after the opening prompt, so every
// field a script already reads by position keeps it, and a conversation the
// harness named nothing ends in an empty field rather than a placeholder.
func TestSessionLinesEndWithTheTitleAfterThePrompt(t *testing.T) {
	last := time.Date(2026, 10, 6, 15, 21, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: last, ID: "b03c41a5", Snippet: "Look at\tthe project", Title: "Parser cleanup"},
		{Harness: "codex", Profile: "q", Project: "y", LastAt: last, ID: "c14d52b6", Snippet: "Look at the project"},
	}
	var out bytes.Buffer
	writeSessionLines(&out, views, true)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	first := strings.Split(lines[0], "\t")
	if len(first) != 10 || first[4] != "b03c41a5" || first[5] != "Look at the project" || first[6] != "Parser cleanup" {
		t.Fatalf("titled line fields = %q", first)
	}
	second := strings.Split(lines[1], "\t")
	if len(second) != 10 || second[5] != "Look at the project" || second[6] != "" {
		t.Fatalf("untitled line fields = %q", second)
	}
}

// Conversations that opened with the same prompt are told apart by how many
// messages each holds, which the table shows in its own column.
func TestSessionTableShowsMessageCounts(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "aaaa1111", Snippet: "same prompt", MessageCount: 343},
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "bbbb2222", Snippet: "same prompt", MessageCount: 3},
	}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if !strings.Contains(lines[0], " MESSAGES ") || !strings.Contains(lines[1], " 343 ") || !strings.Contains(lines[2], " 3 ") {
		t.Fatalf("table = %q, want a MESSAGES column with each count", out.String())
	}
}

// Piped rows end with the message count and a LIVE field in words, appended
// after the title so no earlier field moves, and say "?" rather than closed
// when nobody could be asked which tabs are open.
func TestSessionLinesEndWithMessagesAndLive(t *testing.T) {
	last := time.Date(2026, 10, 6, 15, 21, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: last, ID: "aaaa1111", Snippet: "s", MessageCount: 343, Live: &liveConversation{WindowID: 7, Here: true}},
		{Harness: "claude", Profile: "p", Project: "x", LastAt: last, ID: "bbbb2222", Snippet: "s", MessageCount: 3, Live: &liveConversation{WindowID: 9}},
		{Harness: "codex", Profile: "q", Project: "y", LastAt: last, ID: "cccc3333", Snippet: "s", MessageCount: 0},
	}
	fields := func(liveKnown bool) [][]string {
		var out bytes.Buffer
		writeSessionLines(&out, views, liveKnown)
		var rows [][]string
		for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
			rows = append(rows, strings.Split(line, "\t"))
		}
		return rows
	}
	for i, want := range [][2]string{{"343", "here"}, {"3", "live"}, {"0", ""}} {
		row := fields(true)[i]
		if len(row) != 10 || row[4] != views[i].ID || row[7] != want[0] || row[8] != want[1] {
			t.Fatalf("row %d = %q, want messages %q and live %q", i, row, want[0], want[1])
		}
	}
	for i, row := range fields(false) {
		if row[8] != "?" {
			t.Fatalf("row %d live = %q with nobody asked, want ?", i, row[8])
		}
	}
}

// Runs of the same prompt are told apart by how each ended: the table follows
// the summary with "→" and the ending, holding the summary short enough that
// the ending stays in view, and a piped row appends the ending as its last
// field so every earlier field keeps its position.
func TestSessionListShowsHowEachConversationEnded(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "aaaa1111", Title: "Project improvement ideas for the whole repository", Ending: "Committed 0cf268c3 and installed it."},
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "bbbb2222", Title: "Project improvement ideas for the whole repository", Ending: "All 12 projects now have archived = true."},
		{Harness: "codex", Profile: "q", Project: "y", LastAt: now, ID: "cccc3333", Title: "Never replied"},
	}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 140, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range []string{"→ Committed 0cf268c3 and installed it.", "→ All 12 projects now have archived = true."} {
		if !strings.Contains(lines[i+1], want) || layout.Width(lines[i+1]) > 140 {
			t.Fatalf("row %d = %q; want %q within 140 cells", i, lines[i+1], want)
		}
	}
	if strings.Contains(lines[3], "→") || !strings.HasSuffix(lines[3], "Never replied") {
		t.Fatalf("a row with no ending = %q; want its summary alone", lines[3])
	}

	out.Reset()
	writeSessionLines(&out, views, true)
	rows := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if fields := strings.Split(rows[0], "\t"); len(fields) != 10 || fields[9] != "Committed 0cf268c3 and installed it." {
		t.Fatalf("piped row = %q; want the ending as the 10th field", fields)
	}
	if fields := strings.Split(rows[2], "\t"); fields[9] != "" {
		t.Fatalf("piped row with no ending = %q; want an empty last field", fields)
	}
}
