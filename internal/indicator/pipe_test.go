package indicator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
)

func fakeSnapshots(snapshots map[int]processSnapshot) processSnapshotFunc {
	return func(pid int) (processSnapshot, bool) {
		snapshot, ok := snapshots[pid]
		return snapshot, ok
	}
}

func TestDiscoverHarnessProcessInteractiveHarnesses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		harness string
		command string
	}{
		{name: "claude", harness: "claude", command: "node /usr/local/bin/claude"},
		{name: "codex", harness: "codex", command: "/opt/homebrew/bin/codex"},
		{name: "copilot", harness: "copilot", command: "/usr/local/bin/copilot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovery := discoverHarnessProcess(102, tc.harness, fakeSnapshots(map[int]processSnapshot{
				102: {ParentPID: 101, StartToken: "Thu Jul 16 10:02:00 2026", CommandLine: "amx hook-indicator Stop"},
				101: {ParentPID: 100, StartToken: "Thu Jul 16 10:01:00 2026", CommandLine: "/bin/zsh"},
				100: {ParentPID: 1, StartToken: "Thu  Jul  16  10:00:00  2026", CommandLine: tc.command},
			}))
			if discovery.Harness != tc.harness || discovery.PID != 100 || discovery.PipeMode || !discovery.Trackable {
				t.Fatalf("discovery = %+v", discovery)
			}
			wantIdentity := daemon.HarnessProcessIdentity{Harness: tc.harness, PID: 100, StartToken: "Thu Jul 16 10:00:00 2026"}
			identity := discovery.Identity()
			if identity == nil || *identity != wantIdentity {
				t.Fatalf("identity = %+v, want %+v", identity, wantIdentity)
			}
		})
	}
}

func TestDiscoverHarnessProcessPipeVariants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		harness string
		command string
		want    bool
	}{
		{name: "claude short flag value", harness: "claude", command: "claude -p=hello", want: true},
		{name: "claude long flag value", harness: "claude", command: "claude --print=hello", want: true},
		{name: "copilot short flag value", harness: "copilot", command: "copilot -p=hello", want: true},
		{name: "copilot long flag value", harness: "copilot", command: "copilot --prompt=hello", want: true},
		{name: "codex exec", harness: "codex", command: "codex exec --json", want: true},
		{name: "codex e", harness: "codex", command: "codex e --json", want: true},
		{name: "codex review", harness: "codex", command: "codex review --uncommitted", want: true},
		{name: "codex leading boolean flag", harness: "codex", command: "codex --full-auto exec --json", want: true},
		{name: "codex leading valued flag", harness: "codex", command: "codex --profile work review", want: true},
		{name: "codex leading inline flag", harness: "codex", command: "codex --model=gpt-5 e", want: true},
		{name: "codex profile value collision", harness: "codex", command: "codex --profile review", want: false},
		{name: "codex short flag value collision", harness: "codex", command: "codex -m exec", want: false},
		{name: "codex profile collision before exec", harness: "codex", command: "codex --profile review exec", want: true},
		{name: "codex variadic short image before exec", harness: "codex", command: "codex -i a.png exec", want: false},
		{name: "codex variadic long image before review", harness: "codex", command: "codex --image a.png review", want: false},
		{name: "codex variadic multiple images before exec", harness: "codex", command: "codex -i a.png b.png exec", want: false},
		{name: "codex flag ends variadic image values", harness: "codex", command: "codex -i a.png --full-auto exec", want: true},
		{name: "codex image after exec", harness: "codex", command: "codex exec -i a.png", want: true},
		{name: "codex later positional collision", harness: "codex", command: "codex resume e", want: false},
		{name: "claude flag after positional", harness: "claude", command: "claude prompt -p", want: true},
		{name: "copilot flag after positional", harness: "copilot", command: "copilot prompt --prompt", want: true},
		{name: "claude option terminator", harness: "claude", command: "claude -- -p", want: false},
		{name: "copilot option terminator", harness: "copilot", command: "copilot -- --prompt", want: false},
		{name: "codex option terminator", harness: "codex", command: "codex -- review", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovery := discoverHarnessProcess(10, tc.harness, fakeSnapshots(map[int]processSnapshot{
				10: {ParentPID: 1, StartToken: "Thu Jul 16 10:00:00 2026", CommandLine: tc.command},
			}))
			if discovery.PipeMode != tc.want {
				t.Fatalf("discovery = %+v, want pipe mode %t", discovery, tc.want)
			}
		})
	}
}

func TestDiscoverHarnessProcessLiteralQuotesInArgv(t *testing.T) {
	const startToken = "Thu Jul 16 10:00:00 2026"
	for _, tc := range []struct {
		name    string
		command string
	}{
		{
			name:    "apostrophe in executable path",
			command: "/Users/o'brien/.local/bin/claude -p hello",
		},
		{
			name:    "apostrophe in prompt argument",
			command: "claude --append-system-prompt you're helpful -p query",
		},
		{
			name:    "double quotes in metadata argument",
			command: `claude --metadata key="value with space" -p query`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovery := discoverHarnessProcess(10, "claude", fakeSnapshots(map[int]processSnapshot{
				10: {ParentPID: 1, StartToken: startToken, CommandLine: tc.command},
			}))
			if discovery.Harness != "claude" || discovery.PID != 10 || !discovery.PipeMode || !discovery.Trackable {
				t.Fatalf("discovery = %+v, want trackable claude pipe process", discovery)
			}
		})
	}
}

func TestCommandFieldsQuotesOnlyAtFieldBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "quoted executable path",
			command: `"/usr/local/bin/claude" -p`,
			want:    []string{"/usr/local/bin/claude", "-p"},
		},
		{
			name:    "literal apostrophes",
			command: "/Users/o'brien/.local/bin/claude --append-system-prompt you're helpful -p query",
			want:    []string{"/Users/o'brien/.local/bin/claude", "--append-system-prompt", "you're", "helpful", "-p", "query"},
		},
		{
			name:    "literal double quotes",
			command: `claude --metadata key="value with space" -p query`,
			want:    []string{"claude", "--metadata", `key="value`, "with", `space"`, "-p", "query"},
		},
		{
			name:    "unbalanced quote opened at boundary",
			command: `'claude -p query`,
			want:    []string{"claude -p query"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandFields(tc.command); !slices.Equal(got, tc.want) {
				t.Fatalf("commandFields(%q) = %#v, want %#v", tc.command, got, tc.want)
			}
		})
	}
}

func TestDiscoverHarnessProcessNestedSelectsDeclaredProducer(t *testing.T) {
	snapshots := fakeSnapshots(map[int]processSnapshot{
		30: {ParentPID: 20, StartToken: "Thu Jul 16 10:03:00 2026", CommandLine: "amx hook-indicator Stop --harness=claude"},
		20: {ParentPID: 10, StartToken: "Thu Jul 16 10:02:00 2026", CommandLine: "claude -p nested"},
		10: {ParentPID: 1, StartToken: "Thu Jul 16 10:01:00 2026", CommandLine: "codex exec"},
	})
	claude := discoverHarnessProcess(30, " Claude ", snapshots)
	if claude.Harness != "claude" || claude.PID != 20 || !claude.PipeMode {
		t.Fatalf("claude discovery = %+v", claude)
	}
	codex := discoverHarnessProcess(30, "codex", snapshots)
	if codex.Harness != "codex" || codex.PID != 10 || !codex.PipeMode {
		t.Fatalf("codex discovery = %+v", codex)
	}
	if mismatched := discoverHarnessProcess(30, "copilot", snapshots); mismatched != (HarnessProcessDiscovery{}) {
		t.Fatalf("mismatched discovery = %+v, want empty", mismatched)
	}
}

func TestDiscoverHarnessProcessIgnoresOwnAndWrapperArguments(t *testing.T) {
	const startToken = "Thu Jul 16 10:00:00 2026"
	for _, tc := range []struct {
		name          string
		intermediates []string
	}{
		{
			name:          "own process space-separated harness flag",
			intermediates: []string{"amx hook-indicator Stop --harness claude"},
		},
		{
			name: "shell wrapper space-separated harness flag",
			intermediates: []string{
				"amx hook-indicator Stop --harness=claude",
				`/bin/sh -c "amx hook-indicator Stop --harness claude"`,
			},
		},
		{
			name: "bare path-like argument in intermediate ancestor",
			intermediates: []string{
				"amx hook-indicator Stop --harness=claude",
				"/usr/local/bin/helper /Users/me/claude --metadata",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshots := make(map[int]processSnapshot, len(tc.intermediates)+1)
			for i, command := range tc.intermediates {
				pid := 100 - i
				snapshots[pid] = processSnapshot{
					ParentPID: pid - 1, StartToken: startToken, CommandLine: command,
				}
			}
			harnessPID := 100 - len(tc.intermediates)
			snapshots[harnessPID] = processSnapshot{
				ParentPID: 1, StartToken: startToken, CommandLine: "/usr/local/bin/claude -p prompt",
			}

			discovery := discoverHarnessProcess(100, "claude", fakeSnapshots(snapshots))
			if discovery.PID != harnessPID || discovery.Harness != "claude" || !discovery.PipeMode || !discovery.Trackable {
				t.Fatalf("discovery = %+v, want actual harness PID %d", discovery, harnessPID)
			}
		})
	}
}

func TestDiscoverHarnessProcessLegacyDeliveryPreservesPipeModeWithoutIdentity(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", t.TempDir()+"/missing-config.toml")

	previousRunner := runPSProcessSnapshot
	parentPID := os.Getpid() + 100000
	runPSProcessSnapshot = func(_ context.Context, pid int) ([]byte, error) {
		switch pid {
		case os.Getpid():
			return []byte(fmt.Sprintf("%d Thu Jul 16 10:03:00 2026 amx hook-indicator Stop\n", parentPID)), nil
		case parentPID:
			return []byte("1 Thu Jul 16 10:02:00 2026 /usr/local/bin/claude -p secret-prompt\n"), nil
		default:
			return nil, os.ErrNotExist
		}
	}
	t.Cleanup(func() { runPSProcessSnapshot = previousRunner })

	for _, tc := range []struct {
		name     string
		producer string
	}{
		{name: "legacy empty producer", producer: ""},
		{name: "unknown producer", producer: "future-harness"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdin, err := os.CreateTemp(t.TempDir(), "legacy-hook-*.json")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stdin.Close() })
			if _, err := stdin.WriteString(`{}`); err != nil {
				t.Fatal(err)
			}
			if _, err := stdin.Seek(0, 0); err != nil {
				t.Fatal(err)
			}

			client := &fakeDaemonClient{resp: daemon.Response{OK: true}}
			deliverFromStdinWithDeps(
				"Stop", 42, tc.producer, "/tmp/amx", DeliveryOptions{RequestID: "legacy-pipe-test"}, stdin, client,
			)
			if client.calls != 1 {
				t.Fatalf("daemon calls = %d, want 1", client.calls)
			}
			request := client.reqs[0]
			if !request.PipeMode || request.HarnessProcess != nil {
				t.Fatalf("request = %+v, want pipe mode without process ownership", request)
			}
		})
	}
}

func TestHarnessProcessDiscoveryDoesNotTransportCommandText(t *testing.T) {
	const secretPrompt = "super-secret-command-line-prompt"
	discovery := discoverHarnessProcess(10, "claude", fakeSnapshots(map[int]processSnapshot{
		10: {
			ParentPID: 1, StartToken: "Thu Jul 16 10:20:30 2026",
			CommandLine: "claude -p " + secretPrompt,
		},
	}))
	wire, err := json.Marshal(daemon.Request{
		Type: daemon.TypeHookEvent, ProducerHarness: "claude", HarnessProcess: discovery.Identity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), secretPrompt) || strings.Contains(string(wire), "command_line") {
		t.Fatalf("request leaked transient process evidence: %s", wire)
	}
}

func TestDiscoverHarnessProcessIncompleteAndFailureCases(t *testing.T) {
	t.Run("missing start token still classifies pipe mode", func(t *testing.T) {
		discovery := discoverHarnessProcess(10, "claude", fakeSnapshots(map[int]processSnapshot{
			10: {ParentPID: 1, CommandLine: "claude -p secret-prompt"},
		}))
		if !discovery.PipeMode || discovery.Trackable || discovery.Identity() != nil {
			t.Fatalf("discovery = %+v", discovery)
		}
	})

	t.Run("snapshot failure", func(t *testing.T) {
		got := discoverHarnessProcess(10, "claude", func(int) (processSnapshot, bool) {
			return processSnapshot{}, false
		})
		if got != (HarnessProcessDiscovery{}) {
			t.Fatalf("discovery = %+v", got)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		calls := 0
		got := discoverHarnessProcess(10, "claude", func(pid int) (processSnapshot, bool) {
			calls++
			return processSnapshot{ParentPID: pid, CommandLine: "/bin/zsh"}, true
		})
		if got != (HarnessProcessDiscovery{}) || calls != 1 {
			t.Fatalf("discovery = %+v calls = %d", got, calls)
		}
	})
}

func TestParseProcessSnapshot(t *testing.T) {
	got, ok := parseProcessSnapshot("  99 Thu Jul 16 10:20:30 2026 /usr/local/bin/claude -p secret prompt\n")
	if !ok || got.ParentPID != 99 || got.StartToken != "Thu Jul 16 10:20:30 2026" ||
		got.CommandLine != "/usr/local/bin/claude -p secret prompt" {
		t.Fatalf("snapshot = %+v ok=%t", got, ok)
	}
	for _, malformed := range []string{"", "nope Thu Jul 16 10:20:30 2026 claude", "99 Thu Jul 16"} {
		if got, ok := parseProcessSnapshot(malformed); ok {
			t.Fatalf("parseProcessSnapshot(%q) = %+v, want failure", malformed, got)
		}
	}
}

func TestPsProcessSnapshotHonorsTimeout(t *testing.T) {
	previousTimeout := psCommandTimeout
	previousRunner := runPSProcessSnapshot
	psCommandTimeout = 20 * time.Millisecond
	runPSProcessSnapshot = func(ctx context.Context, _ int) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() {
		psCommandTimeout = previousTimeout
		runPSProcessSnapshot = previousRunner
	})

	started := time.Now()
	if snapshot, ok := psProcessSnapshot(42); ok {
		t.Fatalf("snapshot = %+v, want timeout failure", snapshot)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("psProcessSnapshot exceeded timeout budget: %v", elapsed)
	}
}

func TestPsProcessSnapshotPinsTimezoneAcrossAmbientTZ(t *testing.T) {
	var tokens []string
	for _, timezone := range []string{"Asia/Tokyo", "America/Los_Angeles"} {
		t.Setenv("TZ", timezone)
		snapshot, ok := psProcessSnapshot(os.Getpid())
		if !ok {
			t.Fatalf("psProcessSnapshot failed under TZ=%s", timezone)
		}
		tokens = append(tokens, snapshot.StartToken)
	}
	if tokens[0] != tokens[1] {
		t.Fatalf("start token changed across ambient timezones: %q != %q", tokens[0], tokens[1])
	}

	joined := "\n" + strings.Join(stableProcessLocaleEnv(), "\n") + "\n"
	if strings.Count(joined, "\nTZ=UTC\n") != 1 || strings.Contains(joined, "TZ=America/Los_Angeles") {
		t.Fatalf("stable process environment did not replace ambient TZ: %q", joined)
	}
}

// fakeTree builds walkTree-compatible cmdline and ppid functions from a map.
func fakeTree(cmdlines map[int]string, ppids map[int]int) (func(int) string, func(int) int) {
	return func(pid int) string { return cmdlines[pid] },
		func(pid int) int {
			if p, ok := ppids[pid]; ok {
				return p
			}
			return 1
		}
}

func TestWalkTree_NormalProcess(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{1000: "/bin/zsh", 999: "launchd"},
		map[int]int{1000: 999},
	)
	if walkTree(1000, cmd, ppid) {
		t.Error("expected pipe_mode=false for normal process tree")
	}
}

func TestWalkTree_ClaudeWithDashP(t *testing.T) {
	// Tree: 1002 (hook) → 1001 (shell) → 1000 (claude -p)
	cmd, ppid := fakeTree(
		map[int]string{
			1002: "amx hook-indicator active",
			1001: "/bin/zsh",
			1000: "node /usr/local/bin/claude -p 'summarize'",
		},
		map[int]int{1002: 1001, 1001: 1000, 1000: 1},
	)
	if !walkTree(1002, cmd, ppid) {
		t.Error("expected pipe_mode=true when ancestor has claude -p")
	}
}

func TestWalkTree_ClaudeWithPrint(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{
			1001: "amx hook-indicator stop",
			1000: "node /usr/local/bin/claude --print",
		},
		map[int]int{1001: 1000, 1000: 1},
	)
	if !walkTree(1001, cmd, ppid) {
		t.Error("expected pipe_mode=true when ancestor has claude --print")
	}
}

func TestWalkTree_CodexExec(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{
			1001: "amx hook-indicator Stop",
			1000: "/usr/local/bin/codex exec --json",
		},
		map[int]int{1001: 1000, 1000: 1},
	)
	if !walkTree(1001, cmd, ppid) {
		t.Error("expected pipe_mode=true when ancestor has codex exec")
	}
}

func TestWalkTree_StopsAtRoot(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{1: "launchd"},
		map[int]int{},
	)
	if walkTree(1, cmd, ppid) {
		t.Error("expected pipe_mode=false when starting at root")
	}
}

// TestWalkTree_FlagPositionMatters verifies that a -p flag belonging to an
// unrelated ancestor command (e.g. `claude-lint -p`) does not trigger pipe
// mode for an interactive child claude session. Under the old substring-based
// check, the parent's "claude" substring + "-p" was enough to flag the whole
// tree as pipe mode.
func TestWalkTree_FlagPositionMatters(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{
			1001: "claude",         // interactive child, no pipe flag
			1000: "claude-lint -p", // unrelated ancestor with its own -p flag
		},
		map[int]int{1001: 1000, 1000: 1},
	)
	if walkTree(1001, cmd, ppid) {
		t.Error("expected pipe_mode=false: ancestor's -p must not propagate to a child interactive claude")
	}
}

func TestWalkTree_CycleProtection(t *testing.T) {
	count := 0
	cmd := func(_ int) string {
		count++
		if count > 100 {
			panic("infinite loop in walkTree")
		}
		return "something"
	}
	ppid := func(pid int) int { return pid } // self-referential cycle
	if walkTree(100, cmd, ppid) {
		t.Error("expected pipe_mode=false for non-claude cycle")
	}
}

// TestPsCommandLineHonorsTimeout pins the contract that the indicator's
// process-tree walker cannot hang on a wedged `ps`. Hooks invoke this on
// every Claude/Copilot event; without the bounded context, a stalled or
// audit-paused `ps` would freeze the hook script. The test points the
// indicator at a non-existent PID with the timeout shrunk so the test
// itself stays fast — the production path is identical apart from the
// timeout value.
func TestPsCommandLineHonorsTimeout(t *testing.T) {
	prev := psCommandTimeout
	psCommandTimeout = 50 * time.Millisecond
	t.Cleanup(func() { psCommandTimeout = prev })

	start := time.Now()
	// PID 999999 almost certainly does not exist; ps returns immediately with
	// an error. The point of the test is to assert no hang under the timeout.
	out := psCommandLine(999999)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("psCommandLine blocked beyond timeout: elapsed=%v", elapsed)
	}
	// A non-existent PID should yield an empty string (the function swallows
	// the error and returns "").
	if out != "" {
		t.Logf("psCommandLine(999999) returned %q; non-fatal but unexpected", out)
	}

	start = time.Now()
	parent := psParentPID(999999)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("psParentPID blocked beyond timeout: elapsed=%v", elapsed)
	}
	if parent != -1 {
		t.Logf("psParentPID(999999) returned %d; non-fatal but unexpected", parent)
	}
}

func TestLooksLikeClaudePipe(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"node /usr/local/bin/claude -p 'do thing'", true},
		{"node /usr/local/bin/claude --print 'do thing'", true},
		{"node /usr/local/bin/claude --help", false},
		{"/bin/zsh", false},
		{"", false},
		{"claude", false},
		{"claude -p", true},
		{"claude -p=short", true},
		{"claude --print", true},
		{"claude --print-output", false}, // --print-output is not --print
		{"claude --print-output=true", false},
		// Exact-command match: basename must equal "claude" or "copilot".
		{"claude-lint", false},
		{"claude-lint -p", false},        // basename is claude-lint, not claude
		{"/opt/claude-tools/foo", false}, // basename is foo, not claude
		{"/opt/claude-tools/foo -p", false},
		{"/usr/bin/claude -p", true},              // path-prefixed claude is fine
		{"/usr/local/bin/copilot --prompt", true}, // path-prefixed copilot is fine
		{"/usr/local/bin/codex review", true},     // path-prefixed codex is fine
		{"copilot", false},
		{"copilot -p", true},
		{"copilot -p=short", true},
		{"copilot --prompt", true},
		{"copilot --prompt=short", true},
		{"copilot --print", false}, // --print is claude-only; copilot uses --prompt
		{"codex", false},
		{"codex exec", true},
		{"codex exec=1", false},
		{"codex e", true},
		{"codex review", true},
		{"codex run", false},
		{"codex-exec", false},
		{`"/usr/local/bin/claude" -p`, true},
		{`'/usr/local/bin/copilot' --prompt`, true},
		{`"C:\Program Files\Codex\codex.exe" exec`, true},
		// Flag must follow the command in the same cmdline.
		{"-p claude", false}, // -p before claude
		{"some-tool -p claude", false},
		{"amx hook-indicator Stop --harness claude", false},
		{`/bin/sh -c "amx hook-indicator Stop --harness claude"`, false},
		{"helper /Users/me/claude", false},
		// POSIX `--` terminator stops the option scan so positional `-p` after
		// `--` is not treated as a pipe-mode flag.
		{"claude -- -p", false},
		{"claude -- --print", false},
		{"copilot -- -p prompt", false},
		{"codex -- review", false},
	}
	for _, tc := range cases {
		got := looksLikeClaudePipe(tc.cmd)
		if got != tc.want {
			t.Errorf("looksLikeClaudePipe(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}
