package transcript

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRefresh_PreservesHiddenFromIndex(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "x", "sess-h.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

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
				ID:          "sess-h",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-h")
	if err := idx.SetHidden(key, true); err != nil {
		t.Fatalf("SetHidden: %v", err)
	}

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	got, _ := idx.Get(key)
	if !got.HiddenFromIndex {
		t.Errorf("HiddenFromIndex cleared by Refresh; expected to survive")
	}
}

func TestRefresh_ReleasesLockDuringDiscovery(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "x", "sess-lock.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	key := IndexKey(harness.Claude, "sess-lock")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	started := make(chan struct{})
	unblock := make(chan struct{})
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			close(started)
			<-unblock
			return []SessionMeta{{
				ID:          "sess-lock",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
			}}, nil
		},
	}

	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:          "sess-lock",
		Harness:     harness.Claude,
		Path:        transcriptPath,
		FileSize:    info.Size(),
		FileModTime: info.ModTime(),
	}))

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- idx.Refresh(cfg, map[string]Reader{harness.Claude: fr})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Discover did not start")
	}

	lenDone := make(chan int, 1)
	go func() { lenDone <- idx.Len() }()
	select {
	case got := <-lenDone:
		if got != 1 {
			t.Fatalf("Len during discovery = %d, want 1", got)
		}
	case <-time.After(time.Second):
		close(unblock)
		t.Fatal("Len blocked while Refresh was in Discover")
	}

	close(unblock)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Refresh did not finish")
	}
}

func TestRefresh_PreservesHideDuringDiscovery(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "x", "sess-hide.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	key := IndexKey(harness.Claude, "sess-hide")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	started := make(chan struct{})
	unblock := make(chan struct{})
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			close(started)
			<-unblock
			return []SessionMeta{{
				ID:          "sess-hide",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
				SearchText:  "fresh hidden body",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-hide",
		Harness:      harness.Claude,
		Path:         transcriptPath,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
		MessageCount: 1,
	}))
	must(t, idx.WriteSearchText(key, "old body"))

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- idx.Refresh(cfg, map[string]Reader{harness.Claude: fr})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Discover did not start")
	}

	hideDone := make(chan error, 1)
	go func() { hideDone <- idx.SetHidden(key, true) }()
	select {
	case err := <-hideDone:
		if err != nil {
			t.Fatalf("SetHidden during Refresh: %v", err)
		}
	case <-time.After(time.Second):
		close(unblock)
		t.Fatal("SetHidden blocked while Refresh was in Discover")
	}

	close(unblock)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Refresh did not finish")
	}

	got, ok := idx.Get(key)
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if !got.HiddenFromIndex {
		t.Fatalf("HiddenFromIndex = false, want true")
	}
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists: %v", err)
	}
	if exists {
		t.Fatalf("hidden entry search sidecar was recreated")
	}
}

func TestRefresh_PreservesDeleteDuringDiscovery(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "x", "sess-delete.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	key := IndexKey(harness.Claude, "sess-delete")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	started := make(chan struct{})
	unblock := make(chan struct{})
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			close(started)
			<-unblock
			return []SessionMeta{{
				ID:          "sess-delete",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
				SearchText:  "fresh deleted body",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-delete",
		Harness:      harness.Claude,
		Path:         transcriptPath,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
		MessageCount: 1,
	}))
	must(t, idx.WriteSearchText(key, "old body"))

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- idx.Refresh(cfg, map[string]Reader{harness.Claude: fr})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Discover did not start")
	}

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- idx.Delete(key, false) }()
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("Delete during Refresh: %v", err)
		}
	case <-time.After(time.Second):
		close(unblock)
		t.Fatal("Delete blocked while Refresh was in Discover")
	}

	close(unblock)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Refresh did not finish")
	}

	if _, ok := idx.Get(key); ok {
		t.Fatalf("deleted entry was resurrected by Refresh")
	}
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists: %v", err)
	}
	if exists {
		t.Fatalf("deleted entry search sidecar was recreated")
	}
}
