package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestActionErrorTextNamesAPositionalParameterWithoutAFlag(t *testing.T) {
	err := ActionErrorText(&actions.Error{
		Code: "ambiguous_conversation", Message: "2 conversations match",
		MissingParameters: []actions.ParameterSchema{{Name: "session-id", Description: "a longer prefix", Required: true, Positional: true}},
	})
	if got, want := err.Error(), "2 conversations match\nmissing <session-id>: a longer prefix"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
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

func TestActionErrorTextAlignsCandidatesAndDropsARepeatedTitle(t *testing.T) {
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Skip(homeErr)
	}
	err := ActionErrorText(&actions.Error{
		Code: "not_found", Message: `no configured project matches title "x"`,
		Candidates: []actions.Candidate{
			{ID: "work/🐱amx", DisplayName: "🐱amx", CanonicalPath: filepath.Join(home, "amx") + "/"},
			{ID: "personal/API", DisplayName: "API", CanonicalPath: "/srv/api"},
		},
	})
	want := "no configured project matches title \"x\"\ncandidates:\n" +
		"  work/🐱amx    ~/amx\n" +
		"  personal/API  /srv/api"
	if got := err.Error(); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestActionErrorTextCapsALongCandidateList(t *testing.T) {
	cause := &actions.Error{Code: "not_found", Message: "no match"}
	for i := range candidateTextLimit + 3 {
		cause.Candidates = append(cause.Candidates, actions.Candidate{ID: fmt.Sprintf("c%02d", i)})
	}
	text := ActionErrorText(cause).Error()
	if strings.Contains(text, fmt.Sprintf("c%02d", candidateTextLimit)) {
		t.Fatalf("text lists a candidate past the limit:\n%s", text)
	}
	if !strings.HasSuffix(text, "\n  … and 3 more (--json lists every candidate)") {
		t.Fatalf("text does not count the hidden candidates:\n%s", text)
	}
}

func TestActionErrorTextLeavesAPlainMessageAlone(t *testing.T) {
	if got := ActionErrorText(&actions.Error{Code: "unavailable", Message: "worktree is active"}).Error(); got != "worktree is active" {
		t.Fatalf("text = %q", got)
	}
}
