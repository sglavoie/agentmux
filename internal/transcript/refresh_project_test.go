package transcript

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestRefresh_SharedHomeProfilesKeepStableDisplayAndFilterMembership(t *testing.T) {
	t.Parallel()

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

// One home spelled two ways (here with and without a trailing slash, and
// through a symlink) is still one home: its transcripts belong to every
// profile naming it, and an unchanged refresh writes nothing.
func TestRefresh_SharedHomeSpelledTwoWaysIsOneHome(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	link := filepath.Join(tmp, "claude-link")
	transcriptPath := filepath.Join(home, "projects", "-tmp-x", "sess-spelled.jsonl")
	writeTranscript(t, transcriptPath,
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp/x","entrypoint":"cli","message":{"role":"user","content":"hello world"}}`+"\n")
	if err := os.Symlink(home, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, HomeDir: home},
			{Name: "personal", Harness: harness.Claude, HomeDir: home + "/"},
			{Name: "linked", Harness: harness.Claude, HomeDir: link},
		},
	}
	readers := map[string]Reader{harness.Claude: NewClaudeReader()}
	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, readers); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	saves := 0
	saveLockedHookForTest = func() { saves++ }
	t.Cleanup(func() { saveLockedHookForTest = nil })
	for range 2 {
		if err := idx.Refresh(cfg, readers); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
	if saves != 0 {
		t.Errorf("unchanged refreshes saved the cache %d times, want 0", saves)
	}
	got, ok := idx.Get(IndexKey(harness.Claude, "sess-spelled"))
	if !ok {
		t.Fatal("entry missing after Refresh")
	}
	if want := []string{"work", "personal", "linked"}; !slices.Equal(got.Profiles, want) {
		t.Errorf("Profiles = %v, want %v", got.Profiles, want)
	}
	for _, profile := range []string{"work", "personal", "linked"} {
		if matches := idx.List(ListFilter{Profile: []string{profile}}); len(matches) != 1 {
			t.Errorf("Profile filter %q returned %d sessions, want 1", profile, len(matches))
		}
	}
}

func TestRefresh_DuplicateDirectoryProjectsKeepOrderedMembership(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	projectDir := filepath.Join(tmp, "shared-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}
	transcriptPath := filepath.Join(home, "projects", "shared", "sess-shared-project.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	reportedModTime := info.ModTime().In(time.FixedZone("reader-zone", 60*60))

	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: harness.Claude, HomeDir: home},
			{Name: "personal", Harness: harness.Claude, HomeDir: home},
		},
		Projects: []config.Session{
			{Title: "SS", Directory: projectDir, Profile: "work"},
			{Title: "streamutil", Directory: projectDir, Profile: "personal"},
		},
	}
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(string, DiscoverOptions) ([]SessionMeta, error) {
			return []SessionMeta{{
				ID: "sess-shared-project", Harness: harness.Claude, CWD: projectDir,
				Path: transcriptPath, FileSize: info.Size(), FileModTime: reportedModTime,
				MessageCount: 1, Snippet: "shared project",
			}}, nil
		},
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	key := IndexKey(harness.Claude, "sess-shared-project")
	got, ok := idx.Get(key)
	if !ok {
		t.Fatal("shared-project entry missing after Refresh")
	}
	if got.Project != "SS" || !slices.Equal(got.Projects, []string{"SS", "streamutil"}) {
		t.Fatalf("project classification = %q %#v, want SS [SS streamutil]", got.Project, got.Projects)
	}
	if got.Profile != "work" {
		t.Fatalf("display Profile = %q, want first match profile work", got.Profile)
	}
	for _, project := range []string{"SS", "streamutil"} {
		matches := idx.List(ListFilter{Project: []string{project}})
		if len(matches) != 1 || matches[0].ID != got.ID {
			t.Fatalf("project filter %q returned %+v, want shared session", project, matches)
		}
	}
	if matches := idx.List(ListFilter{Project: []string{"unrelated"}}); len(matches) != 0 {
		t.Fatalf("unrelated project filter returned %+v", matches)
	}

	cfg.Projects[0], cfg.Projects[1] = cfg.Projects[1], cfg.Projects[0]
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh after configuration reorder: %v", err)
	}
	got, _ = idx.Get(key)
	if got.Project != "streamutil" || !slices.Equal(got.Projects, []string{"streamutil", "SS"}) {
		t.Fatalf("reordered classification = %q %#v, want streamutil [streamutil SS]", got.Project, got.Projects)
	}
	if got.Profile != "personal" {
		t.Fatalf("reordered display Profile = %q, want first match profile personal", got.Profile)
	}

	saves := 0
	saveLockedHookForTest = func() { saves++ }
	t.Cleanup(func() { saveLockedHookForTest = nil })
	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("unchanged Refresh: %v", err)
	}
	if saves != 0 {
		t.Fatalf("unchanged project classification persisted cache %d times, want 0", saves)
	}
}

func TestRefresh_CachedEntryWithoutCWDKeepsProjectMetadata(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	home := filepath.Join(tmp, "claude-home")
	transcriptPath := filepath.Join(home, "projects", "legacy", "sess-no-cwd.jsonl")
	info := writeTranscript(t, transcriptPath, "{}\n")
	key := IndexKey(harness.Claude, "sess-no-cwd")

	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID: "sess-no-cwd", Harness: harness.Claude, Profile: "work",
		Project: "Legacy", Projects: []string{"Legacy", "Legacy Alias"},
		Path: transcriptPath, FileSize: info.Size(), FileModTime: info.ModTime(),
		ReaderVersion: claudeReaderVersion,
	}))
	fr := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			cached, ok := opts.Cached(transcriptPath, info.Size(), info.ModTime())
			if !ok {
				t.Fatal("cached entry without CWD was reparsed")
			}
			return []SessionMeta{cached}, nil
		},
	}
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}},
		Projects: []config.Session{{Title: "Other", Directory: filepath.Join(tmp, "other")}},
	}

	if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: fr}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, ok := idx.Get(key)
	if !ok || got.Project != "Legacy" || !slices.Equal(got.Projects, []string{"Legacy", "Legacy Alias"}) {
		t.Fatalf("cached no-CWD project metadata = %+v found=%t", got, ok)
	}
}

func TestRefresh_DuplicateProjectTitleUsesMatchedDirectoryProfile(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
		Projects: []config.Session{
			{Title: "API", Directory: projectDir, Profile: "work"},
			{Title: "API-alias", Directory: projectDir, Profile: "personal"},
		},
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
	if !slices.Equal(got.Projects, []string{"API", "API-alias"}) {
		t.Fatalf("Projects = %v, want [API API-alias]", got.Projects)
	}
	if got.Profile != "work" {
		t.Fatalf("Profile = %q, want linked worktree matched profile work", got.Profile)
	}
	if !slices.Equal(got.Profiles, []string{"work", "personal"}) {
		t.Fatalf("Profiles = %v, want [work personal]", got.Profiles)
	}
}

func TestRefresh_CodexSharedHomeRealDataStyleSessionVisible(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	return git
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agentmux Test",
		"GIT_AUTHOR_EMAIL=agentmux-test@example.invalid",
		"GIT_COMMITTER_NAME=Agentmux Test",
		"GIT_COMMITTER_EMAIL=agentmux-test@example.invalid",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
