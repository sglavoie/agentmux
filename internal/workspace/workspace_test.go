package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	searchsvc "github.com/sglavoie/agentmux/internal/search"
)

func TestBuildGroupsProfilesAndPersistsGeneratedWorkspaceID(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	service := New(store)
	directory := t.TempDir()
	cfg := workspaceConfig(
		config.Session{Title: "API", Directory: directory, Profile: "work"},
		config.Session{Title: "API personal", Directory: directory, Profile: "personal"},
	)
	cfg.Profiles[1].Harness = "codex"

	workspaces, err := service.Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("Build returned %d workspaces, want 1: %#v", len(workspaces), workspaces)
	}
	workspace := workspaces[0]
	if !strings.HasPrefix(workspace.ID, "ws-") {
		t.Errorf("generated workspace ID = %q, want ws-*", workspace.ID)
	}
	if workspace.DefaultLaunch.Title != "API" {
		t.Errorf("default launch = %q, want first config entry", workspace.DefaultLaunch.Title)
	}
	if len(workspace.LaunchVariants) != 1 || workspace.LaunchVariants[0].Title != "API personal" {
		t.Errorf("launch variants = %#v, want second config entry", workspace.LaunchVariants)
	}
	var searchTitle, searchContent, searchWorkspace, searchHarness, searchProfile string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT title, normalized_content, workspace_id, harness, profile_name
		FROM search_documents WHERE entity_type = 'project' AND entity_id = ?
	`, workspace.ID).Scan(&searchTitle, &searchContent, &searchWorkspace, &searchHarness, &searchProfile); err != nil {
		t.Fatalf("project search document: %v", err)
	}
	if searchTitle != "API" || searchWorkspace != workspace.ID || searchHarness != "claude" || searchProfile != "work" || !strings.Contains(searchContent, "API personal") {
		t.Fatalf("project search document = title %q workspace %q harness %q profile %q content %q", searchTitle, searchWorkspace, searchHarness, searchProfile, searchContent)
	}
	searchService, err := searchsvc.New(store)
	if err != nil {
		t.Fatalf("new search service: %v", err)
	}
	variantResults, err := searchService.Search(ctx, searchsvc.Query{
		Text: "API", Types: []searchsvc.EntityType{searchsvc.EntityProject},
		Workspaces: []string{workspace.ID}, Harnesses: []string{"CODEX"},
		Profiles: []string{"PERSONAL"}, To: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("search non-default launch variant: %v", err)
	}
	if len(variantResults) != 1 || variantResults[0].EntityID != workspace.ID {
		t.Fatalf("non-default launch variant results = %#v", variantResults)
	}
	result := variantResults[0]
	if result.Harness != "claude" || result.Profile != "work" {
		t.Fatalf("default scalar fields changed: harness %q profile %q", result.Harness, result.Profile)
	}
	if !slices.Equal(result.Harnesses, []string{"claude", "codex"}) || !slices.Equal(result.Profiles, []string{"work", "personal"}) {
		t.Fatalf("variant memberships = harnesses %v profiles %v", result.Harnesses, result.Profiles)
	}

	// Config titles can change without moving a legacy workspace to a fresh
	// generated ID.
	cfg.Projects[0].Title = "API renamed"
	cfg.Projects[1].Title = "API personal renamed"
	workspaces, err = service.Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build after rename: %v", err)
	}
	if got := workspaces[0].ID; got != workspace.ID {
		t.Errorf("workspace ID after rename = %q, want persisted %q", got, workspace.ID)
	}

	byID, err := service.Resolve(ctx, cfg, workspace.ID)
	if err != nil || byID.ID != workspace.ID {
		t.Fatalf("Resolve generated ID = %#v, %v", byID, err)
	}
	byTitle, err := service.Resolve(ctx, cfg, "API personal renamed")
	if err != nil || byTitle.ID != workspace.ID {
		t.Fatalf("Resolve title alias = %#v, %v", byTitle, err)
	}
	byPath, err := service.Resolve(ctx, cfg, directory)
	if err != nil || byPath.ID != workspace.ID {
		t.Fatalf("Resolve canonical path = %#v, %v", byPath, err)
	}
}

func TestSynchronizeReplacesWorkspaceSearchMemberships(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	service := New(store)
	directory := t.TempDir()
	work := config.Session{ID: "api", Title: "API", Directory: directory, Profile: "work"}
	personal := config.Session{Title: "API personal", Directory: directory, Profile: "personal"}
	cfg := workspaceConfig(work, personal)
	cfg.Profiles[1].Harness = "codex"

	if _, err := service.Synchronize(ctx, cfg); err != nil {
		t.Fatalf("initial Synchronize: %v", err)
	}
	cfg.Projects = []config.Session{work}
	if _, err := service.Synchronize(ctx, cfg); err != nil {
		t.Fatalf("reduced Synchronize: %v", err)
	}

	var profileCount, harnessCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM workspace_profile_memberships WHERE workspace_id = 'api'`).Scan(&profileCount); err != nil {
		t.Fatalf("count profile memberships: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM workspace_harness_memberships WHERE workspace_id = 'api'`).Scan(&harnessCount); err != nil {
		t.Fatalf("count harness memberships: %v", err)
	}
	if profileCount != 1 || harnessCount != 1 {
		t.Fatalf("replaced membership counts = profiles %d harnesses %d", profileCount, harnessCount)
	}
	searchService, _ := searchsvc.New(store)
	for name, query := range map[string]searchsvc.Query{
		"removed profile": {Text: "API", Types: []searchsvc.EntityType{searchsvc.EntityProject}, Profiles: []string{"personal"}},
		"removed harness": {Text: "API", Types: []searchsvc.EntityType{searchsvc.EntityProject}, Harnesses: []string{"codex"}},
	} {
		results, err := searchService.Search(ctx, query)
		if err != nil {
			t.Fatalf("%s Search: %v", name, err)
		}
		if len(results) != 0 {
			t.Fatalf("%s still matched: %#v", name, results)
		}
	}
}

func TestResolveAmbiguousTitleReturnsCandidates(t *testing.T) {
	ctx := context.Background()
	service := New(datastore.OpenTest(t))
	cfg := workspaceConfig(
		config.Session{ID: "one", Title: "API", Directory: t.TempDir(), Profile: "work"},
		config.Session{ID: "two", Title: "API", Directory: t.TempDir(), Profile: "personal"},
	)

	_, err := service.Resolve(ctx, cfg, "API")
	var ambiguous *AmbiguousSelectorError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("Resolve ambiguous title error = %v, want AmbiguousSelectorError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Errorf("candidate count = %d, want 2: %#v", len(ambiguous.Candidates), ambiguous.Candidates)
	}
}

func TestBuildRemovesSearchDocumentsForUnconfiguredWorkspaces(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	service := New(store)
	first := config.Session{ID: "first", Title: "First", Directory: t.TempDir(), Profile: "work"}
	second := config.Session{ID: "second", Title: "Second", Directory: t.TempDir(), Profile: "personal"}
	if _, err := service.Build(ctx, workspaceConfig(first, second)); err != nil {
		t.Fatalf("initial Build: %v", err)
	}
	if _, err := service.Build(ctx, workspaceConfig(first)); err != nil {
		t.Fatalf("reduced Build: %v", err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM search_documents WHERE entity_type = 'project'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("project documents after removal = %d, want 1", count)
	}
	if _, err := service.Build(ctx, &config.Config{}); err != nil {
		t.Fatalf("empty Build: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM search_documents WHERE entity_type = 'project'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("project documents after empty config = %d, want 0", count)
	}
}

func TestGitWorktreesShareWorkspaceAndCWDMapsChildContext(t *testing.T) {
	git := requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	primary := filepath.Join(root, "repo")
	linked := filepath.Join(root, "repo-feature")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatalf("Mkdir primary: %v", err)
	}
	runGit(t, git, primary, "init")
	runGit(t, git, primary, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runGit(t, git, primary, "worktree", "add", "-b", "feature/workspace", linked)
	linkedCWD := filepath.Join(linked, "services", "api")
	if err := os.MkdirAll(linkedCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll linked CWD: %v", err)
	}

	service := New(datastore.OpenTest(t))
	primaryConfig := workspaceConfig(config.Session{Title: "Repo", Directory: primary, Profile: "work"})
	workspaces, err := service.Build(ctx, primaryConfig)
	if err != nil {
		t.Fatalf("Build primary: %v", err)
	}
	if len(workspaces) != 1 || len(workspaces[0].Worktrees) != 2 {
		t.Fatalf("primary Build = %#v, want one workspace with primary and linked contexts", workspaces)
	}
	workspaceID := workspaces[0].ID

	// Reordering the configured launch location to the linked checkout keeps
	// the workspace ID and turns the former canonical path into an alias.
	linkedConfig := workspaceConfig(config.Session{Title: "Repo feature", Directory: linked, Profile: "work"})
	workspaces, err = service.Build(ctx, linkedConfig)
	if err != nil {
		t.Fatalf("Build linked: %v", err)
	}
	if got := workspaces[0].ID; got != workspaceID {
		t.Fatalf("linked workspace ID = %q, want %q", got, workspaceID)
	}
	if got := workspaces[0].CanonicalPath; got != projectresolve.AbsPath(linked) {
		t.Errorf("linked canonical path = %q, want %q", got, projectresolve.AbsPath(linked))
	}
	if !containsString(workspaces[0].Aliases, projectresolve.AbsPath(primary)) {
		t.Errorf("historical primary path missing from aliases: %#v", workspaces[0].Aliases)
	}
	byOldPath, err := service.Resolve(ctx, linkedConfig, primary)
	if err != nil || byOldPath.ID != workspaceID {
		t.Fatalf("Resolve historical primary path = %#v, %v", byOldPath, err)
	}

	match, ok, err := service.MatchCWD(ctx, linkedConfig, linkedCWD)
	if err != nil || !ok {
		t.Fatalf("MatchCWD linked checkout = %#v, %v, %v", match, ok, err)
	}
	if match.Worktree == nil || match.Worktree.CanonicalPath != projectresolve.AbsPath(linked) {
		t.Errorf("matched worktree = %#v, want linked checkout", match.Worktree)
	}
}

func TestSynchronizeExcludesRemovedWorktreeButRetainsHistoricalRow(t *testing.T) {
	git := requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	primary := filepath.Join(root, "repo")
	linked := filepath.Join(root, "repo-feature")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatalf("Mkdir primary: %v", err)
	}
	runGit(t, git, primary, "init")
	runGit(t, git, primary, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runGit(t, git, primary, "worktree", "add", "-b", "feature/removed", linked)

	store := datastore.OpenTest(t)
	service := New(store)
	cfg := workspaceConfig(config.Session{ID: "repo", Title: "Repo", Directory: primary, Profile: "work"})
	catalog, err := service.Synchronize(ctx, cfg)
	if err != nil {
		t.Fatalf("initial Synchronize: %v", err)
	}
	if got := catalog.Workspaces(); len(got) != 1 || len(got[0].Worktrees) != 2 {
		t.Fatalf("initial catalog = %#v, want primary and linked worktrees", got)
	}
	linkedCanonical := ""
	for _, worktree := range catalog.Workspaces()[0].Worktrees {
		if !worktree.IsPrimary {
			linkedCanonical = worktree.CanonicalPath
		}
	}
	if linkedCanonical == "" {
		t.Fatal("initial catalog did not identify the linked worktree")
	}
	runGit(t, git, primary, "worktree", "remove", "--force", linked)

	catalog, err = service.Synchronize(ctx, cfg)
	if err != nil {
		t.Fatalf("Synchronize after external removal: %v", err)
	}
	workspaces := catalog.Workspaces()
	if len(workspaces) != 1 || len(workspaces[0].Worktrees) != 1 || workspaces[0].Worktrees[0].CanonicalPath != projectresolve.AbsPath(primary) {
		t.Fatalf("current catalog worktrees = %#v, want only primary", workspaces)
	}
	var historicalRows int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM worktree_contexts WHERE canonical_path = ?`,
		linkedCanonical,
	).Scan(&historicalRows); err != nil {
		t.Fatalf("query historical worktree: %v", err)
	}
	if historicalRows != 1 {
		t.Fatalf("historical linked worktree rows = %d, want 1", historicalRows)
	}
}

func TestSynchronizedCatalogIsImmutableAcrossConfigReloadAndSupportsEmptyConfig(t *testing.T) {
	ctx := context.Background()
	service := New(datastore.OpenTest(t))
	firstDir := t.TempDir()
	firstConfig := workspaceConfig(config.Session{
		ID: "first", Title: "First", Directory: firstDir, Profile: "work",
		Env: map[string]string{"TEAM": "platform"}, Groups: []string{"backend"},
	})
	first, err := service.Synchronize(ctx, firstConfig)
	if err != nil {
		t.Fatalf("Synchronize first config: %v", err)
	}
	if !first.Synchronized() {
		t.Fatal("first catalog is not marked synchronized")
	}

	// Neither caller mutations nor later configuration changes may alter a
	// retained catalog.
	returned := first.Workspaces()
	returned[0].DisplayName = "changed copy"
	returned[0].DefaultLaunch.Env["TEAM"] = "changed copy"
	returned[0].DefaultLaunch.Groups[0] = "changed copy"
	firstConfig.Projects[0].Title = "changed config"
	firstConfig.Projects[0].Env["TEAM"] = "changed config"
	retained := first.Workspaces()
	if retained[0].DisplayName != "First" || retained[0].DefaultLaunch.Title != "First" || retained[0].DefaultLaunch.Env["TEAM"] != "platform" || retained[0].DefaultLaunch.Groups[0] != "backend" {
		t.Fatalf("retained catalog was mutated: %#v", retained[0])
	}
	if len(retained[0].Worktrees) != 0 {
		t.Fatalf("non-Git workspace has worktrees: %#v", retained[0].Worktrees)
	}

	secondDir := t.TempDir()
	second, err := service.Synchronize(ctx, workspaceConfig(config.Session{ID: "second", Title: "Second", Directory: secondDir, Profile: "personal"}))
	if err != nil {
		t.Fatalf("Synchronize reloaded config: %v", err)
	}
	if got := second.Workspaces(); len(got) != 1 || got[0].ID != "second" {
		t.Fatalf("reloaded catalog = %#v, want second", got)
	}
	if got := first.Workspaces(); len(got) != 1 || got[0].ID != "first" {
		t.Fatalf("retained first catalog after reload = %#v", got)
	}

	empty, err := service.Synchronize(ctx, &config.Config{})
	if err != nil {
		t.Fatalf("Synchronize empty config: %v", err)
	}
	if !empty.Synchronized() || len(empty.Workspaces()) != 0 {
		t.Fatalf("empty catalog = synchronized %v workspaces %#v", empty.Synchronized(), empty.Workspaces())
	}
	snapshot, err := ProjectSnapshot(ctx, empty, SnapshotOptions{})
	if err != nil || len(snapshot.Workspaces) != 0 {
		t.Fatalf("empty projection = %#v, %v", snapshot, err)
	}
}

func workspaceConfig(projects ...config.Session) *config.Config {
	return &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/work", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/personal", Color: "green"},
		},
		Projects: projects,
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return git
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agent Mux",
		"GIT_AUTHOR_EMAIL=agentmux@example.invalid",
		"GIT_COMMITTER_NAME=Agent Mux",
		"GIT_COMMITTER_EMAIL=agentmux@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
