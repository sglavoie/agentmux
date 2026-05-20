package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// completionState backs the floating completion menu rendered under the
// search input. Visible is false when no sigil is active at the caret, or
// when the user dismissed the menu with esc.
type completionState struct {
	Visible     bool
	Sigil       byte
	Prefix      string
	ReplaceFrom int
	ReplaceTo   int
	Candidates  []string
	Selected    int
}

// completionStateNames is the closed set of state names completed for `!`.
// Mirrors matchState in filter.go plus the active/idle aliases.
var completionStateNames = []string{"active", "idle", "bolt", "key", "stop", "error", "subagent", "compact", "question"}

// candidatesForSigil returns the unfiltered candidate set for a sigil.
// Returns nil for unknown sigils.
func candidatesForSigil(sigil byte, cfg *config.Config) []string {
	switch sigil {
	case '@':
		var out []string
		seen := map[string]bool{}
		for _, p := range cfg.Profiles {
			if !seen[p.Name] {
				out = append(out, p.Name)
				seen[p.Name] = true
			}
		}
		for _, p := range cfg.Accounts {
			if !seen[p.Name] {
				out = append(out, p.Name)
				seen[p.Name] = true
			}
		}
		return out
	case '!':
		return append([]string(nil), completionStateNames...)
	case '%':
		return harness.Names()
	}
	return nil
}

// filterCandidates ranks all against prefix (case-insensitive): prefix matches
// first (in their original order), then substring matches not already
// included. Empty prefix returns all unchanged.
func filterCandidates(all []string, prefix string) []string {
	if prefix == "" {
		return append([]string(nil), all...)
	}
	needle := strings.ToLower(prefix)
	out := make([]string, 0, len(all))
	for _, c := range all {
		if strings.HasPrefix(strings.ToLower(c), needle) {
			out = append(out, c)
		}
	}
	for _, c := range all {
		lc := strings.ToLower(c)
		if !strings.HasPrefix(lc, needle) && strings.Contains(lc, needle) {
			out = append(out, c)
		}
	}
	return out
}

// recomputeCompletion looks at m.filterText at end-caret position and updates
// m.completion. Closes the menu when no sigil token is active or no
// candidates remain.
func (m *Model) recomputeCompletion() {
	m.recomputeCompletionFor(m.filterText)
}

// recomputeCompletionFor is the buffer-agnostic core used by both the
// ModeSearch input (m.filterText) and the ModeList type-to-filter input
// (m.typeBuffer).
func (m *Model) recomputeCompletionFor(text string) {
	sigil, prefix, from, to := TokenAtCaret(text, len(text))
	if sigil == 0 {
		m.completion.Visible = false
		m.completion.Candidates = nil
		m.completion.Selected = 0
		return
	}
	all := candidatesForSigil(sigil, m.cfg)
	filtered := filterCandidates(all, prefix)
	if len(filtered) == 0 {
		m.completion.Visible = false
		m.completion.Candidates = nil
		m.completion.Selected = 0
		return
	}
	m.completion.Visible = true
	m.completion.Sigil = sigil
	m.completion.Prefix = prefix
	m.completion.ReplaceFrom = from
	m.completion.ReplaceTo = to
	m.completion.Candidates = filtered
	if m.completion.Selected < 0 || m.completion.Selected >= len(filtered) {
		m.completion.Selected = 0
	}
}

// cycleCompletion moves the popup selection by delta with wrap-around.
func (m *Model) cycleCompletion(delta int) {
	if !m.completion.Visible || len(m.completion.Candidates) == 0 {
		return
	}
	n := len(m.completion.Candidates)
	m.completion.Selected = ((m.completion.Selected+delta)%n + n) % n
}

// insertCompletion replaces the active token in m.filterText with the
// selected candidate (sigil prefix + value + trailing space) and closes the
// menu. Callers must reapply the filter after.
func (m *Model) insertCompletion() {
	m.insertCompletionInto(&m.filterText)
}

// insertCompletionInto is the buffer-agnostic core; ModeList type-to-filter
// passes &m.typeBuffer instead of &m.filterText.
func (m *Model) insertCompletionInto(buf *string) {
	if !m.completion.Visible || len(m.completion.Candidates) == 0 {
		return
	}
	pick := m.completion.Candidates[m.completion.Selected]
	inserted := string(m.completion.Sigil) + pick + " "
	s := *buf
	*buf = s[:m.completion.ReplaceFrom] + inserted + s[m.completion.ReplaceTo:]
	m.completion = completionState{}
}

// closeCompletion hides the menu without changing the filter text.
func (m *Model) closeCompletion() {
	m.completion = completionState{}
}

// renderCompletion returns the floating menu lines drawn below the search
// input. Returns "" when the menu is hidden.
func renderCompletion(m Model) string {
	if !m.completion.Visible || len(m.completion.Candidates) == 0 {
		return ""
	}
	const maxRows = 6
	n := len(m.completion.Candidates)
	start := 0
	if m.completion.Selected >= maxRows {
		start = m.completion.Selected - maxRows + 1
	}
	end := start + maxRows
	if end > n {
		end = n
	}
	var sb strings.Builder
	for i := start; i < end; i++ {
		c := m.completion.Candidates[i]
		body := "  " + string(m.completion.Sigil) + c + "  "
		if i == m.completion.Selected {
			sb.WriteString(styleCompletionSelected.Render(body))
		} else {
			sb.WriteString(styleCompletionPopup.Render(body))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
