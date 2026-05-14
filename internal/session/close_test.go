package session_test

import (
	"errors"
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// TestClose_CallsCloseTab verifies that Close sends kitten @ close-tab.
func TestClose_CallsCloseTab(t *testing.T) {
	// ls returns empty (tab 4 is already gone immediately).
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls_empty.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	err := session.Close(client, 4)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	found := false
	for _, c := range runner.Calls {
		for _, a := range c.Args {
			if a == "close-tab" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected kitten @ close-tab to be called")
	}
}

// TestPollClosed_TabGoneImmediately returns nil when the tab is not in ls.
func TestPollClosed_TabGoneImmediately(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls_empty.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	err := session.PollClosed(client, 999)
	if err != nil {
		t.Errorf("PollClosed with absent tab: got %v, want nil", err)
	}
}

// TestPollClosed_Timeout returns ErrCloseTimeout when tab persists.
func TestPollClosed_Timeout(t *testing.T) {
	// Tab 4 is present in kitty_ls.json (W:api has id=4).
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	err := session.PollClosed(client, 4)
	if !errors.Is(err, session.ErrCloseTimeout) {
		t.Errorf("PollClosed with persistent tab: got %v, want ErrCloseTimeout", err)
	}
}
