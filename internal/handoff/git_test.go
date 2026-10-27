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

// A handoff inspects the source session's checkout, where its agent may be
// committing, so the inspection must never take index.lock. A file with stale
// stat information is what makes status or diff rewrite the index; the
// inspection must leave it exactly as it was.
func TestGitInspectionLeavesTheIndexAlone(t *testing.T) {
	t.Parallel()

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
