package gitstate

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// A review that git refuses says why in git's words, never as an exit status.
func TestReadChangesReportsGitRefusalsPlainly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", dir)
	_, err := ReadChanges(context.Background(), dir, "", "")
	if err == nil || strings.Contains(err.Error(), "exit status") || !strings.Contains(err.Error(), "is not inside a Git checkout") {
		t.Fatalf("outside a checkout: %v", err)
	}

	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	_, err = ReadChanges(context.Background(), repo, "no-such-ref", "")
	if err == nil || strings.Contains(err.Error(), "exit status") || !strings.Contains(err.Error(), `comparison base "no-such-ref" is not a commit`) {
		t.Fatalf("unknown base: %v", err)
	}
}
