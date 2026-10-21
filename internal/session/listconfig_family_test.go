package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
)

// newFamilyRepo builds a real repository with one linked worktree and returns
// both checkout roots.
func newFamilyRepo(t *testing.T) (repo, linked string) {
	t.Helper()
	git := requireSessionGit(t)
	root := t.TempDir()
	repo = filepath.Join(root, "family repo")
	linked = filepath.Join(root, "family worktree")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir repo: %v", err)
	}
	runSessionGit(t, git, repo, "init")
	runSessionGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runSessionGit(t, git, repo, "worktree", "add", "-b", "family/worktree", linked)
	return repo, linked
}

// TestListWithConfig_StampsOneRepositoryFamilyAcrossCheckouts covers both
// conversion paths at once: tab 70 is an ordinary managed window carrying amx
// user vars, tab 72 is recovered from its harness CWD. The two checkouts are
// configured as separate projects and still belong to one repository.
func TestListWithConfig_StampsOneRepositoryFamilyAcrossCheckouts(t *testing.T) {
	t.Parallel()

	repo, linked := newFamilyRepo(t)

	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{
			{
				ID:       70,
				IsActive: true,
				Title:    "App",
				Windows: []kitty.Window{{
					ID:       71,
					IsActive: true,
					CWD:      repo,
					UserVars: kitty.UserVars{
						Profile:      "work",
						Harness:      "claude",
						Project:      "App",
						AMXDirectory: repo,
						LaunchID:     "launch-70",
					},
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"claude"},
						CWD:     repo,
					}},
				}},
			},
			{
				ID:    72,
				Title: "App Security",
				Windows: []kitty.Window{{
					ID:  73,
					CWD: linked,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"claude"},
						CWD:     linked,
					}},
				}},
			},
		},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{
			{Title: "App", Account: "work", Directory: repo},
			{Title: "App Security", Account: "work", Directory: linked},
		},
	}

	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(sessions), sessions)
	}

	main, worktree := sessions[0], sessions[1]
	if main.Project != "App" || worktree.Project != "App Security" {
		t.Fatalf("session identities = %q/%q, want App/App Security", main.Project, worktree.Project)
	}
	if main.FamilyKey == "" {
		t.Fatal("main checkout resolved no family key")
	}
	if worktree.FamilyKey != main.FamilyKey {
		t.Errorf("linked worktree family key = %q, want the main checkout key %q", worktree.FamilyKey, main.FamilyKey)
	}
	wantRoot := projectresolve.AbsPath(repo)
	for _, rs := range sessions {
		if !rs.FamilyIsRepository {
			t.Errorf("%s FamilyIsRepository = false, want true", rs.Project)
		}
		// Both rows carry the repository's label, not their own project title,
		// so the two checkouts render as one family.
		if rs.FamilyLabel != "App" {
			t.Errorf("%s FamilyLabel = %q, want App", rs.Project, rs.FamilyLabel)
		}
		if rs.FamilyPrimaryRoot != wantRoot {
			t.Errorf("%s FamilyPrimaryRoot = %q, want %q", rs.Project, rs.FamilyPrimaryRoot, wantRoot)
		}
		if rs.FamilyLabel == rs.FamilyKey {
			t.Errorf("%s renders the internal family key %q as its label", rs.Project, rs.FamilyKey)
		}
	}
}

func TestListWithConfig_AmbiguousProjectLeavesFamilyEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       74,
			IsActive: true,
			Title:    "Notes",
			Windows: []kitty.Window{{
				ID:       75,
				IsActive: true,
				CWD:      dir,
				UserVars: kitty.UserVars{
					Profile:      "claude-work",
					Harness:      "claude",
					Project:      "Notes",
					AMXDirectory: dir,
					LaunchID:     "launch-74",
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"claude"},
					CWD:     dir,
				}},
			}},
		}},
	}})

	// Two configured identities share the directory and it is not a repository,
	// so there is no evidence that says which family the row belongs to.
	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "claude-work", Harness: "claude"},
			{Name: "codex-work", Harness: "codex"},
		},
		Sessions: []config.Session{
			{Title: "Notes", Account: "claude-work", Directory: dir},
			{Title: "Journal", Account: "codex-work", Directory: dir},
		},
	}

	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(sessions), sessions)
	}
	assertNoFamily(t, sessions[0])
}

func TestListWithConfig_UnconfiguredDirectoryLeavesFamilyEmpty(t *testing.T) {
	t.Parallel()

	configured := t.TempDir()
	elsewhere := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       76,
			IsActive: true,
			Title:    "scratch",
			Windows: []kitty.Window{{
				ID:       77,
				IsActive: true,
				CWD:      elsewhere,
				UserVars: kitty.UserVars{
					Profile:      "work",
					Harness:      "claude",
					AMXDirectory: elsewhere,
					LaunchID:     "launch-76",
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"claude"},
					CWD:     elsewhere,
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "App", Account: "work", Directory: configured}},
	}

	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(sessions), sessions)
	}
	assertNoFamily(t, sessions[0])
}

func TestListFromOSWindows_WithoutConfigLeavesFamilyEmpty(t *testing.T) {
	t.Parallel()

	repo, _ := newFamilyRepo(t)
	sessions := session.ListFromOSWindows([]kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       78,
			IsActive: true,
			Title:    "App",
			Windows: []kitty.Window{{
				ID:       79,
				IsActive: true,
				CWD:      repo,
				UserVars: kitty.UserVars{
					Profile:      "work",
					Harness:      "claude",
					Project:      "App",
					AMXDirectory: repo,
					LaunchID:     "launch-78",
				},
			}},
		}},
	}})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(sessions), sessions)
	}
	// Family identity is a configured-project fact. Discovery with no config
	// has nothing to resolve against and must not probe Git to invent one.
	assertNoFamily(t, sessions[0])
}

// TestListWithConfig_ResolvesFamilyWithoutRepeatingTheGitProbe puts a counting
// shim in front of git so the whole discovery pass can be measured. Family
// identity must come out of the probe the resolver already runs once per
// configured project, so the count cannot grow with the number of sessions.
func TestListWithConfig_ResolvesFamilyWithoutRepeatingTheGitProbe(t *testing.T) {
	git := requireSessionGit(t)
	repo, linked := newFamilyRepo(t)

	shimDir := t.TempDir()
	counter := filepath.Join(shimDir, "invocations")
	shim := "#!/bin/sh\nprintf 'x' >> '" + counter + "'\nexec '" + git + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", shimDir)

	tabs := make([]kitty.Tab, 0, 4)
	for i, dir := range []string{repo, linked, repo, linked} {
		tabs = append(tabs, kitty.Tab{
			ID:       80 + i,
			IsActive: i == 0,
			Title:    "App",
			Windows: []kitty.Window{{
				ID:       180 + i,
				IsActive: true,
				CWD:      dir,
				UserVars: kitty.UserVars{
					Profile:      "work",
					Harness:      "claude",
					Project:      "App",
					AMXDirectory: dir,
					LaunchID:     "launch-shim",
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"claude"},
					CWD:     dir,
				}},
			}},
		})
	}
	client := newClientFromWindows(t, []kitty.OSWindow{{ID: 1, IsActive: true, Tabs: tabs}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "App", Account: "work", Directory: repo}},
	}

	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != len(tabs) {
		t.Fatalf("expected %d sessions, got %d", len(tabs), len(sessions))
	}
	for _, rs := range sessions {
		if rs.FamilyKey != sessions[0].FamilyKey || rs.FamilyKey == "" {
			t.Fatalf("family keys diverged across checkouts: %+v", sessions)
		}
	}

	invocations, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read git invocation counter: %v", err)
	}
	// One configured project, one probe: `rev-parse` and `worktree list`.
	if len(invocations) != 2 {
		t.Fatalf("git ran %d times for one configured project, want 2", len(invocations))
	}
}

func assertNoFamily(t *testing.T, rs session.RunningSession) {
	t.Helper()
	if rs.FamilyKey != "" || rs.FamilyLabel != "" || rs.FamilyIsRepository || rs.FamilyPrimaryRoot != "" {
		t.Fatalf("expected no family metadata, got key=%q label=%q repo=%v root=%q",
			rs.FamilyKey, rs.FamilyLabel, rs.FamilyIsRepository, rs.FamilyPrimaryRoot)
	}
}
