package kitty_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

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
