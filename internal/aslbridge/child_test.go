package aslbridge

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// argvEchoScript prints its own path followed by each argument on its own line,
// so a test can prove exactly how the executable was invoked.
const argvEchoScript = "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\"\n"

func TestExecLauncherRunsSpacedPathAsOneArgument(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Agent Tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "asl")
	if err := os.WriteFile(command, []byte(argvEchoScript), 0o755); err != nil {
		t.Fatal(err)
	}

	child, err := execLauncher(command)(context.Background())
	if err != nil {
		t.Fatalf("launching %q: %v", command, err)
	}
	defer func() { _ = child.Stdin.Close() }()

	var lines []string
	scanner := bufio.NewScanner(child.Stdout)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading child stdout: %v", err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("child exited with error: %v", err)
	}

	want := append([]string{command}, BridgeArgs...)
	if len(lines) != len(want) {
		t.Fatalf("child saw argv %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full argv %q)", i, lines[i], want[i], lines)
		}
	}
	if child.Path != command {
		t.Fatalf("child path = %q, want %q", child.Path, command)
	}
}

func TestExecLauncherRejectsArgumentBearingCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Agent Tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "asl")
	if err := os.WriteFile(command, []byte(argvEchoScript), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, argumentBearing := range []string{"asl bridge serve", command + " bridge serve"} {
		child, err := execLauncher(argumentBearing)(context.Background())
		if err == nil {
			_ = child.Stdin.Close()
			_ = child.Terminate()
			t.Errorf("command %q was launched", argumentBearing)
			continue
		}
		if !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("command %q: unexpected error: %v", argumentBearing, err)
		}
	}
}
