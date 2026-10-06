package transcript

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

func recaseReaders(byPath map[string][]Message) map[string]Reader {
	return map[string]Reader{harness.Claude: &dispatchReader{harness: harness.Claude, byPath: byPath}}
}

// TestRecasePreviewsRestoresTheCaseASidecarLost pins that a preview read out
// of a lowercased sidecar is replaced by the same line as the transcript
// wrote it, for an any-source and a source-filtered query alike.
func TestRecasePreviewsRestoresTheCaseASidecarLost(t *testing.T) {
	readers := recaseReaders(map[string][]Message{
		"/tmp/a.jsonl": {
			{Role: "user", Text: "Open QuickCapturePanel.swift"},
			{Role: "assistant", Text: "The Swift App now builds"},
		},
	})
	meta := SessionMeta{ID: "a", Harness: harness.Claude, Path: "/tmp/a.jsonl", FileSize: 10}
	for _, tc := range []struct {
		source, found, want string
	}{
		{"", "open quickcapturepanel.swift", "Open QuickCapturePanel.swift"},
		{"harness", "the swift app now builds", "The Swift App now builds"},
	} {
		got := RecasePreviews(readers, []Result{{Meta: meta, Matches: 1, Preview: tc.found}}, Query{Text: "swift", MessageSource: tc.source})
		if got[0].Preview != tc.want {
			t.Errorf("source %q: Preview = %q, want %q", tc.source, got[0].Preview, tc.want)
		}
	}
}

// TestRecasePreviewsKeepsWhatItCannotImprove pins the previews left alone: one
// that already carries case, one whose transcript no longer matches, and one
// with no reader.
func TestRecasePreviewsKeepsWhatItCannotImprove(t *testing.T) {
	readers := recaseReaders(map[string][]Message{
		"/tmp/cased.jsonl": {{Role: "user", Text: "Swift but different"}},
		"/tmp/gone.jsonl":  {{Role: "user", Text: "Nothing here now"}},
	})
	results := []Result{
		{Meta: SessionMeta{ID: "cased", Harness: harness.Claude, Path: "/tmp/cased.jsonl"}, Matches: 1, Preview: "The Swift snippet"},
		{Meta: SessionMeta{ID: "gone", Harness: harness.Claude, Path: "/tmp/gone.jsonl"}, Matches: 1, Preview: "swift was here"},
		{Meta: SessionMeta{ID: "other", Harness: harness.Codex, Path: "/tmp/other.jsonl"}, Matches: 1, Preview: "swift elsewhere"},
	}
	got := RecasePreviews(readers, results, Query{Text: "swift"})
	for i, want := range []string{"The Swift snippet", "swift was here", "swift elsewhere"} {
		if got[i].Preview != want {
			t.Errorf("result %d: Preview = %q, want %q", i, got[i].Preview, want)
		}
	}
	if results[1].Preview != "swift was here" {
		t.Fatal("RecasePreviews modified its input")
	}
}

// TestRecasePreviewsStopsAtItsByteBudget pins that re-reading is spent on
// results in order and stops once the transcripts it would read exceed the
// budget, so a broad search cannot re-read the whole corpus.
func TestRecasePreviewsStopsAtItsByteBudget(t *testing.T) {
	readers := recaseReaders(map[string][]Message{
		"/tmp/first.jsonl":  {{Role: "user", Text: "Swift first"}},
		"/tmp/huge.jsonl":   {{Role: "user", Text: "Swift huge"}},
		"/tmp/second.jsonl": {{Role: "user", Text: "Swift second"}},
	})
	results := []Result{
		{Meta: SessionMeta{ID: "first", Harness: harness.Claude, Path: "/tmp/first.jsonl", FileSize: 1 << 20}, Matches: 1, Preview: "swift first"},
		{Meta: SessionMeta{ID: "huge", Harness: harness.Claude, Path: "/tmp/huge.jsonl", FileSize: previewRereadBudget}, Matches: 1, Preview: "swift huge"},
		{Meta: SessionMeta{ID: "second", Harness: harness.Claude, Path: "/tmp/second.jsonl", FileSize: 1}, Matches: 1, Preview: "swift second"},
	}
	got := RecasePreviews(readers, results, Query{Text: "swift"})
	for i, want := range []string{"Swift first", "swift huge", "swift second"} {
		if got[i].Preview != want {
			t.Errorf("result %d: Preview = %q, want %q", i, got[i].Preview, want)
		}
	}
}
