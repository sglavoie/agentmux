package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLandRefusesSourceBranchInBisect(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"checkout", "no-checkout"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			repo, inventory, entry := reviewFixture(t)
			commitFile(t, entry.Path, "first.txt", "first\n", "first")
			commitFile(t, entry.Path, "second.txt", "second\n", "second")
			if _, err := inventory.Land(context.Background(), entry.ID); err != nil {
				t.Fatal(err)
			}
			before := revision(t, repo, "HEAD")
			commitFile(t, entry.Path, "third.txt", "third\n", "third")
			args := []string{"bisect", "start"}
			if mode == "no-checkout" {
				args = append(args, "--no-checkout")
			}
			gitRun(t, repo, append(args, "HEAD", "HEAD~2")...)
			t.Cleanup(func() { _ = execGit(repo, "bisect", "reset") })
			if err := execGit(repo, "symbolic-ref", "-q", "HEAD"); err == nil && mode == "checkout" {
				t.Fatal("expected detached HEAD during bisect")
			}
			result, err := inventory.Land(context.Background(), entry.ID)
			if !errors.Is(err, ErrSourceBusy) || result.Landed {
				t.Errorf("Land = %+v, %v; want busy-source refusal", result, err)
			}
			if got := revision(t, repo, "refs/heads/"+entry.SourceBranch); got != before {
				t.Errorf("bisect source branch moved from %s to %s", before, got)
			}
		})
	}
}

func TestRemoveRefusesBranchInBisect(t *testing.T) {
	t.Parallel()
	repo, inventory, entry := reviewFixture(t)
	commitFile(t, entry.Path, "first.txt", "first\n", "first")
	commitFile(t, entry.Path, "second.txt", "second\n", "second")
	if _, err := inventory.Land(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "remove", entry.Path)
	gitRun(t, repo, "checkout", entry.Branch)
	before := revision(t, repo, "HEAD")
	gitRun(t, repo, "bisect", "start", "HEAD", "HEAD~2")
	t.Cleanup(func() { _ = execGit(repo, "bisect", "reset") })
	if err := execGit(repo, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Fatal("expected detached HEAD during bisect")
	}
	result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
	if err == nil || result.BranchDeleted || result.MetadataForgotten {
		t.Errorf("Remove = %+v, %v; want bisect branch and metadata preserved", result, err)
	}
	if exists, err := gitBranchExists(context.Background(), repo, entry.Branch); err != nil || !exists {
		t.Errorf("bisect branch exists = %t, %v; want kept at %s", exists, err, before)
	} else if got := revision(t, repo, "refs/heads/"+entry.Branch); got != before {
		t.Errorf("bisect branch moved from %s to %s", before, got)
	}
	if _, err := os.Stat(entry.MetadataPath); err != nil {
		t.Errorf("metadata was lost: %v", err)
	}
}

func TestBranchMutationsRefuseUnavailableOperationState(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"land", "remove"} {
		for _, failure := range []string{"git", "read"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				t.Parallel()
				repo, inventory, entry := reviewFixture(t)
				ref := "refs/heads/" + entry.SourceBranch
				if operation == "land" {
					commitFile(t, entry.Path, "feature.txt", "feature\n", "feature")
					gitRun(t, repo, "checkout", "-b", "elsewhere")
				} else {
					gitRun(t, repo, "worktree", "remove", entry.Path)
					ref = "refs/heads/" + entry.Branch
				}
				before := revision(t, repo, ref)
				unreadable := filepath.Join(t.TempDir(), "head-name")
				if err := os.Mkdir(unreadable, 0o700); err != nil {
					t.Fatal(err)
				}
				git := inventory.git
				probed := false
				inventory.git = func(ctx context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
					if strings.Join(args, " ") == "rev-parse --path-format=absolute --git-path rebase-merge/head-name" {
						probed = true
						if failure == "git" {
							return nil, errors.New("Git state probe failed")
						}
						return []byte(unreadable + "\n"), nil
					}
					return git(ctx, directory, timeout, args...)
				}
				if operation == "land" {
					result, err := inventory.Land(context.Background(), entry.ID)
					if !errors.Is(err, ErrGitUnavailable) || result.Landed {
						t.Errorf("Land = %+v, %v; want unavailable-state refusal", result, err)
					}
				} else {
					result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
					if !errors.Is(err, ErrGitUnavailable) || result.BranchDeleted || result.MetadataForgotten {
						t.Errorf("Remove = %+v, %v; want branch and metadata preserved", result, err)
					}
				}
				if !probed {
					t.Fatal("operation state was not probed")
				}
				if exists, err := inventory.gitRefExists(context.Background(), repo, ref); err != nil || !exists {
					t.Errorf("ref exists = %t, %v; want kept", exists, err)
				} else if got := revision(t, repo, ref); got != before {
					t.Errorf("ref moved from %s to %s", before, got)
				}
			})
		}
	}
}
