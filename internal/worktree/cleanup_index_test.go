package worktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupPreservesTrackedFilesHiddenByIndexFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		for _, unused := range []bool{false, true} {
			name := flag
			if unused {
				name += "/failed-launch"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				repo := testRepository(t)
				// Include a newline so the guard must parse NUL-delimited paths.
				const file = "local\nsettings.txt"
				commitFile(t, repo, file, "original\n", "settings")
				root := filepath.Join(t.TempDir(), "worktrees")
				workflow := testWorkflow(root)
				created, err := workflow.Create(context.Background(), Request{SourceCWD: repo, SessionID: "hidden-edits"})
				if err != nil {
					t.Fatal(err)
				}
				gitRun(t, created.Path, "update-index", flag, "--", file)
				path := filepath.Join(created.Path, file)
				const content = "valuable local settings\n"
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if status := gitOutput(t, created.Path, "status", "--porcelain"); status != "" {
					t.Fatalf("fixture is not hidden from status: %q", status)
				}
				index := strings.TrimSpace(gitOutput(t, created.Path, "rev-parse", "--path-format=absolute", "--git-path", "index"))
				before, err := os.ReadFile(index)
				if err != nil {
					t.Fatal(err)
				}
				if unused {
					err = workflow.RemoveUnused(context.Background(), created)
				} else {
					_, err = testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
				}
				if !errors.Is(err, ErrWorktreeDirty) || !strings.Contains(err.Error(), flag[2:]) {
					t.Errorf("cleanup = %v; want refusal explaining %s", err, flag)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != content {
					t.Errorf("cleanup lost hidden local work: %q, %v", got, err)
				}
				if after, err := os.ReadFile(index); err != nil || !bytes.Equal(before, after) {
					t.Errorf("cleanup changed the index: %v", err)
				}
				if _, err := os.Stat(created.MetadataPath); err != nil {
					t.Errorf("cleanup lost recovery metadata: %v", err)
				}
				if got := revision(t, repo, "refs/heads/"+created.Branch); got != created.BaseCommit {
					t.Errorf("cleanup moved branch to %s", got)
				}
				if t.Failed() {
					return
				}
				gitRun(t, created.Path, "update-index", "--no-assume-unchanged", "--", file)
				gitRun(t, created.Path, "update-index", "--no-skip-worktree", "--", file)
				if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if unused {
					err = workflow.RemoveUnused(context.Background(), created)
				} else {
					_, err = testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
				}
				if err != nil {
					t.Fatalf("cleanup after clearing flags and restoring content: %v", err)
				}
			})
		}
	}
}

func TestCleanupAllowsAbsentSkipWorktreeFiles(t *testing.T) {
	t.Parallel()
	for _, unused := range []bool{false, true} {
		name := "user-removal"
		if unused {
			name = "failed-launch"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, root, workflow, created := cleanupFixture(t)
			gitRun(t, created.Path, "update-index", "--skip-worktree", "README.md")
			if err := os.Remove(filepath.Join(created.Path, "README.md")); err != nil {
				t.Fatal(err)
			}
			var err error
			if unused {
				err = workflow.RemoveUnused(context.Background(), created)
			} else {
				_, err = testInventory(root).Remove(context.Background(), created.ID, RemoveOptions{})
			}
			if err != nil {
				t.Fatalf("cleanup with absent sparse path: %v", err)
			}
			assertPathMissing(t, created.Path)
		})
	}
}

func TestCleanupPreservesCheckoutWhenIndexFlagsCannotBeRead(t *testing.T) {
	t.Parallel()
	_, root, _, created := cleanupFixture(t)
	inventory := testInventory(root)
	git := inventory.git
	probeErr := errors.New("index is unreadable")
	inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "ls-files --cached -v -z" {
			return nil, probeErr
		}
		return git(ctx, directory, timeout, args...)
	}
	result, err := inventory.Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
	if !errors.Is(err, ErrGitUnavailable) || !errors.Is(err, probeErr) || result.CheckoutRemoved {
		t.Fatalf("Remove = %+v, %v; want refusal on index probe failure", result, err)
	}
	for _, path := range []string{created.Path, created.MetadataPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("cleanup lost %s: %v", path, err)
		}
	}
}
