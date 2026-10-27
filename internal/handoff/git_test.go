package handoff

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitInspectionFixture(t *testing.T) (string, func(...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Agent Mux Test")
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-m", "initial")
	return repo, git
}

// A handoff inspects the source session's checkout, where its agent may be
// committing, so the inspection must never take index.lock. A file with stale
// stat information is what makes status or diff rewrite the index; the
// inspection must leave it exactly as it was.
func TestGitInspectionLeavesTheIndexAlone(t *testing.T) {
	t.Parallel()
	repo, _ := gitInspectionFixture(t)
	readme := filepath.Join(repo, "README.md")
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(readme, stale, stale); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	state := commandGitInspector{}.Inspect(context.Background(), repo, 10*time.Second)
	if !state.Available || state.Status != "" {
		t.Fatalf("state = %+v, want a clean, available checkout", state)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the handoff's Git inspection rewrote the index")
	}
}

func TestGitInspectionIncludesUntrackedFilesHiddenByConfig(t *testing.T) {
	t.Parallel()
	repo, git := gitInspectionFixture(t)
	git("config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("unfinished work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := commandGitInspector{}.Inspect(context.Background(), repo, 10*time.Second)
	if !state.Available || !strings.Contains(state.Status, "?? new.txt") {
		t.Fatalf("handoff hides untracked work: %+v", state)
	}
}

func TestGitInspectionIncludesSubmoduleChangesHiddenByConfig(t *testing.T) {
	t.Parallel()
	repo, git := gitInspectionFixture(t)
	child, _ := gitInspectionFixture(t)
	git("-c", "protocol.file.allow=always", "submodule", "add", child, "child")
	git("commit", "-am", "add submodule")
	git("config", "submodule.child.ignore", "all")
	git("config", "diff.ignoreSubmodules", "all")
	if err := os.WriteFile(filepath.Join(repo, "child", "README.md"), []byte("unfinished work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := commandGitInspector{}.Inspect(context.Background(), repo, 10*time.Second)
	if !state.Available || !strings.Contains(state.Status, "child") || !strings.Contains(state.DiffStat, "child") {
		t.Fatalf("handoff hides submodule work: %+v", state)
	}
}

func TestGitInspectionIncludesChangesOutsideSessionSubdirectory(t *testing.T) {
	t.Parallel()
	repo, git := gitInspectionFixture(t)
	git("config", "diff.relative", "true")
	subdir := filepath.Join(repo, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("unfinished work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := commandGitInspector{}.Inspect(context.Background(), subdir, 10*time.Second)
	if !state.Available || !strings.Contains(state.DiffStat, "README.md") {
		t.Fatalf("handoff hides work outside the session directory: %+v", state)
	}
}
