package cli

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/layout"
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
		if parameter.Positional {
			fmt.Fprintf(&b, "\nmissing <%s>", parameter.Name)
		} else {
			fmt.Fprintf(&b, "\nmissing --%s", parameter.Name)
		}
		if parameter.Description != "" {
			fmt.Fprintf(&b, ": %s", parameter.Description)
		}
	}
	if len(err.Candidates) > 0 {
		b.WriteString("\ncandidates:")
		writeCandidates(&b, err.Candidates)
	}
	return &textActionError{text: b.String(), cause: err}
}

// candidateTextLimit bounds the candidates a text caller is shown. A selector
// that matched nothing near may offer every configured project, and forty
// lines push the message itself off the screen; --json still carries them all.
const candidateTextLimit = 10

// writeCandidates lists candidates as aligned columns: the selector to pass
// back, the display name when it adds something the ID does not already end
// with, and the path written the way the tables write one.
func writeCandidates(b *strings.Builder, candidates []actions.Candidate) {
	shown := candidates
	if len(shown) > candidateTextLimit {
		shown = shown[:candidateTextLimit]
	}
	rows := make([][]string, 0, len(shown))
	widths := make([]int, 3)
	for _, candidate := range shown {
		name := candidate.DisplayName
		if name == candidate.ID || strings.HasSuffix(candidate.ID, "/"+name) {
			name = ""
		}
		path := DisplayPath(candidate.CanonicalPath)
		if candidate.Problem != "" {
			path = strings.TrimSpace(path + " (" + candidate.Problem + ")")
		}
		row := []string{candidate.ID, name, path}
		for i, cell := range row {
			widths[i] = max(widths[i], layout.Width(cell))
		}
		rows = append(rows, row)
	}
	for _, row := range rows {
		// A column no candidate fills takes no space, and a row ends at its
		// last filled cell rather than in padding.
		last := len(row) - 1
		for last > 0 && row[last] == "" {
			last--
		}
		b.WriteString("\n  ")
		for i, cell := range row[:last+1] {
			if widths[i] == 0 {
				continue
			}
			if i == last {
				b.WriteString(cell)
				break
			}
			b.WriteString(layout.PadRight(cell, widths[i]))
			b.WriteString(tableGap)
		}
	}
	if hidden := len(candidates) - len(shown); hidden > 0 {
		fmt.Fprintf(b, "\n  … and %d more (--json lists every candidate)", hidden)
	}
}

// textActionError keeps the original *actions.Error reachable through
// errors.As while reporting the expanded text.
type textActionError struct {
	text  string
	cause *actions.Error
}

func (e *textActionError) Error() string { return e.text }
func (e *textActionError) Unwrap() error { return e.cause }
