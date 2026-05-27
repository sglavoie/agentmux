package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRefresh_WritesSidecarAndClearsTransientSearchText(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	stat := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				Path:         transcriptPath,
				FileSize:     stat.Size(),
				FileModTime:  stat.ModTime(),
				MessageCount: 2,
				SearchText:   "needle body",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-1")
	meta, ok := idx.Get(key)
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if meta.SearchText != "" {
		t.Fatalf("SearchText retained in index entry: %q", meta.SearchText)
	}
	sidecar, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if sidecar != "needle body" {
		t.Fatalf("sidecar = %q", sidecar)
	}
}

func TestRefresh_MissingSidecarInvalidatesUnchangedCache(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	stat := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}
	key := IndexKey(harness.Claude, "sess-1")
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-1",
		Harness:      harness.Claude,
		Path:         transcriptPath,
		FileSize:     stat.Size(),
		FileModTime:  stat.ModTime(),
		MessageCount: 2,
		Snippet:      "cached",
	}))

	parses := 0
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			if opts.Cached != nil {
				if meta, ok := opts.Cached(transcriptPath, stat.Size(), stat.ModTime()); ok {
					return []SessionMeta{meta}, nil
				}
			}
			parses++
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				Path:         transcriptPath,
				FileSize:     stat.Size(),
				FileModTime:  stat.ModTime(),
				MessageCount: 3,
				Snippet:      "reparsed",
				SearchText:   "reparsed needle",
			}}, nil
		},
	}

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if parses != 1 {
		t.Fatalf("parses=%d, want 1 because sidecar was missing", parses)
	}
	meta, _ := idx.Get(key)
	if meta.SearchText != "" {
		t.Fatalf("SearchText retained after repair: %q", meta.SearchText)
	}
	sidecar, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if sidecar != "reparsed needle" {
		t.Fatalf("sidecar = %q", sidecar)
	}
}

func TestRefresh_PreservesEntriesBeyondCutoff(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-old.jsonl")
	writeTranscript(t, transcriptPath, "{}\n")

	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(transcriptPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	stat, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.Defaults{
			SessionArchive: config.SessionArchiveConfig{ScanMaxAgeDays: 30},
		},
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	var sawPaths []string
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			if !opts.MinTime.IsZero() && stat.ModTime().Before(opts.MinTime) {
				return nil, nil
			}
			sawPaths = append(sawPaths, transcriptPath)
			return []SessionMeta{{
				ID:          "sess-old",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    stat.Size(),
				FileModTime: stat.ModTime(),
			}}, nil
		},
	}

	idx := newTestIndex(t)
	key := IndexKey(harness.Claude, "sess-old")
	primed := SessionMeta{
		ID:           "sess-old",
		Harness:      harness.Claude,
		Path:         transcriptPath,
		FileSize:     stat.Size(),
		FileModTime:  stat.ModTime(),
		MessageCount: 99,
		Snippet:      "ancient",
		StartedAt:    oldTime,
		LastAt:       oldTime,
	}
	must(t, idx.SetEntryForTest(key, primed))

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(sawPaths) != 0 {
		t.Errorf("reader saw paths beyond cutoff: %v", sawPaths)
	}
	got, ok := idx.Get(key)
	if !ok {
		t.Fatalf("entry beyond cutoff was dropped by Refresh")
	}
	if got.MessageCount != 99 {
		t.Errorf("MessageCount = %d, want 99 (untouched)", got.MessageCount)
	}
	if got.Snippet != "ancient" {
		t.Errorf("Snippet = %q, want %q (untouched)", got.Snippet, "ancient")
	}
}

func TestRefresh_RescansFilesWithinCutoff(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-recent.jsonl")
	writeTranscript(t, transcriptPath, "{}\n")

	recentTime := time.Now().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(transcriptPath, recentTime, recentTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	stat, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.Defaults{
			SessionArchive: config.SessionArchiveConfig{ScanMaxAgeDays: 30},
		},
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	var sawPaths []string
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			if !opts.MinTime.IsZero() && stat.ModTime().Before(opts.MinTime) {
				return nil, nil
			}
			sawPaths = append(sawPaths, transcriptPath)
			return []SessionMeta{{
				ID:          "sess-recent",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    stat.Size(),
				FileModTime: stat.ModTime(),
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(sawPaths) != 1 {
		t.Errorf("reader did not see recent file within cutoff: paths=%v", sawPaths)
	}
	if _, ok := idx.Get(IndexKey(harness.Claude, "sess-recent")); !ok {
		t.Errorf("recent entry not added to index")
	}
}
