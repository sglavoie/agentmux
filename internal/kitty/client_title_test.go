package kitty_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

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
		"--type=overlay":                    false,
		"--source-window=id:7":              false,
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
