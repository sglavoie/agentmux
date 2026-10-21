package gitstate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reviewFixture(t *testing.T) (string, func(...string) string, func(string, string)) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	write("tracked", "before\n")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "initial")
	return dir, git, write
}

func TestReviewIncludesStagedUnstagedUntrackedAndLiteralPaths(t *testing.T) {
	t.Parallel()

	dir, git, write := reviewFixture(t)
	write("tracked", "after\nsecond\n")
	write("staged", "staged\n")
	git("add", "staged")
	write("new\tfile", "new\n")
	write("--option", "literal\n")
	got, err := ReadChanges(context.Background(), dir, "HEAD", "new\tfile")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 4 {
		t.Fatalf("files: %+v", got.Files)
	}
	if !strings.Contains(got.Patch, "+new") {
		t.Fatalf("patch: %s", got.Patch)
	}
	var tracked ChangedFile
	for _, f := range got.Files {
		if f.Path == "tracked" {
			tracked = f
		}
	}
	if tracked.Added != 2 || tracked.Deleted != 1 {
		t.Fatalf("counts: %+v", tracked)
	}
	got, err = ReadChanges(context.Background(), dir, got.Base, "--option")
	if err != nil || !strings.Contains(got.Patch, "+literal") {
		t.Fatalf("literal patch: %+v, %v", got, err)
	}
}

func TestReviewBaseChangesAndRejectsUnknownOrOptionLikeReferences(t *testing.T) {
	t.Parallel()

	dir, git, write := reviewFixture(t)
	base := git("rev-parse", "HEAD")
	write("tracked", "committed\n")
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "second")
	got, err := ReadChanges(context.Background(), dir, base, "tracked")
	if err != nil || !strings.Contains(got.Patch, "+committed") {
		t.Fatalf("review: %+v, %v", got, err)
	}
	clean, err := ReadChanges(context.Background(), dir, "HEAD", "")
	if err != nil || len(clean.Files) != 0 {
		t.Fatalf("clean: %+v, %v", clean, err)
	}
	for _, bad := range []string{"--help", "missing-reference"} {
		if _, err := ReadChanges(context.Background(), dir, bad, ""); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := ReadChanges(context.Background(), dir, base, "../outside"); err == nil {
		t.Fatal("accepted an unlisted path")
	}
}

func TestReviewNeverRunsExternalDiffOrTextconv(t *testing.T) {
	t.Parallel()

	dir, git, write := reviewFixture(t)
	marker := filepath.Join(dir, "driver-ran")
	git("config", "diff.external", "touch "+marker)
	git("config", "diff.custom.textconv", "touch "+marker)
	write(".gitattributes", "tracked diff=custom\n")
	write("tracked", "after\n")
	got, err := ReadChanges(context.Background(), dir, "HEAD", "tracked")
	if err != nil || !strings.Contains(got.Patch, "+after") {
		t.Fatalf("review: %+v, %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("external driver ran")
	}
}

func TestReviewBinaryAndUntrackedSymlinkDoNotExposeTarget(t *testing.T) {
	t.Parallel()

	dir, git, write := reviewFixture(t)
	write("binary", "\x00old")
	git("add", "binary")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "binary")
	write("binary", "\x00new")
	if err := os.Symlink("tracked", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadChanges(context.Background(), dir, "HEAD", "link")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Patch, "symbolic link") {
		t.Fatalf("patch: %s", got.Patch)
	}
	if !got.Files[0].Binary {
		t.Fatalf("files: %+v", got.Files)
	}
}

func TestReviewOutputIsBounded(t *testing.T) {
	t.Parallel()

	var b reviewBuffer
	input := []byte(strings.Repeat("x", reviewOutputLimit+100))
	if n, err := b.Write(input); err != nil || n != len(input) {
		t.Fatalf("write: %d, %v", n, err)
	}
	if b.Len() != reviewOutputLimit || !b.truncated {
		t.Fatal("output was not bounded")
	}
	if n, err := b.Write([]byte("more")); err != nil || n != 4 {
		t.Fatalf("drain: %d, %v", n, err)
	}
}
