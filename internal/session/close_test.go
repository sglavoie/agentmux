package session_test

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
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

// TestPollClosed_NoSleepOnFirstSuccess verifies that PollClosed checks the
// tab list before its first sleep, so a tab that has already disappeared by
// the time we look does not cost the caller closePollDelay of wall time. The
// previous implementation slept first and checked after, wasting ~100 ms on
// every fast close (e.g. Restart's old-tab cleanup) and slowing the test
// suite proportionally.
func TestPollClosed_NoSleepOnFirstSuccess(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls_empty.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	start := time.Now()
	if err := session.PollClosed(client, 999); err != nil {
		t.Fatalf("PollClosed with absent tab: got %v, want nil", err)
	}
	elapsed := time.Since(start)
	// The fixed version returns essentially immediately on first iteration.
	// Allow generous headroom for slow CI; pre-fix code would always take
	// ≥ closePollDelay (~100 ms) before returning.
	if elapsed > 50*time.Millisecond {
		t.Errorf("PollClosed slept before first check: elapsed=%v (expected <50ms when tab already gone)", elapsed)
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
