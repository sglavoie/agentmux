package cli

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
)

func TestActionErrorTextNamesTheConfirmationFlag(t *testing.T) {
	cause := &actions.Error{
		Code: "confirmation_required", Message: "remove this worktree?",
		MissingParameters: []actions.ParameterSchema{{Name: "yes", Required: true}},
	}
	err := ActionErrorText(cause)
	if got, want := err.Error(), "remove this worktree?\nre-run with --yes to confirm"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	var unwrapped *actions.Error
	if !errors.As(err, &unwrapped) || unwrapped != cause {
		t.Fatal("ActionErrorText must keep the typed action error reachable")
	}
}

func TestActionErrorTextListsCandidates(t *testing.T) {
	err := ActionErrorText(&actions.Error{
		Code: "ambiguous", Message: `selector "api" matches 2 worktrees`,
		Candidates: []actions.Candidate{
			{ID: "wt-1", DisplayName: "api-fix", CanonicalPath: "/w/api-fix"},
			{ID: "wt-2", DisplayName: "wt-2"},
		},
	})
	want := "selector \"api\" matches 2 worktrees\ncandidates:\n  wt-1  api-fix  /w/api-fix\n  wt-2"
	if got := err.Error(); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestActionErrorTextLeavesAPlainMessageAlone(t *testing.T) {
	if got := ActionErrorText(&actions.Error{Code: "unavailable", Message: "worktree is active"}).Error(); got != "worktree is active" {
		t.Fatalf("text = %q", got)
	}
}
