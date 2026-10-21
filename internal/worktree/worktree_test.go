package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestCreateReturnsGitAuthoritativeWorktreeAndRemoveUnusedCleansIt(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	destinationRoot := filepath.Join(t.TempDir(), "worktrees")
	workflow := testWorkflow(destinationRoot)
	cfg := testConfig(repo)

	result, err := workflow.Create(context.Background(), Request{Config: cfg, SourceCWD: repo, SessionID: "session-1234567890"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantParent, err := canonicalDirectory(filepath.Join(destinationRoot, "api"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Path, wantParent+string(os.PathSeparator)) {
		t.Fatalf("worktree path = %q, want project parent %q", result.Path, wantParent)
	}
	wantSourceRoot, err := canonicalDirectory(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectLabel != "api" || result.BaseRef != "HEAD" || result.SourceRoot != wantSourceRoot {
		t.Fatalf("result = %+v, want configured project and Git source", result)
	}
	if result.ID == "" || result.BaseCommit != strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD")) ||
		result.SourceBranch == "" || result.CreatedAt.IsZero() || result.MetadataPath != SidecarPath(result.Path) {
		t.Fatalf("result = %+v, want durable metadata identity", result)
	}
	if result.Branch != "amx/api/session-1234-20260711T120000Z" {
		t.Fatalf("branch = %q", result.Branch)
	}
	if got := gitOutput(t, result.Path, "branch", "--show-current"); strings.TrimSpace(got) != result.Branch {
		t.Fatalf("worktree branch = %q, want %q", got, result.Branch)
	}

	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("successful worktree was not retained: %v", err)
	}
	for _, path := range []string{destinationRoot, filepath.Join(destinationRoot, mutationLockName)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		wantMode := os.FileMode(0o700)
		if path != destinationRoot {
			wantMode = 0o600
		}
		if info.Mode().Perm() != wantMode {
			t.Fatalf("mode for %s = %o, want %o", path, info.Mode().Perm(), wantMode)
		}
	}
	metadata, err := ReadMetadata(result.MetadataPath)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if metadata.ID != result.ID || metadata.Path != result.Path || metadata.Branch != result.Branch ||
		metadata.BaseCommit != result.BaseCommit || metadata.SourceBranch != result.SourceBranch ||
		metadata.SourceRoot != result.SourceRoot || metadata.SourceConversationID != "session-1234567890" {
		t.Fatalf("metadata = %+v, want creation result provenance", metadata)
	}

	if err := workflow.RemoveUnused(context.Background(), result); err != nil {
		t.Fatalf("RemoveUnused: %v", err)
	}
	if _, err := os.Stat(result.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed worktree stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(result.MetadataPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed metadata stat error = %v, want not exist", err)
	}
	if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+result.Branch).Run(); err == nil {
		t.Fatalf("branch %q remains after unused cleanup", result.Branch)
	}
}

func TestCreateAcceptsUnconfiguredRepositoryAndUsesExplicitTranscriptLabel(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))

	result, err := workflow.Create(context.Background(), Request{
		ProjectLabel: "Transcript Project", SourceCWD: repo, SessionID: "source-1",
	})
	if err != nil {
		t.Fatalf("Create unconfigured repository: %v", err)
	}
	if result.ProjectLabel != "Transcript Project" || !strings.Contains(result.Path, string(os.PathSeparator)+"transcript-project"+string(os.PathSeparator)) ||
		!strings.HasPrefix(result.Branch, "amx/transcript-project/") {
		t.Fatalf("result = %+v, want transcript label and slug", result)
	}
	if err := workflow.RemoveUnused(context.Background(), result); err != nil {
		t.Fatal(err)
	}
}

func TestCreateUsesRepositoryBasenameWithoutProjectHints(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))

	result, err := workflow.Create(context.Background(), Request{SourceCWD: repo, SessionID: "source-1"})
	if err != nil {
		t.Fatalf("Create unconfigured repository: %v", err)
	}
	if result.ProjectLabel != filepath.Base(repo) {
		t.Fatalf("project label = %q, want repository basename %q", result.ProjectLabel, filepath.Base(repo))
	}
	if err := workflow.RemoveUnused(context.Background(), result); err != nil {
		t.Fatal(err)
	}
}

func TestCreateMatchesConfiguredProjectFromLinkedWorktree(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, repo, "worktree", "add", "-b", "linked-source", linked, "HEAD")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked).Run()
		_ = exec.Command("git", "-C", repo, "branch", "-D", "linked-source").Run()
	})
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))

	result, err := workflow.Create(context.Background(), Request{Config: testConfig(repo), SourceCWD: linked, SessionID: "source-1"})
	if err != nil {
		t.Fatalf("Create from linked worktree: %v", err)
	}
	wantSourceRoot, err := canonicalDirectory(linked)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectLabel != "api" || result.SourceRoot != wantSourceRoot {
		t.Fatalf("result = %+v, want configured label and linked source root", result)
	}
	if err := workflow.RemoveUnused(context.Background(), result); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRefusesDirtySourceUnlessExplicitlyAllowed(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := workflow.Create(context.Background(), Request{Config: testConfig(repo), SourceCWD: repo, SessionID: "source-1"})
	if !errors.Is(err, ErrDirtySource) {
		t.Fatalf("Create error = %v, want ErrDirtySource", err)
	}
	result, err := workflow.Create(context.Background(), Request{Config: testConfig(repo), SourceCWD: repo, SessionID: "source-1", AllowDirty: true})
	if err != nil {
		t.Fatalf("allow-dirty Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.Path, "dirty.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirty source file was copied into isolated worktree: %v", err)
	}
	if err := workflow.RemoveUnused(context.Background(), result); err != nil {
		t.Fatal(err)
	}
}

func TestCreateResolvesBranchAndPathCollisionsDeterministically(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	request := Request{Config: testConfig(repo), SourceCWD: repo, SessionID: "source-1"}
	first, err := workflow.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workflow.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Branch == second.Branch || first.Path == second.Path {
		t.Fatalf("collision was not resolved: first=%+v second=%+v", first, second)
	}
	if !strings.HasSuffix(second.Branch, "-2") || !strings.HasSuffix(second.Path, "-2") {
		t.Fatalf("second collision name = branch %q path %q, want deterministic -2 suffix", second.Branch, second.Path)
	}
	if err := workflow.RemoveUnused(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err := workflow.RemoveUnused(context.Background(), first); err != nil {
		t.Fatal(err)
	}
}

func TestCreateChecksPathAndBranchCollisionsIndependently(t *testing.T) {
	t.Parallel()

	const (
		leaf       = "source-1-20260711T120000Z"
		baseBranch = "amx/api/" + leaf
	)
	for _, tc := range []struct {
		name    string
		occupy  func(*testing.T, string, string)
		cleanup func(*testing.T, string)
	}{
		{
			name: "path",
			occupy: func(t *testing.T, repo, destinationRoot string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(destinationRoot, "api", leaf), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "branch",
			occupy: func(t *testing.T, repo, destinationRoot string) {
				t.Helper()
				gitRun(t, repo, "branch", baseBranch)
			},
			cleanup: func(t *testing.T, repo string) {
				t.Helper()
				gitRun(t, repo, "branch", "-D", baseBranch)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := testRepository(t)
			destinationRoot := filepath.Join(t.TempDir(), "worktrees")
			tc.occupy(t, repo, destinationRoot)
			workflow := testWorkflow(destinationRoot)
			result, err := workflow.Create(context.Background(), Request{Config: testConfig(repo), SourceCWD: repo, SessionID: "source-1"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(result.Branch, "-2") || !strings.HasSuffix(result.Path, "-2") {
				t.Fatalf("collision name = branch %q path %q, want deterministic -2 suffix", result.Branch, result.Path)
			}
			if err := workflow.RemoveUnused(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			if tc.cleanup != nil {
				tc.cleanup(t, repo)
			}
		})
	}
}

func TestCreateReportsNonGitCheckout(t *testing.T) {
	t.Parallel()

	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	_, err := workflow.Create(context.Background(), Request{SourceCWD: t.TempDir(), SessionID: "source-1"})
	if !errors.Is(err, ErrNotGitCheckout) {
		t.Fatalf("Create error = %v, want ErrNotGitCheckout", err)
	}
}

func TestCreateCapturesProfileHarnessGitIdentityAndDetachedSource(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	baseCommit := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "checkout", "--detach", baseCommit)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))

	result, err := workflow.Create(context.Background(), Request{
		ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1", Profile: "work", Harness: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = workflow.RemoveUnused(context.Background(), result) }()
	metadata, err := ReadMetadata(result.MetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	wantCommonDir, err := canonicalDirectory(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceBranch != "" || metadata.SourceBranch != "" || result.BaseCommit != baseCommit ||
		metadata.BaseCommit != baseCommit || metadata.GitCommonDir != wantCommonDir ||
		metadata.Profile != "work" || metadata.Harness != "codex" || metadata.SourceConversationID != "source-1" {
		t.Fatalf("result=%+v metadata=%+v, want detached source provenance", result, metadata)
	}
}

func TestCreateRollsBackCheckoutBranchAndMetadataWhenSidecarWriteFails(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	destinationRoot := filepath.Join(t.TempDir(), "worktrees")
	workflow := testWorkflow(destinationRoot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workflow.writeMetadata = func(path string, metadata Metadata) error {
		if err := os.WriteFile(path, []byte("partial metadata"), 0o600); err != nil {
			return err
		}
		cancel()
		return errors.New("injected metadata failure")
	}

	_, err := workflow.Create(ctx, Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1"})
	if err == nil || !strings.Contains(err.Error(), "persisting metadata") || !strings.Contains(err.Error(), "injected metadata failure") {
		t.Fatalf("Create error = %v, want metadata persistence failure", err)
	}
	destination := filepath.Join(destinationRoot, "api", "source-1-20260711T120000Z")
	for _, path := range []string{destination, SidecarPath(destination)} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("artifact %s stat error = %v, want not exist", path, statErr)
		}
	}
	branch := "amx/api/source-1-20260711T120000Z"
	if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run(); err == nil {
		t.Fatalf("branch %q remains after metadata rollback", branch)
	}
}

func TestCreatePreservesMetadataAndRollbackErrors(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	workflow.writeMetadata = func(string, Metadata) error { return errors.New("metadata unavailable") }
	var retained Result
	workflow.rollback = func(_ context.Context, result Result) error {
		retained = result
		return errors.New("rollback unavailable")
	}

	_, err := workflow.Create(context.Background(), Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1"})
	if err == nil || !strings.Contains(err.Error(), "metadata unavailable") || !strings.Contains(err.Error(), "rollback unavailable") {
		t.Fatalf("Create error = %v, want primary and rollback failures", err)
	}
	workflow.rollback = nil
	if cleanupErr := workflow.RemoveUnused(context.Background(), retained); cleanupErr != nil {
		t.Fatalf("test cleanup: %v", cleanupErr)
	}
}

func TestRemoveUnusedDeletesMetadataWhenGitCleanupFails(t *testing.T) {
	t.Parallel()

	metadataPath := filepath.Join(t.TempDir(), "unused"+MetadataSuffix)
	if err := os.WriteFile(metadataPath, []byte("metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	err := workflow.RemoveUnused(context.Background(), Result{
		Path: filepath.Join(t.TempDir(), "missing-checkout"), Branch: "amx/api/missing",
		SourceRoot: t.TempDir(), MetadataPath: metadataPath,
	})
	if err == nil {
		t.Fatal("RemoveUnused succeeded with a non-repository source root")
	}
	if _, statErr := os.Stat(metadataPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("metadata stat error = %v, want not exist despite Git cleanup failure", statErr)
	}
}

func TestConcurrentCreateSerializesCollisionAllocationAndMetadata(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	workflow := testWorkflow(filepath.Join(t.TempDir(), "worktrees"))
	request := Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1", Profile: "work", Harness: "codex"}
	const count = 4
	type outcome struct {
		result Result
		err    error
	}
	outcomes := make(chan outcome, count)
	var start sync.WaitGroup
	start.Add(1)
	for range count {
		go func() {
			start.Wait()
			result, err := workflow.Create(context.Background(), request)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	start.Done()

	results := make([]Result, 0, count)
	branches := make([]string, 0, count)
	ids := make(map[string]struct{}, count)
	for range count {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("concurrent Create: %v", outcome.err)
		}
		if _, duplicate := ids[outcome.result.ID]; duplicate {
			t.Fatalf("duplicate stable ID %q", outcome.result.ID)
		}
		ids[outcome.result.ID] = struct{}{}
		if _, err := ReadMetadata(outcome.result.MetadataPath); err != nil {
			t.Fatalf("ReadMetadata(%s): %v", outcome.result.MetadataPath, err)
		}
		results = append(results, outcome.result)
		branches = append(branches, outcome.result.Branch)
	}
	sort.Strings(branches)
	wantBranches := []string{
		"amx/api/source-1-20260711T120000Z",
		"amx/api/source-1-20260711T120000Z-2",
		"amx/api/source-1-20260711T120000Z-3",
		"amx/api/source-1-20260711T120000Z-4",
	}
	sort.Strings(wantBranches)
	if fmt.Sprint(branches) != fmt.Sprint(wantBranches) {
		t.Fatalf("branches = %v, want %v", branches, wantBranches)
	}
	for _, result := range results {
		if err := workflow.RemoveUnused(context.Background(), result); err != nil {
			t.Fatalf("RemoveUnused(%s): %v", result.Path, err)
		}
	}
}

func testWorkflow(destinationRoot string) *Workflow {
	return &Workflow{
		directory: func() (string, error) { return destinationRoot, nil },
		now:       func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) },
	}
}

func testConfig(repo string) *config.Config {
	return &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Projects: []config.Session{{Title: "api", Directory: repo, Profile: "work"}},
	}
}

func testRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Agent Mux Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-m", "initial")
	return repo
}

func gitRun(t *testing.T, directory string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
