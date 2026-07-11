package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
)

func TestCreateTracksIsolatedWorktreeAndRemoveUnusedCleansIt(t *testing.T) {
	repo := testRepository(t)
	dbPath := filepath.Join(t.TempDir(), "agent-mux.db")
	workflow := testWorkflow(t, dbPath)
	cfg := testConfig(repo)

	result, err := workflow.Create(context.Background(), Request{Config: cfg, SourceCWD: repo, SessionID: "session-1234567890"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantParent := filepath.Join(filepath.Dir(dbPath), "worktrees", "api")
	if !strings.HasPrefix(result.Path, wantParent+string(os.PathSeparator)) {
		t.Fatalf("worktree path = %q, want managed parent %q", result.Path, wantParent)
	}
	if result.Branch != "amx/api/session-1234-20260711T120000Z" {
		t.Fatalf("branch = %q", result.Branch)
	}
	if got := gitOutput(t, result.Path, "branch", "--show-current"); strings.TrimSpace(got) != result.Branch {
		t.Fatalf("worktree branch = %q, want %q", got, result.Branch)
	}

	store, err := datastore.OpenPath(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	var status, contextID string
	if err := store.DB().QueryRow(`SELECT status, worktree_context_id FROM managed_worktrees WHERE id = ?`, result.ID).Scan(&status, &contextID); err != nil {
		t.Fatalf("managed row: %v", err)
	}
	if status != managedStatusActive || contextID != result.WorktreeContextID || contextID == "" {
		t.Fatalf("managed row = status=%q context=%q, result=%+v", status, contextID, result)
	}

	if err := workflow.RemoveUnused(context.Background(), result); err != nil {
		t.Fatalf("RemoveUnused: %v", err)
	}
	if _, err := os.Stat(result.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed worktree stat error = %v, want not exist", err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM managed_worktrees WHERE id = ?`, result.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("managed record count = %d, want 0", count)
	}
	if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+result.Branch).Run(); err == nil {
		t.Fatalf("branch %q remains after unused cleanup", result.Branch)
	}
}

func TestCreateRefusesDirtySourceUnlessExplicitlyAllowed(t *testing.T) {
	repo := testRepository(t)
	dbPath := filepath.Join(t.TempDir(), "agent-mux.db")
	workflow := testWorkflow(t, dbPath)
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
	repo := testRepository(t)
	workflow := testWorkflow(t, filepath.Join(t.TempDir(), "agent-mux.db"))
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

func testWorkflow(t *testing.T, path string) *Workflow {
	t.Helper()
	var next int
	return &Workflow{
		openStore: func(ctx context.Context) (*datastore.Store, error) { return datastore.OpenPath(ctx, path) },
		now:       func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) },
		newID: func(prefix string) (string, error) {
			next++
			return prefix + "-test-" + string(rune('0'+next)), nil
		},
	}
}

func testConfig(repo string) *config.Config {
	return &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Projects: []config.Session{{ID: "api", Title: "api", Directory: repo, Profile: "work"}},
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
