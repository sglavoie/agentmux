package transcript

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRefresh_AddsNewEntries(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "claude-home", "projects", "-tmp-foo", "sess-1.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "claude-home"),
		}},
		Projects: []config.Session{{
			Title:     "Foo",
			Directory: "/tmp/foo",
		}},
	}

	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(homeDir string, _ DiscoverOptions) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				CWD:          "/tmp/foo",
				StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
				Path:         transcriptPath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				MessageCount: 4,
				Snippet:      "hello",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Claude, "sess-1"))
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if got.Profile != "work" {
		t.Errorf("Profile = %q, want %q", got.Profile, "work")
	}
	if got.Project != "Foo" {
		t.Errorf("Project = %q, want %q", got.Project, "Foo")
	}
	if got.MessageCount != 4 {
		t.Errorf("MessageCount = %d, want 4", got.MessageCount)
	}
	if got.Snippet != "hello" {
		t.Errorf("Snippet = %q, want %q", got.Snippet, "hello")
	}
}

func TestRefresh_SharedHomeProfilesKeepStableDisplayAndFilterMembership(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codex-home")
	transcriptPath := filepath.Join(home, "projects", "-tmp-foo", "sess-shared.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Codex, HomeDir: home},
			{Name: "personal", Harness: harness.Codex, HomeDir: home},
		},
		Projects: []config.Session{{
			Title:     "Foo",
			Directory: "/tmp/foo",
			Profile:   "work",
		}},
	}

	discoverCalls := 0
	fr := &fakeReader{
		harness: harness.Codex,
		discover: func(homeDir string, _ DiscoverOptions) ([]SessionMeta, error) {
			discoverCalls++
			if homeDir != home {
				t.Errorf("Discover homeDir = %q, want %q", homeDir, home)
			}
			return []SessionMeta{{
				ID:           "sess-shared",
				Harness:      harness.Codex,
				CWD:          "/tmp/foo",
				StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
				Path:         transcriptPath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				MessageCount: 4,
				Snippet:      "shared",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Codex: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Codex, "sess-shared"))
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if got.Profile != "work" {
		t.Errorf("Profile = %q, want stable display profile %q", got.Profile, "work")
	}
	if !slices.Equal(got.Profiles, []string{"work", "personal"}) {
		t.Errorf("Profiles = %v, want [work personal]", got.Profiles)
	}
	for _, profile := range []string{"work", "personal"} {
		matches := idx.List(ListFilter{Profile: []string{profile}})
		if len(matches) != 1 || matches[0].ID != "sess-shared" {
			t.Errorf("Profile filter %q returned %+v, want shared session", profile, matches)
		}
	}
	if discoverCalls != 1 {
		t.Errorf("Discover called %d times for one physical transcript home, want 1", discoverCalls)
	}
}

func TestRefresh_DuplicateProjectTitleUsesMatchedDirectoryProfile(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	transcriptPath := filepath.Join(home, "projects", "-tmp-personal-app", "sess-personal.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, HomeDir: home},
			{Name: "personal", Harness: harness.Claude, HomeDir: home},
		},
		Projects: []config.Session{
			{Title: "App", Directory: "/tmp/work-app", Profile: "work"},
			{Title: "App", Directory: "/tmp/personal-app", Profile: "personal"},
		},
	}
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(homeDir string, _ DiscoverOptions) ([]SessionMeta, error) {
			if homeDir != home {
				t.Errorf("Discover homeDir = %q, want %q", homeDir, home)
			}
			return []SessionMeta{{
				ID:           "sess-personal",
				Harness:      harness.Claude,
				CWD:          "/tmp/personal-app",
				StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
				Path:         transcriptPath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				MessageCount: 4,
				Snippet:      "duplicate title",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Claude, "sess-personal"))
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if got.Project != "App" {
		t.Fatalf("Project = %q, want App", got.Project)
	}
	if got.Profile != "personal" {
		t.Fatalf("Profile = %q, want cwd-matched profile personal", got.Profile)
	}
	if !slices.Equal(got.Profiles, []string{"work", "personal"}) {
		t.Fatalf("Profiles = %v, want [work personal]", got.Profiles)
	}
}

func TestRefresh_GitLinkedWorktreeUsesConfiguredParentProjectAndProfile(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	linked := filepath.Join(tmp, "linked-worktree")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	runGit(t, git, repo, "init")
	runGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")

	projectDir := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll projectDir: %v", err)
	}
	runGit(t, git, repo, "worktree", "add", "-b", "feature/refresh-index", linked)
	linkedCWD := filepath.Join(linked, "services", "api", "cmd")
	if err := os.MkdirAll(linkedCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll linkedCWD: %v", err)
	}

	home := filepath.Join(tmp, "claude-home")
	transcriptPath := filepath.Join(home, "projects", "linked-worktree", "sess-linked.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, HomeDir: home},
			{Name: "personal", Harness: harness.Claude, HomeDir: home},
		},
		Projects: []config.Session{{
			Title:     "API",
			Directory: projectDir,
			Profile:   "work",
		}},
	}
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(homeDir string, _ DiscoverOptions) ([]SessionMeta, error) {
			if homeDir != home {
				t.Errorf("Discover homeDir = %q, want %q", homeDir, home)
			}
			return []SessionMeta{{
				ID:           "sess-linked",
				Harness:      harness.Claude,
				CWD:          linkedCWD,
				StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
				Path:         transcriptPath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				MessageCount: 3,
				Snippet:      "linked worktree",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Claude, "sess-linked"))
	if !ok {
		t.Fatalf("entry missing after Refresh")
	}
	if got.Project != "API" {
		t.Fatalf("Project = %q, want API", got.Project)
	}
	if got.Profile != "work" {
		t.Fatalf("Profile = %q, want linked worktree matched profile work", got.Profile)
	}
	if !slices.Equal(got.Profiles, []string{"work", "personal"}) {
		t.Fatalf("Profiles = %v, want [work personal]", got.Profiles)
	}
}

func TestRefresh_CodexSharedHomeRealDataStyleSessionVisible(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codex-home")
	const sessionID = "019ed5d2-642f-7ae0-85e0-9eaf5905b272"
	transcriptPath := filepath.Join(home, "sessions", "2026", "06", "17",
		"rollout-2026-06-17T09-30-00-"+sessionID+".jsonl")
	writeTranscript(t, transcriptPath,
		`{"timestamp":"2026-06-17T13:30:01.000Z","type":"session_meta","payload":{"id":"`+sessionID+`","timestamp":"2026-06-17T13:30:00.000Z","cwd":"/tmp/log-scanner"}}`+"\n"+
			`{"timestamp":"2026-06-17T13:30:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"Find missing scanner logs."}}`+"\n"+
			`{"timestamp":"2026-06-17T13:30:03.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Found the copied fixture session."}]}}`+"\n")

	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Codex, HomeDir: home},
			{Name: "personal", Harness: harness.Codex, HomeDir: home},
		},
		Projects: []config.Session{{
			Title:     "LogScanner",
			Directory: "/tmp/log-scanner",
			Profile:   "personal",
		}},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Codex: NewCodexReader()}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Codex, sessionID))
	if !ok {
		t.Fatalf("real-data-style Codex session %q missing after Refresh", IndexKey(harness.Codex, sessionID))
	}
	if got.Project != "LogScanner" {
		t.Fatalf("Project = %q, want LogScanner", got.Project)
	}
	if got.Profile != "personal" {
		t.Fatalf("Profile = %q, want project default personal", got.Profile)
	}
	if !slices.Equal(got.Profiles, []string{"work", "personal"}) {
		t.Fatalf("Profiles = %v, want [work personal]", got.Profiles)
	}
	for _, profile := range []string{"work", "personal"} {
		matches := idx.List(ListFilter{Profile: []string{profile}})
		if len(matches) != 1 || matches[0].ID != sessionID {
			t.Fatalf("Profile filter %q returned %+v, want copied Codex fixture", profile, matches)
		}
	}
}

// TestRefresh_ExpandsTildeInHomeDir guards the integration between the
// config layer (which stores HomeDir verbatim, with a leading "~") and
// each Reader.Discover (which calls os.ReadDir on the raw value).
// Without tilde expansion in Refresh the per-harness readers silently
// return zero entries because "~/.claude" doesn't exist as a literal path.
func TestRefresh_ExpandsTildeInHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir available: %v", err)
	}
	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: "~/transcript-tilde-test-should-not-exist",
		}},
	}
	var got string
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(homeDir string, _ DiscoverOptions) ([]SessionMeta, error) {
			got = homeDir
			return nil, nil
		},
	}
	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := filepath.Join(home, "transcript-tilde-test-should-not-exist")
	if got != want {
		t.Errorf("Discover received homeDir = %q, want %q", got, want)
	}
}

func TestRefresh_SkipsUnchangedEntries(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	// First discovery: rich metadata. The reader records call count so
	// we can assert it was invoked twice in total (once per Refresh).
	calls := 0
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			calls++
			// On the second call, return identical FileSize/FileModTime
			// but blank MessageCount/Snippet/StartedAt/LastAt — the
			// index should refuse to clobber the cached values.
			if calls == 1 {
				return []SessionMeta{{
					ID:           "sess-1",
					Harness:      harness.Claude,
					CWD:          "/tmp/foo",
					StartedAt:    time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
					LastAt:       time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC),
					Path:         transcriptPath,
					FileSize:     info.Size(),
					FileModTime:  info.ModTime(),
					MessageCount: 4,
					Snippet:      "original",
				}}, nil
			}
			return []SessionMeta{{
				ID:          "sess-1",
				Harness:     harness.Claude,
				CWD:         "/tmp/foo",
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
				// Note: MessageCount/Snippet/StartedAt/LastAt all zero —
				// the unchanged-file path must keep the cached values.
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	got, ok := idx.Get(IndexKey(harness.Claude, "sess-1"))
	if !ok {
		t.Fatalf("entry missing after second Refresh")
	}
	if got.MessageCount != 4 {
		t.Errorf("MessageCount = %d, want 4 (cached)", got.MessageCount)
	}
	if got.Snippet != "original" {
		t.Errorf("Snippet = %q, want %q (cached)", got.Snippet, "original")
	}
	if got.StartedAt.IsZero() {
		t.Errorf("StartedAt cleared; expected cached value to survive")
	}
}

func TestRefresh_RefreshesChangedEntries(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	info1 := writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	calls := 0
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			calls++
			if calls == 1 {
				return []SessionMeta{{
					ID:           "sess-1",
					Harness:      harness.Claude,
					CWD:          "/tmp/foo",
					Path:         transcriptPath,
					FileSize:     info1.Size(),
					FileModTime:  info1.ModTime(),
					MessageCount: 2,
					Snippet:      "first",
				}}, nil
			}
			// File grew — index must adopt the new MessageCount/Snippet.
			info2, err := os.Stat(transcriptPath)
			if err != nil {
				return nil, err
			}
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				CWD:          "/tmp/foo",
				Path:         transcriptPath,
				FileSize:     info2.Size(),
				FileModTime:  info2.ModTime(),
				MessageCount: 7,
				Snippet:      "updated",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// Mutate the file to bump FileSize/FileModTime.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(transcriptPath, []byte("{}\n{}\n{}\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}

	got, _ := idx.Get(IndexKey(harness.Claude, "sess-1"))
	if got.MessageCount != 7 {
		t.Errorf("MessageCount = %d, want 7 (updated)", got.MessageCount)
	}
	if got.Snippet != "updated" {
		t.Errorf("Snippet = %q, want %q", got.Snippet, "updated")
	}
}

func TestRefresh_PreservesHiddenFromIndex(t *testing.T) {
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

func TestRefresh_NoReaderForHarnessSkipped(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()},
			{Name: "extra", Harness: harness.Codex, HomeDir: t.TempDir()},
		},
	}
	claudeFR := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID: "x", Harness: harness.Claude,
			}}, nil
		},
	}
	idx := newTestIndex(t)
	// No codex reader supplied — that profile is silently skipped.
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: claudeFR}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, ok := idx.Get(IndexKey(harness.Claude, "x")); !ok {
		t.Errorf("claude entry missing after partial-reader Refresh")
	}
}

func TestRefresh_FastPathSkipsUnchangedFiles(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-1.jsonl")
	writeTranscript(t, transcriptPath, "{}\n")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	var parsedPaths []string
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			stat, err := os.Stat(transcriptPath)
			if err != nil {
				return nil, err
			}
			if opts.Cached != nil {
				if meta, ok := opts.Cached(transcriptPath, stat.Size(), stat.ModTime()); ok {
					return []SessionMeta{meta}, nil
				}
			}
			parsedPaths = append(parsedPaths, transcriptPath)
			return []SessionMeta{{
				ID:           "sess-1",
				Harness:      harness.Claude,
				CWD:          "/tmp/foo",
				Path:         transcriptPath,
				FileSize:     stat.Size(),
				FileModTime:  stat.ModTime(),
				MessageCount: 4,
				Snippet:      "original",
				SearchText:   "original",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if len(parsedPaths) != 1 {
		t.Fatalf("first Refresh: parsed %d files, want 1", len(parsedPaths))
	}
	parsedPaths = nil
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if len(parsedPaths) != 0 {
		t.Errorf("second Refresh: parsed %d files, want 0 (cache should short-circuit)", len(parsedPaths))
	}
}

func TestRefresh_RepairsMissingSearchSidecar(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "projects", "-tmp-foo", "sess-repair.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	key := IndexKey(harness.Claude, "sess-repair")

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(tmp, "home"),
		}},
	}

	parsed := 0
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			if opts.Cached != nil {
				if meta, ok := opts.Cached(transcriptPath, info.Size(), info.ModTime()); ok {
					return []SessionMeta{meta}, nil
				}
			}
			parsed++
			return []SessionMeta{{
				ID:           "sess-repair",
				Harness:      harness.Claude,
				Path:         transcriptPath,
				FileSize:     info.Size(),
				FileModTime:  info.ModTime(),
				MessageCount: 2,
				SearchText:   "fresh search body",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID:           "sess-repair",
		Harness:      harness.Claude,
		Path:         transcriptPath,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
		MessageCount: 2,
	}))

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if parsed != 1 {
		t.Fatalf("parsed = %d, want 1 to repair missing sidecar", parsed)
	}
	got, err := idx.ReadSearchText(key)
	if err != nil {
		t.Fatalf("ReadSearchText: %v", err)
	}
	if got != "fresh search body" {
		t.Fatalf("sidecar = %q, want %q", got, "fresh search body")
	}
}

func TestRefresh_PassesScanCutoffToDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		days     int
		wantZero bool
	}{
		{name: "positive days", days: 3},
		{name: "negative days unlimited", days: -1, wantZero: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				Defaults: config.Defaults{
					SessionArchive: config.SessionArchiveConfig{ScanMaxAgeDays: tc.days},
				},
				Profiles: []config.Account{{
					Name:    "work",
					Harness: harness.Claude,
					HomeDir: t.TempDir(),
				}},
			}

			var got time.Time
			fr := &fakeReader{
				harness: harness.Claude,
				discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
					got = opts.MinTime
					return nil, nil
				},
			}

			before := time.Now().Add(-time.Duration(tc.days) * 24 * time.Hour)
			if err := newTestIndex(t).Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			after := time.Now().Add(-time.Duration(tc.days) * 24 * time.Hour)

			if tc.wantZero {
				if !got.IsZero() {
					t.Fatalf("MinTime = %v, want zero", got)
				}
				return
			}
			if got.IsZero() {
				t.Fatalf("MinTime is zero, want cutoff")
			}
			if got.Before(before) || got.After(after) {
				t.Fatalf("MinTime = %v, want between %v and %v", got, before, after)
			}
		})
	}
}

func TestRefresh_SkipsFileChangedBeforeMerge(t *testing.T) {
	tmp := t.TempDir()
	transcriptPath := filepath.Join(tmp, "home", "x", "sess-stale.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	key := IndexKey(harness.Claude, "sess-stale")

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
				ID:          "sess-stale",
				Harness:     harness.Claude,
				Path:        transcriptPath,
				FileSize:    info.Size(),
				FileModTime: info.ModTime(),
				SearchText:  "stale body",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- idx.Refresh(cfg, map[string]Reader{harness.Claude: fr})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Discover did not start")
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(transcriptPath, []byte("{}\n{}\n"), 0o644); err != nil {
		close(unblock)
		t.Fatalf("rewrite transcript: %v", err)
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
		t.Fatalf("stale discovery result was merged")
	}
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists: %v", err)
	}
	if exists {
		t.Fatalf("stale discovery search sidecar was written")
	}
}
