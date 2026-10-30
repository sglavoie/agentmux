package transcript

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRebuildKeepsSearchTextForSessionsItCannotReparse(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"removed profile", "discovery failure", "changed source", "partial discovery", "concurrent session"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			cfg, readers, source := filesystemTranscriptFixture(t)
			idx := newTestIndex(t)
			must(t, idx.Refresh(cfg, readers))
			reader := readers[harness.Claude].(*fakeReader)
			discoveryErr := errors.New("transcript home unavailable")
			key, want := IndexKey(harness.Claude, "session"), "fixture needle"
			switch reason {
			case "removed profile":
				cfg.Profiles = nil
			case "discovery failure":
				reader.discoverErr = discoveryErr
			case "changed source":
				// Discovery still returns the old stat; the merge must skip it.
				writeTranscript(t, source, "source changed during discovery\n")
			case "partial discovery":
				other := filepath.Join(filepath.Dir(source), "other.jsonl")
				info := writeTranscript(t, other, "{}\n")
				reader.discover = func(string, DiscoverOptions) ([]SessionMeta, error) {
					return []SessionMeta{{
						ID: "other", Harness: harness.Claude, Path: other,
						FileSize: info.Size(), FileModTime: info.ModTime(),
						MessageCount: 1, SearchText: "other needle",
					}}, discoveryErr
				}
			case "concurrent session":
				key, want = IndexKey(harness.Claude, "concurrent"), "concurrent needle"
				discover := reader.discover
				reader.discover = func(home string, opts DiscoverOptions) ([]SessionMeta, error) {
					// Another index writer saves a session after the rebuild's
					// snapshot, while discovery holds no advisory lock.
					other, err := LoadIndexAt(idx.Path())
					must(t, err)
					must(t, other.SetEntryForTest(key, SessionMeta{
						ID: "concurrent", Harness: harness.Claude,
						MessageCount: 1, SearchText: want,
					}))
					return discover(home, opts)
				}
			}

			_, err := Rebuild(idx.Path(), cfg, readers, true)
			if reason == "discovery failure" || reason == "partial discovery" {
				if !errors.Is(err, discoveryErr) {
					t.Fatalf("Rebuild error = %v, want discovery error", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got, err := idx.ReadSearchText(key); err != nil || got != want {
				t.Fatalf("rebuild discarded undiscovered session's search text: %q, %v", got, err)
			}
			status, err := Status(idx.Path())
			must(t, err)
			if status.MissingSidecars != 0 {
				t.Fatalf("rebuild left sessions without search text: %v", status.MissingSidecarKeys)
			}
			if reason == "partial discovery" {
				if got, err := idx.ReadSearchText(IndexKey(harness.Claude, "other")); err != nil || got != "other needle" {
					t.Fatalf("partial discovery was not merged: %q, %v", got, err)
				}
			}
		})
	}
}
