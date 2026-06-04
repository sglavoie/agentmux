package kitty_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestLaunchTab_CallsKitten(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.LaunchTab(kitty.LaunchOpts{
		Title:     "W:api",
		Directory: "~/projects/api",
		Env:       map[string]string{"DEBUG": "1"},
		UserVars:  map[string]string{"cc_account": "work"},
		Cmdline:   []string{"claude"},
	})
	if err != nil {
		t.Fatalf("LaunchTab: %v", err)
	}
	if len(runner.Calls) == 0 {
		t.Fatal("expected kitten to be called")
	}
	if runner.Calls[0].Name != "kitten" {
		t.Errorf("expected 'kitten', got %q", runner.Calls[0].Name)
	}
}

// TestLaunchTab_SeparatesCmdlineWithDashDash guards the contract that the
// Cmdline tokens are passed after a `--` end-of-options marker. Without the
// marker, a Cmdline whose first token begins with a dash (or contains a
// kitten-recognised flag) would be silently consumed by `kitten @ launch`'s
// argument parser instead of being passed to the launched program.
func TestLaunchTab_SeparatesCmdlineWithDashDash(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.LaunchTab(kitty.LaunchOpts{
		Title:   "W:api",
		Cmdline: []string{"--weird-shell", "-lic", "exec claude"},
	})
	if err != nil {
		t.Fatalf("LaunchTab: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}

	args := runner.Calls[0].Args
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 {
		t.Fatalf("expected `--` separator in argv, got %v", args)
	}
	rest := args[sepIdx+1:]
	want := []string{"--weird-shell", "-lic", "exec claude"}
	if len(rest) != len(want) {
		t.Fatalf("argv after --: got %v, want %v", rest, want)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Errorf("argv[after --][%d]: got %q, want %q", i, rest[i], want[i])
		}
	}
}

// TestLaunchTab_NoCmdlineSkipsSeparator confirms we do not emit a trailing
// `--` when there is nothing positional to pass; doing so would be harmless
// to kitten but would clutter the argv used by command-key based mock lookups
// and make debug logs noisier than necessary.
func TestLaunchTab_NoCmdlineSkipsSeparator(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.LaunchTab(kitty.LaunchOpts{Title: "W:api"}); err != nil {
		t.Fatalf("LaunchTab: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	for _, a := range runner.Calls[0].Args {
		if a == "--" {
			t.Errorf("did not expect a trailing `--` when Cmdline is empty, args=%v", runner.Calls[0].Args)
		}
	}
}

// TestLaunchTab_DeterministicArgvOrder pins that --env and --var arguments are
// emitted in sorted key order. Map iteration order in Go is randomized, so
// without sorting, two LaunchTab invocations with identical opts would emit
// different argv sequences — which would break command-key based mock lookups
// and make debug logs harder to diff across runs.
func TestLaunchTab_DeterministicArgvOrder(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.LaunchTab(kitty.LaunchOpts{
		Title: "W:api",
		Env: map[string]string{
			"ZED":   "z",
			"ALPHA": "a",
			"MID":   "m",
		},
		UserVars: map[string]string{
			"cc_account":  "work",
			"amx_profile": "work",
			"amx_harness": "claude",
		},
	})
	if err != nil {
		t.Fatalf("LaunchTab: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}

	args := runner.Calls[0].Args
	var envArgs, varArgs []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--env="):
			envArgs = append(envArgs, a)
		case strings.HasPrefix(a, "--var="):
			varArgs = append(varArgs, a)
		}
	}
	wantEnv := []string{"--env=ALPHA=a", "--env=MID=m", "--env=ZED=z"}
	wantVars := []string{"--var=amx_harness=claude", "--var=amx_profile=work", "--var=cc_account=work"}
	if len(envArgs) != len(wantEnv) {
		t.Fatalf("env args: got %v, want %v", envArgs, wantEnv)
	}
	for i := range wantEnv {
		if envArgs[i] != wantEnv[i] {
			t.Errorf("env arg %d: got %q, want %q", i, envArgs[i], wantEnv[i])
		}
	}
	if len(varArgs) != len(wantVars) {
		t.Fatalf("var args: got %v, want %v", varArgs, wantVars)
	}
	for i := range wantVars {
		if varArgs[i] != wantVars[i] {
			t.Errorf("var arg %d: got %q, want %q", i, varArgs[i], wantVars[i])
		}
	}
}

func TestLaunchOverlay_ArgvContract(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.LaunchOverlay(kitty.LaunchOpts{
		Title:     "loop tail",
		Directory: "/tmp/work",
		Hold:      true,
		Env: map[string]string{
			"ZED":   "z",
			"ALPHA": "a",
		},
		UserVars: map[string]string{
			"run":     "42",
			"account": "work",
		},
		Cmdline: []string{"--dash-command", "tail", "/tmp/plan"},
	})
	if err != nil {
		t.Fatalf("LaunchOverlay: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}

	want := []string{
		"@",
		"launch",
		"--type=overlay",
		"--no-response",
		"--hold",
		"--title=loop tail",
		"--cwd=/tmp/work",
		"--env=ALPHA=a",
		"--env=ZED=z",
		"--var=account=work",
		"--var=run=42",
		"--",
		"--dash-command",
		"tail",
		"/tmp/plan",
	}
	if !reflect.DeepEqual(runner.Calls[0].Args, want) {
		t.Fatalf("LaunchOverlay args:\n got %v\nwant %v", runner.Calls[0].Args, want)
	}
}

func TestLaunchOverlay_NoCmdlineSkipsSeparatorAndHold(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.LaunchOverlay(kitty.LaunchOpts{Title: "loop tail"}); err != nil {
		t.Fatalf("LaunchOverlay: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	for _, a := range runner.Calls[0].Args {
		if a == "--" {
			t.Errorf("did not expect a trailing `--` when Cmdline is empty, args=%v", runner.Calls[0].Args)
		}
		if a == "--hold" {
			t.Errorf("did not expect `--hold` unless Hold is true, args=%v", runner.Calls[0].Args)
		}
	}
}
