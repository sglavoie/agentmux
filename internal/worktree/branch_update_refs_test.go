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

func TestBranchMutationsPreserveRebaseUpdateRefs(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"land", "remove"} {
		for _, location := range []string{"main", "linked"} {
			t.Run(operation+"/"+location, func(t *testing.T) {
				t.Parallel()
				repo, inventory, entry := reviewFixture(t)
				commitFile(t, entry.Path, "first.txt", "first\n", "first")
				if _, err := inventory.Land(context.Background(), entry.ID); err != nil {
					t.Fatal(err)
				}
				ref := "refs/heads/" + entry.SourceBranch
				if operation == "land" {
					commitFile(t, entry.Path, "second.txt", "second\n", "second")
				} else {
					gitRun(t, repo, "worktree", "remove", entry.Path)
					ref = "refs/heads/" + entry.Branch
				}
				before := revision(t, repo, ref)
				gitRun(t, repo, "checkout", "-b", "stack")
				rebasing := repo
				if location == "linked" {
					gitRun(t, repo, "checkout", "--detach")
					rebasing = filepath.Join(t.TempDir(), "stack")
					gitRun(t, repo, "worktree", "add", rebasing, "stack")
				}
				commitFile(t, rebasing, "stack.txt", "stack\n", "stack")
				if err := execGit(rebasing, "rebase", "--force-rebase", "--update-refs", "--exec", "false", "HEAD~2"); err == nil {
					t.Fatal("expected rebase to pause at the failing exec")
				}
				t.Cleanup(func() { _ = execGit(rebasing, "rebase", "--abort") })
				statePath := strings.TrimSpace(gitOutput(t, rebasing, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge/update-refs"))
				state, err := os.ReadFile(statePath)
				if err != nil || !strings.Contains(string(state), ref+"\n") {
					t.Fatalf("rebase did not reserve %s: %q, %v", ref, state, err)
				}
				if operation == "land" {
					result, err := inventory.Land(context.Background(), entry.ID)
					if !errors.Is(err, ErrSourceBusy) || result.Landed {
						t.Errorf("Land = %+v, %v; want busy-source refusal", result, err)
					}
				} else {
					result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true})
					if err == nil || result.BranchDeleted || result.MetadataForgotten {
						t.Errorf("Remove = %+v, %v; want reserved branch and metadata preserved", result, err)
					}
				}
				if exists, err := inventory.gitRefExists(context.Background(), repo, ref); err != nil || !exists {
					t.Errorf("reserved branch lost: exists=%t, %v", exists, err)
				} else if after := revision(t, repo, ref); after != before {
					t.Errorf("reserved branch moved from %s to %s", before, after)
				}
				if after, err := os.ReadFile(statePath); err != nil || string(after) != string(state) {
					t.Errorf("rebase state changed: %q, %v", after, err)
				}
				if _, err := os.Stat(entry.MetadataPath); err != nil {
					t.Errorf("metadata lost: %v", err)
				}
				gitRun(t, rebasing, "rebase", "--abort")
				if operation == "land" {
					if result, err := inventory.Land(context.Background(), entry.ID); err != nil || !result.Landed {
						t.Errorf("Land after abort = %+v, %v", result, err)
					}
				} else if result, err := inventory.Remove(context.Background(), entry.ID, RemoveOptions{DeleteBranch: true}); err != nil || !result.BranchDeleted || !result.MetadataForgotten {
					t.Errorf("Remove after abort = %+v, %v", result, err)
				}
			})
		}
	}
}

func TestBranchOperationUpdateRefs(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"reserved", "unrelated", "missing", "unreadable", "probe failure"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			file := filepath.Join(directory, "update-refs")
			const ref = "refs/heads/topic"
			if scenario == "unreadable" {
				if err := os.Mkdir(file, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "missing" {
				reserved := ref
				if scenario == "unrelated" {
					reserved += "-other"
				}
				data := "refs/heads/first\n" + strings.Repeat("1", 40) + "\n" + strings.Repeat("0", 40) + "\n" + reserved + "\n" + strings.Repeat("2", 40) + "\n" + strings.Repeat("0", 40) + "\n"
				if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			probeError := errors.New("cannot locate rebase state")
			git := func(_ context.Context, _ string, _ time.Duration, args ...string) ([]byte, error) {
				if args[len(args)-1] == "rebase-merge/update-refs" {
					if scenario == "probe failure" {
						return nil, probeError
					}
					return []byte(file + "\n"), nil
				}
				return []byte(filepath.Join(directory, "absent") + "\n"), nil
			}
			operation, err := branchOperation(context.Background(), git, directory, ref)
			switch scenario {
			case "unreadable", "probe failure":
				if !errors.Is(err, ErrGitUnavailable) {
					t.Fatalf("branchOperation = %q, %v; want unavailable-state refusal", operation, err)
				}
			case "reserved":
				if err != nil || operation != "rebase" {
					t.Fatalf("branchOperation = %q, %v; want rebase", operation, err)
				}
			default:
				if err != nil || operation != "" {
					t.Fatalf("branchOperation = %q, %v; want unreserved branch", operation, err)
				}
			}
		})
	}
}
