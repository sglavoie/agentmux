package transcript

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

func TestSearch_EmptyTextReturnsFilteredMetadata(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Profile: "work", Project: "Foo",
		Snippet: "alpha", LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("codex:b", SessionMeta{
		ID: "b", Harness: harness.Codex, Profile: "work", Project: "Bar",
		Snippet: "beta", LastAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:c", SessionMeta{
		ID: "c", Harness: harness.Claude, Profile: "home", Project: "Foo",
		Snippet:         "gamma",
		LastAt:          time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		HiddenFromIndex: true,
	}))

	results, err := Search(idx, nil, Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len=%d, want 2 (hidden excluded)", len(results))
	}
	if results[0].Meta.ID != "b" {
		t.Errorf("results not sorted LastAt desc; first=%q", results[0].Meta.ID)
	}
	if results[0].Matches != 0 {
		t.Errorf("Matches=%d for text-less query, want 0", results[0].Matches)
	}
	if results[0].Preview != "beta" {
		t.Errorf("Preview=%q, want snippet fallback %q", results[0].Preview, "beta")
	}

	byHarness, err := Search(idx, nil, Query{Harness: []string{harness.Claude}})
	if err != nil {
		t.Fatalf("Search by harness: %v", err)
	}
	if len(byHarness) != 1 || byHarness[0].Meta.ID != "a" {
		t.Errorf("harness filter returned %+v", byHarness)
	}
}

func TestSearch_ProfileFilterMatchesProfilesMembership(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:shared", SessionMeta{
		ID:       "shared",
		Harness:  harness.Claude,
		Profile:  "work",
		Profiles: []string{"work", "personal"},
		Snippet:  "shared home",
		LastAt:   time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:solo", SessionMeta{
		ID:      "solo",
		Harness: harness.Claude,
		Profile: "work",
		Snippet: "work only",
		LastAt:  time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))

	results, err := Search(idx, nil, Query{Profile: []string{"personal"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Meta.ID != "shared" {
		t.Fatalf("profile membership search returned %+v, want shared", results)
	}
	if results[0].Preview != "shared home" {
		t.Fatalf("Preview = %q, want snippet fallback", results[0].Preview)
	}
}

func TestSearch_ProjectFilterMatchesProjectsMembership(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:shared", SessionMeta{
		ID:       "shared",
		Harness:  harness.Claude,
		Project:  "SS",
		Projects: []string{"SS", "streamutil"},
		Snippet:  "shared project",
		LastAt:   time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	}))

	for _, project := range []string{"SS", "streamutil"} {
		results, err := Search(idx, nil, Query{Project: []string{project}})
		if err != nil {
			t.Fatalf("Search project %q: %v", project, err)
		}
		if len(results) != 1 || results[0].Meta.ID != "shared" {
			t.Fatalf("project membership search %q returned %+v, want shared", project, results)
		}
	}

	results, err := Search(idx, nil, Query{Project: []string{"unrelated"}})
	if err != nil {
		t.Fatalf("Search unrelated project: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("unrelated project search returned %+v, want no results", results)
	}
}

func TestSearch_TextFiltersAndCountsMatches(t *testing.T) {
	idx := newTestIndex(t)
	hitKey := "claude:hit"
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
		LastAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:miss", SessionMeta{
		ID: "miss", Harness: harness.Claude, Path: "/tmp/miss.jsonl",
		LastAt: time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))

	readers := map[string]Reader{
		harness.Claude: &fakeReader{
			harness:    harness.Claude,
			readResult: nil, // overridden below per-call via discover/readResult swap
		},
	}

	// Drive different ReadMessages results per session path using a
	// dispatcher reader.
	disp := &dispatchReader{
		harness: harness.Claude,
		byPath: map[string][]Message{
			"/tmp/hit.jsonl": {
				{Role: "user", Text: "first line\nsearchTerm shows up here\nlast"},
				{Role: "assistant", Text: "talked about SearchTerm again"},
			},
			"/tmp/miss.jsonl": {
				{Role: "user", Text: "nothing relevant"},
			},
		},
	}
	readers[harness.Claude] = disp

	results, err := Search(idx, readers, Query{Text: "searchterm"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len=%d, want 1 (only hit session)", len(results))
	}
	r := results[0]
	if r.Meta.ID != "hit" {
		t.Errorf("ID=%q, want hit", r.Meta.ID)
	}
	if r.Matches != 2 {
		t.Errorf("Matches=%d, want 2 (case-insensitive)", r.Matches)
	}
	if !strings.Contains(r.Preview, "searchTerm") {
		t.Errorf("Preview=%q does not contain match line", r.Preview)
	}
	if strings.Contains(r.Preview, "first line") {
		t.Errorf("Preview leaked prior line: %q", r.Preview)
	}
	repaired, err := idx.ReadSearchText(hitKey)
	if err != nil {
		t.Fatalf("ReadSearchText after fallback: %v", err)
	}
	if repaired != "first line\nsearchterm shows up here\nlast\ntalked about searchterm again" {
		t.Errorf("repaired sidecar = %q", repaired)
	}
}

// TestSearch_MultiWordQueryRequiresEveryWord pins the multi-term contract:
// words may sit in different messages and any order, every one must appear
// somewhere in the transcript, and a quoted phrase stays contiguous.
func TestSearch_MultiWordQueryRequiresEveryWord(t *testing.T) {
	idx := newTestIndex(t)
	for _, id := range []string{"both", "one"} {
		must(t, idx.SetEntryForTest("claude:"+id, SessionMeta{
			ID: id, Harness: harness.Claude, Path: "/tmp/" + id + ".jsonl",
			LastAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		}))
	}
	readers := map[string]Reader{harness.Claude: &dispatchReader{
		harness: harness.Claude,
		byPath: map[string][]Message{
			"/tmp/both.jsonl": {
				{Role: "user", Text: "plan the Refactor"},
				{Role: "assistant", Text: "auth first, then auth tests"},
			},
			"/tmp/one.jsonl": {
				{Role: "user", Text: "auth only"},
			},
		},
	}}

	results, err := Search(idx, readers, Query{Text: "auth refactor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Meta.ID != "both" {
		t.Fatalf("results = %+v, want only the transcript holding both words", results)
	}
	if results[0].Matches != 3 {
		t.Errorf("Matches = %d, want 3 (refactor×1 + auth×2)", results[0].Matches)
	}
	if results[0].Preview != "plan the Refactor" {
		t.Errorf("Preview = %q, want the first matching line", results[0].Preview)
	}

	phrase, err := Search(idx, readers, Query{Text: `"auth refactor"`})
	if err != nil {
		t.Fatalf("Search phrase: %v", err)
	}
	if len(phrase) != 0 {
		t.Fatalf("quoted phrase matched non-contiguous words: %+v", phrase)
	}
}

// A multi-word query previews the first line holding every word, even when it
// sits in a later message than the first line holding one of them.
func TestSearch_MultiWordPreviewPrefersALineHoldingEveryWord(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
		LastAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	readers := map[string]Reader{harness.Claude: &dispatchReader{
		harness: harness.Claude,
		byPath: map[string][]Message{
			"/tmp/hit.jsonl": {
				{Role: "user", Text: "Kitty tabs are colored\nthe socket is closed"},
				{Role: "assistant", Text: "first\nthe Kitty socket listens on /tmp"},
			},
		},
	}}

	results, err := Search(idx, readers, Query{Text: "kitty socket"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Preview != "the Kitty socket listens on /tmp" {
		t.Fatalf("results = %+v, want the line holding both words", results)
	}
}

// A result is previewed by a line someone wrote before a line a tool printed,
// though a printed line holding every word still beats a written line holding
// one of them.
func TestSearch_PreviewPrefersWrittenLinesToToolOutput(t *testing.T) {
	written := func(role, text string) Message {
		return Message{Role: role, Text: text, handoffText: &text}
	}
	printed := func(text string) Message {
		empty := ""
		return Message{Role: "user", Text: text, handoffText: &empty}
	}
	mixed := func(tool, prose string) Message {
		return Message{Role: "assistant", Text: tool + "\n" + prose, handoffText: &prose}
	}
	cases := []struct {
		name  string
		query string
		msgs  []Message
		want  string
	}{
		{
			name:  "a later written line beats an earlier printed one",
			query: "daemon",
			msgs: []Message{
				printed("daemon_state_test.go"),
				written("assistant", "first\nThe daemon restarted cleanly."),
			},
			want: "The daemon restarted cleanly.",
		},
		{
			name:  "the written part of a message beats its tool output",
			query: "worktree branch",
			msgs: []Message{
				mixed("5a4c0e83 fix(worktree): Offer near-miss branches", "I checked the branch of each worktree."),
			},
			want: "I checked the branch of each worktree.",
		},
		{
			name:  "a printed line holding every word beats a written one holding one",
			query: "kitty socket",
			msgs: []Message{
				written("user", "Kitty tabs are colored"),
				printed("kitty sockets: unix:/tmp/kitty-690"),
				written("assistant", "the socket is closed"),
			},
			want: "kitty sockets: unix:/tmp/kitty-690",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := newTestIndex(t)
			must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
				ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
				LastAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
			}))
			readers := map[string]Reader{harness.Claude: &dispatchReader{
				harness: harness.Claude,
				byPath:  map[string][]Message{"/tmp/hit.jsonl": tc.msgs},
			}}
			results, err := Search(idx, readers, Query{Text: tc.query})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(results) != 1 || results[0].Preview != tc.want {
				t.Fatalf("results = %+v, want preview %q", results, tc.want)
			}
		})
	}
}

func TestSearch_TextPreviewMapsFoldedUnicodeOffset(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
		LastAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))

	readers := map[string]Reader{
		harness.Claude: &dispatchReader{
			harness: harness.Claude,
			byPath: map[string][]Message{
				"/tmp/hit.jsonl": {
					{Role: "user", Text: "ẞ\nNeedle is on the second line"},
				},
			},
		},
	}

	results, err := Search(idx, readers, Query{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len=%d, want 1", len(results))
	}
	if results[0].Preview != "Needle is on the second line" {
		t.Fatalf("Preview=%q, want second line", results[0].Preview)
	}
}

func TestSearch_NoReaderMeansSessionDropped(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Path: "/tmp/a.jsonl",
		LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	// readers map exists but has no claude entry
	results, err := Search(idx, map[string]Reader{
		harness.Codex: &fakeReader{harness: harness.Codex},
	}, Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when reader missing, got %d", len(results))
	}
}

func TestSearch_ReadErrorDropsSession(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Path: "/tmp/a.jsonl",
		LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	readers := map[string]Reader{
		harness.Claude: &fakeReader{
			harness: harness.Claude,
			readErr: errors.New("boom"),
		},
	}
	results, err := Search(idx, readers, Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on read error, got %d", len(results))
	}
}

func TestSearch_ResultsSortedByLastAtThenStartedAt(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:older", SessionMeta{
		ID: "older", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LastAt:    time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:newer", SessionMeta{
		ID: "newer", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		LastAt:    time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:freshest", SessionMeta{
		ID: "freshest", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		LastAt:    time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
	}))

	results, err := Search(idx, nil, Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len=%d, want 3", len(results))
	}
	wantOrder := []string{"freshest", "newer", "older"}
	for i, want := range wantOrder {
		if results[i].Meta.ID != want {
			t.Errorf("results[%d]=%q, want %q", i, results[i].Meta.ID, want)
		}
	}
}

func TestSearch_NilIndexReturnsError(t *testing.T) {
	if _, err := Search(nil, nil, Query{}); err == nil {
		t.Errorf("expected error for nil index")
	}
}

// dispatchReader returns canned messages keyed by path. Used to drive
// per-session text differences in the same Search call.
type dispatchReader struct {
	harness string
	byPath  map[string][]Message
}

func (d *dispatchReader) Harness() string { return d.harness }
func (d *dispatchReader) Discover(string, DiscoverOptions) ([]SessionMeta, error) {
	return nil, nil
}
func (d *dispatchReader) ReadMessages(path string) ([]Message, error) {
	return d.byPath[path], nil
}

func TestSearch_SinceDropsOlderSessionsBeforeReadingAnything(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:old", SessionMeta{ID: "old", Harness: harness.Claude, Snippet: "old", LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}))
	must(t, idx.SetEntryForTest("claude:new", SessionMeta{ID: "new", Harness: harness.Claude, Snippet: "new", LastAt: time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)}))
	results, err := Search(idx, nil, Query{Since: time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Meta.ID != "new" {
		t.Fatalf("results = %+v, want only the session active after Since", results)
	}
}

// The preview marks a line entered partway with a leading ellipsis; where the
// line is entered is searchquery.MatchLine's to decide and test.
func TestFirstMatchLineMarksALineEnteredNearTheMatch(t *testing.T) {
	line := "d446830 feat(macos): Add the Agent Mux.app skeleton, toolchain, and packaging for release"
	if got := firstMatchLine(line, strings.Index(line, "packaging")); got != "…toolchain, and packaging for release" {
		t.Fatalf("preview = %q, want an ellipsis before the lead-in", got)
	}
	if got := firstMatchLine("Fix the packaging step", 8); got != "Fix the packaging step" {
		t.Fatalf("preview = %q, want a near-start match unmarked", got)
	}
}

type scopedReadRecorder struct {
	dispatchReader
	paths []string
}

func (r *scopedReadRecorder) ReadMessages(path string) ([]Message, error) {
	r.paths = append(r.paths, path)
	return []Message{{Role: "user", Text: "needle"}}, nil
}
func TestSearchConversationScopePrecedesTranscriptReads(t *testing.T) {
	idx := newTestIndex(t)
	for _, entry := range []SessionMeta{
		{ID: "same", Harness: "claude", Path: "wanted", Project: "API"},
		{ID: "same", Harness: "codex", Path: "wrong-harness", Project: "API"},
		{ID: "same-prefix", Harness: "claude", Path: "wrong-id", Project: "API"},
	} {
		must(t, idx.SetEntryForTest(IndexKey(entry.Harness, entry.ID), entry))
	}
	reader := &scopedReadRecorder{}
	result, err := Search(idx, map[string]Reader{"claude": reader, "codex": reader}, Query{Text: "needle", Conversation: "claude:same", Project: []string{"API"}})
	if err != nil || len(result) != 1 || len(reader.paths) != 1 || reader.paths[0] != "wanted" {
		t.Fatalf("result=%v paths=%v err=%v", result, reader.paths, err)
	}
	reader.paths = nil
	_, err = Search(idx, map[string]Reader{"claude": reader}, Query{Text: "needle", Conversation: "claude:same", Project: []string{"Elsewhere"}})
	if err != nil || len(reader.paths) != 0 {
		t.Fatalf("project scope read unrelated transcript: %v %v", reader.paths, err)
	}
}
