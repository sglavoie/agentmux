package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// Execute the launch command against a stand-in CLI: capability selection,
// quoting and profile environment must survive the actual shell boundary.
func TestManagedCodexLaunchTransport(t *testing.T) {
	for _, shell := range []string{"sh", "zsh"} {
		shellPath, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		for _, tc := range []struct {
			name     string
			help     string
			status   string
			args     []string
			isolated bool
			probe    bool
		}{
			{name: "supported", help: "Options: --no-daemon", isolated: true, probe: true},
			{name: "older CLI", help: "Options: --model", probe: true},
			{name: "failed help", help: "Options: --no-daemon", status: "1", probe: true},
			{name: "explicit isolation", args: []string{"--no-daemon"}, isolated: true},
			{name: "remote separate", args: []string{"--remote", "unix:///tmp/server"}},
			{name: "remote equals", args: []string{"--remote=unix:///tmp/server"}},
		} {
			t.Run(shell+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				cli := filepath.Join(dir, "codex test")
				probeLog := filepath.Join(dir, "help-called")
				status := tc.status
				if status == "" {
					status = "0"
				}
				script := "#!/bin/sh\nif [ \"$1\" = --help ]; then\n" +
					"printf '%s' \"$CODEX_HOME\" > " + shellQuoteWord(probeLog) + "\n" +
					"printf '%s\\n' " + shellQuoteWord(tc.help) + "\nexit " + status + "\nfi\n" +
					"printf '%s\\n' \"$CODEX_HOME\" \"$AMX_RUNTIME_SESSION_ID\" \"$@\"\n"
				if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				prompt := "literal $(touch should-not-exist) `echo unsafe` 'quoted'"
				logging := false
				invocation, err := buildLaunchInvocation(config.Session{Directory: dir, Args: tc.args},
					config.Account{Harness: harness.Codex, HomeDir: dir}, config.Defaults{Diagnostics: config.DiagnosticsConfig{NativeLogging: &logging}},
					harness.InvocationResume, "session with spaces", prompt)
				if err != nil {
					t.Fatal(err)
				}
				invocation.Executable = cli
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, shellPath, "-c", RenderInvocation(invocation))
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "AMX_RUNTIME_SESSION_ID=rs-terminal-owner", "CODEX_HOME=/wrong")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("launch: %v: %s", err, out)
				}
				want := []string{dir, "rs-terminal-owner"}
				if tc.isolated && tc.probe {
					want = append(want, "--no-daemon")
				}
				want = append(want, tc.args...)
				want = append(want, "resume", "session with spaces", prompt)
				if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !reflect.DeepEqual(got, want) {
					t.Fatalf("argv/environment = %#v, want %#v", got, want)
				}
				data, err := os.ReadFile(probeLog)
				if tc.probe {
					if err != nil || string(data) != dir {
						t.Fatalf("help profile = %q, %v", data, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("explicit transport was probed: %v", err)
				}
				if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
					t.Fatal("prompt executed as shell code")
				}
			})
		}
	}
}

func TestCodexTransportOnlyForManagedCodex(t *testing.T) {
	for _, name := range []string{harness.Claude, harness.Copilot, harness.Codex} {
		invocation := harness.Invocation{Harness: name, Executable: name}
		if isolateCodexLaunch(invocation) {
			t.Fatalf("unmanaged %s was changed", name)
		}
		invocation.LaunchID = "managed"
		if got := isolateCodexLaunch(invocation); got != (name == harness.Codex) {
			t.Fatalf("%s isolation = %v", name, got)
		}
	}
}
