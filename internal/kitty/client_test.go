package kitty_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("loading fixture %s: %v", name, err)
	}
	return data
}

func TestListWindows_ParsesJSON(t *testing.T) {
	fixture := loadFixture(t, "kitty_ls.json")
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	windows, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 OS window, got %d", len(windows))
	}
	if len(windows[0].Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(windows[0].Tabs))
	}

	tab1 := windows[0].Tabs[0]
	if tab1.Title != "P:dotfiles" {
		t.Errorf("tab 1 title: got %q, want %q", tab1.Title, "P:dotfiles")
	}
	win := tab1.Windows[0]
	if win.UserVars.Account != "personal" {
		t.Errorf("user var cc_account: got %q, want %q", win.UserVars.Account, "personal")
	}
}

func TestListWindows_Empty(t *testing.T) {
	fixture := loadFixture(t, "kitty_ls_empty.json")
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	windows, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows[0].Tabs) != 0 {
		t.Errorf("expected 0 tabs, got %d", len(windows[0].Tabs))
	}
}

func TestListWindows_InvalidJSON(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": []byte("not-json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	_, err := client.ListWindows()
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
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

func TestCloseTab_MatchExpr(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	_ = client.CloseTab("id:42")

	found := false
	for _, c := range runner.Calls {
		for _, a := range c.Args {
			if a == "--match=id:42" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected --match=id:42 in kitten @ close-tab args")
	}
}

func TestMoveTab_CallsKitten(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.MoveTab("id:5", -2); err != nil {
		t.Fatalf("MoveTab: %v", err)
	}
	if len(runner.Calls) != 3 {
		t.Fatalf("expected 3 kitten calls, got %d: %v", len(runner.Calls), runner.Calls)
	}

	// First call: focus-tab --match=id:5
	focus := runner.Calls[0]
	if focus.Name != "kitten" {
		t.Errorf("call 0: expected 'kitten', got %q", focus.Name)
	}
	if !containsArg(focus.Args, "focus-tab") || !containsArg(focus.Args, "--match=id:5") {
		t.Errorf("call 0: expected focus-tab --match=id:5, got %v", focus.Args)
	}

	// Calls 1 and 2: action move_tab_backward
	for i := 1; i <= 2; i++ {
		c := runner.Calls[i]
		if c.Name != "kitten" {
			t.Errorf("call %d: expected 'kitten', got %q", i, c.Name)
		}
		if !containsArg(c.Args, "action") || !containsArg(c.Args, "move_tab_backward") {
			t.Errorf("call %d: expected action move_tab_backward, got %v", i, c.Args)
		}
	}
}

func TestMoveTab_ZeroDelta_NoCalls(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.MoveTab("id:5", 0); err != nil {
		t.Fatalf("MoveTab: %v", err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("expected 0 kitten calls for delta=0, got %d: %v", len(runner.Calls), runner.Calls)
	}
}

func TestMoveTab_PositiveDelta_ForwardAction(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.MoveTab("id:7", 3); err != nil {
		t.Fatalf("MoveTab: %v", err)
	}
	if len(runner.Calls) != 4 {
		t.Fatalf("expected 4 kitten calls, got %d", len(runner.Calls))
	}
	for i := 1; i <= 3; i++ {
		if !containsArg(runner.Calls[i].Args, "move_tab_forward") {
			t.Errorf("call %d: expected move_tab_forward, got %v", i, runner.Calls[i].Args)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
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

// TestSetTabTitle_UsesDashDashSeparator verifies that the title is passed
// after `--` so that a leading-dash title is not parsed by kitten as a flag.
func TestSetTabTitle_UsesDashDashSeparator(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.SetTabTitle("id:7", "--definitely-a-title"); err != nil {
		t.Fatalf("SetTabTitle: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}

	args := runner.Calls[0].Args
	// The argv must be: @ set-tab-title --match=id:7 -- --definitely-a-title
	if len(args) < 2 || args[len(args)-2] != "--" || args[len(args)-1] != "--definitely-a-title" {
		t.Fatalf("expected trailing `-- --definitely-a-title`, got %v", args)
	}
}

func TestLaunchOverlayPager_Args(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	_ = client.LaunchOverlayPager("id:7")

	if len(runner.Calls) == 0 {
		t.Fatal("expected kitten to be called")
	}
	args := runner.Calls[0].Args
	wantArgs := map[string]bool{
		"--type=overlay":                  false,
		"--source-window=id:7":            false,
		"--stdin-source=@screen_scrollback": false,
	}
	for _, a := range args {
		if _, ok := wantArgs[a]; ok {
			wantArgs[a] = true
		}
	}
	for arg, found := range wantArgs {
		if !found {
			t.Errorf("expected %q in kitten launch args", arg)
		}
	}
}

func TestSetUserVars_Argv(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.SetUserVars("id:7", map[string]string{
		"cc_account": "work",
		"amx_profile": "work",
	})
	if err != nil {
		t.Fatalf("SetUserVars: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	got := runner.Calls[0].Args
	want := []string{"@", "set-user-vars", "--match=id:7", "amx_profile=work", "cc_account=work"}
	if len(got) != len(want) {
		t.Fatalf("argv length: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSetUserVars_EmptyIsNoOp(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.SetUserVars("id:7", nil); err != nil {
		t.Fatalf("SetUserVars(nil): %v", err)
	}
	if err := client.SetUserVars("id:7", map[string]string{}); err != nil {
		t.Fatalf("SetUserVars(empty): %v", err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("expected 0 kitten calls for empty vars, got %d: %v", len(runner.Calls), runner.Calls)
	}
}

func TestGetText_ReturnsText(t *testing.T) {
	want := "Hello from terminal\nLine 2\n"
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ get-text --extent=screen --match=id:7": []byte(want),
	})
	client := kitty.NewClientWithSocket(runner, "")

	got, err := client.GetText("id:7", "screen")
	if err != nil {
		t.Fatalf("GetText: %v", err)
	}
	if got != want {
		t.Errorf("GetText: got %q, want %q", got, want)
	}
}

// Verify UserVars JSON unmarshalling works correctly.
func TestUserVarsUnmarshal(t *testing.T) {
	raw := `{"cc_account":"work","cc_config_dir":"~/.config/claude-work","cc_flags":"--dangerously-skip-permissions","cc_pw":"","cc_env":"","cc_groups":"backend","cc_mute":"","cc_cwd":"/home/user/api"}`
	var uv kitty.UserVars
	if err := json.Unmarshal([]byte(raw), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if uv.Account != "work" {
		t.Errorf("Account: got %q, want %q", uv.Account, "work")
	}
	if uv.Groups != "backend" {
		t.Errorf("Groups: got %q, want %q", uv.Groups, "backend")
	}
}

func TestUserVarsUnmarshal_ExplicitFlagsPresence(t *testing.T) {
	raw := `{"cc_account":"work","cc_flags":"--account-flag","cc_explicit_flags":"","cc_cwd":"/home/user/api"}`
	var uv kitty.UserVars
	if err := json.Unmarshal([]byte(raw), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if uv.ExplicitFlags != "" {
		t.Errorf("ExplicitFlags: got %q, want empty", uv.ExplicitFlags)
	}
	if !uv.ExplicitFlagsSet {
		t.Error("expected ExplicitFlagsSet to be true when cc_explicit_flags is present")
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

// TestSetTabColor_AllEmptyIsNoOp pins that passing four empty color strings
// does not result in a kitten invocation. The previous behavior emitted
// `kitten @ set-tab-color --match=...` with no color args, which kitten
// rejects with an error — a silent waste of a subprocess that surfaces as a
// confusing failure to the caller.
func TestSetTabColor_AllEmptyIsNoOp(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.SetTabColor("id:7", "", "", "", ""); err != nil {
		t.Fatalf("SetTabColor: %v", err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("expected 0 kitten calls when all colors empty, got %d: %v", len(runner.Calls), runner.Calls)
	}
}

// TestSetTabColor_PartialColorsStillCalls confirms a single non-empty color
// still triggers the kitten invocation.
func TestSetTabColor_PartialColorsStillCalls(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.SetTabColor("id:7", "#abcdef", "", "", ""); err != nil {
		t.Fatalf("SetTabColor: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	if !containsArg(runner.Calls[0].Args, "active_bg=#abcdef") {
		t.Errorf("expected active_bg=#abcdef in args, got %v", runner.Calls[0].Args)
	}
}

func TestUserVarsUnmarshal_ExplicitFlagsAbsent(t *testing.T) {
	raw := `{"cc_account":"work","cc_flags":"--legacy-flag","cc_cwd":"/home/user/api"}`
	var uv kitty.UserVars
	if err := json.Unmarshal([]byte(raw), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if uv.ExplicitFlagsSet {
		t.Error("expected ExplicitFlagsSet to be false when cc_explicit_flags is absent")
	}
}
