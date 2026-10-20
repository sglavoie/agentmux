package worktree

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A Git failure keeps both its classification and its cause, so a caller can
// tell a timeout from a broken repository with errors.Is instead of parsing
// the message.
func TestGitFailureKeepsItsCauseBehindTheSentinel(t *testing.T) {
	t.Parallel()

	inventory := &Inventory{git: func(context.Context, string, time.Duration, ...string) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}}
	_, err := inventory.gitRefExists(context.Background(), "/repo", "refs/heads/main")
	if !errors.Is(err, ErrGitUnavailable) {
		t.Fatalf("err = %v, want ErrGitUnavailable", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the timeout still reachable", err)
	}
}
