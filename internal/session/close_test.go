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
	t.Parallel()

	// ls returns empty (tab 4 is already gone immediately).
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls_empty.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	err := session.Close(kitty.NewBackend(client), kitty.GroupRef(4))
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
	t.Parallel()

	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls_empty.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	err := session.PollClosed(kitty.NewBackend(client), kitty.GroupRef(999))
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
	t.Parallel()

	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls_empty.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	start := time.Now()
	if err := session.PollClosed(kitty.NewBackend(client), kitty.GroupRef(999)); err != nil {
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
	t.Parallel()

	// Tab 4 is present in kitty_ls.json (W:api has id=4).
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, "kitty_ls.json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	err := session.PollClosed(kitty.NewBackend(client), kitty.GroupRef(4))
	if !errors.Is(err, session.ErrCloseTimeout) {
		t.Errorf("PollClosed with persistent tab: got %v, want ErrCloseTimeout", err)
	}
}

// TestCloseObservedDistinguishesTheThreeEndings is the reason the observation
// exists: two of these three endings return no error, and only one of them means
// the tab actually went away.
func TestCloseObservedDistinguishesTheThreeEndings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ls       []byte
		tabID    int
		want     session.CloseObservation
		wantErr  error
		confirms bool
	}{
		{
			name:     "a successful listing without the tab is the confirmed close",
			ls:       loadFixture(t, "kitty_ls_empty.json"),
			tabID:    999,
			want:     session.CloseObservedGone,
			confirms: true,
		},
		{
			// The listing-error branch that assumes the tab closed. It still
			// returns nil — Restart depends on that — but it may not claim the
			// tab is gone, because nothing looked.
			name:  "a failed listing is unobserved rather than closed",
			ls:    []byte("not-json"),
			tabID: 999,
			want:  session.CloseUnobserved,
		},
		{
			name:    "a tab still present when the polls run out is unobserved",
			ls:      loadFixture(t, "kitty_ls.json"),
			tabID:   4,
			want:    session.CloseUnobserved,
			wantErr: session.ErrCloseTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := kitty.NewMockRunner(map[string][]byte{"kitten @ ls": tc.ls})
			client := kitty.NewClientWithSocket(runner, "")

			observation, err := session.CloseObserved(kitty.NewBackend(client), kitty.GroupRef(tc.tabID))
			if observation != tc.want {
				t.Errorf("observation = %q, want %q", observation, tc.want)
			}
			if observation.Confirmed() != tc.confirms {
				t.Errorf("Confirmed() = %v, want %v", observation.Confirmed(), tc.confirms)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestCloseObservedReportsTheCloseTabFailure keeps the failure of the request
// itself distinct from the failure to observe its effect.
func TestCloseObservedReportsTheCloseTabFailure(t *testing.T) {
	t.Parallel()

	client := kitty.NewClientWithSocket(failingCloseRunner{}, "")

	observation, err := session.CloseObserved(kitty.NewBackend(client), kitty.GroupRef(4))
	if err == nil {
		t.Fatal("expected the close-tab error to be reported")
	}
	if observation != session.CloseUnobserved {
		t.Errorf("observation = %q, want %q", observation, session.CloseUnobserved)
	}
}

// failingCloseRunner fails every kitten call, which is the closest the mock
// runner gets to a Kitty that will not accept a close-tab.
type failingCloseRunner struct{}

func (failingCloseRunner) Run(string, ...string) ([]byte, []byte, error) {
	return nil, []byte("no such tab"), errors.New("kitten failed")
}
