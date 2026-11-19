package tmux

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestServerExecutableReadsTheKernelPath(t *testing.T) {
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ServerExecutable(strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Skipf("unsupported here: %v", err)
	}
	if !SameExecutable(got, want) {
		t.Fatalf("ServerExecutable(self) = %q, want %q", got, want)
	}
}

func TestServerExecutableRejectsABadPID(t *testing.T) {
	for _, pid := range []string{"", "0", "-3", "x"} {
		if _, err := ServerExecutable(pid); err == nil {
			t.Fatalf("ServerExecutable(%q) succeeded", pid)
		}
	}
}

func TestSameExecutableFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tmux-real")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "tmux")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if !SameExecutable(link, target) {
		t.Fatal("a symlink and its target compare different")
	}
	if SameExecutable(target, filepath.Join(dir, "other")) {
		t.Fatal("different files compare equal")
	}
}
