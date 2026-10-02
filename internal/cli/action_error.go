package cli

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
)

// ActionErrorText is the text-mode form of a typed action failure. A JSON
// caller already receives the missing parameters and candidates as structured
// fields; without this, a text caller saw only the bare message — a
// confirmation prompt such as "remove this worktree?" printed as an error with
// no word on how to answer it, and an ambiguous selector with no list of what
// it matched. The way forward is appended to the message so the error stays
// a single value the caller returns.
func ActionErrorText(err *actions.Error) error {
	var b strings.Builder
	b.WriteString(err.Message)
	for _, parameter := range err.MissingParameters {
		if parameter.Name == "yes" {
			b.WriteString("\nre-run with --yes to confirm")
			continue
		}
		fmt.Fprintf(&b, "\nmissing --%s", parameter.Name)
		if parameter.Description != "" {
			fmt.Fprintf(&b, ": %s", parameter.Description)
		}
	}
	if len(err.Candidates) > 0 {
		b.WriteString("\ncandidates:")
		for _, candidate := range err.Candidates {
			fmt.Fprintf(&b, "\n  %s", candidate.ID)
			if candidate.DisplayName != "" && candidate.DisplayName != candidate.ID {
				fmt.Fprintf(&b, "  %s", candidate.DisplayName)
			}
			if candidate.CanonicalPath != "" {
				fmt.Fprintf(&b, "  %s", candidate.CanonicalPath)
			}
		}
	}
	return &textActionError{text: b.String(), cause: err}
}

// textActionError keeps the original *actions.Error reachable through
// errors.As while reporting the expanded text.
type textActionError struct {
	text  string
	cause *actions.Error
}

func (e *textActionError) Error() string { return e.text }
func (e *textActionError) Unwrap() error { return e.cause }
