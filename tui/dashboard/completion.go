package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// Completion backs the floating completion menu rendered under the dashboard
// search input. Visible is false when no sigil is active at the caret, or when
// the user dismissed the menu. The zero value is the closed menu, so a caller
// closes it by assigning Completion{}.
type Completion struct {
	Visible     bool
	sigil       byte
	Prefix      string
	replaceFrom int
	replaceTo   int
	Candidates  []string
	Selected    int
}

// completionStateNames is the closed set of state names completed for `!`.
// Mirrors matchState in filter.go plus the active/idle aliases.
var completionStateNames = []string{"active", "idle", "bolt", "key", "stop", "warning", "error", "subagent", "compact", "question"}

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

// Recompute looks at text at end-caret position and updates the menu. It
// closes the menu when no sigil token is active or no candidates remain,
// preserving the current selection when it still indexes the new candidate
// list.
func (c *Completion) Recompute(text string, cfg *config.Config) {
	sigil, prefix, from, to := tokenAtCaret(text, len(text))
	if sigil == 0 {
		c.Visible = false
		c.Candidates = nil
		c.Selected = 0
		return
	}
	filtered := filterCandidates(candidatesForSigil(sigil, cfg), prefix)
	if len(filtered) == 0 {
		c.Visible = false
		c.Candidates = nil
		c.Selected = 0
		return
	}
	c.Visible = true
	c.sigil = sigil
	c.Prefix = prefix
	c.replaceFrom = from
	c.replaceTo = to
	c.Candidates = filtered
	if c.Selected < 0 || c.Selected >= len(filtered) {
		c.Selected = 0
	}
}

// Cycle moves the popup selection by delta with wrap-around.
func (c *Completion) Cycle(delta int) {
	if !c.Visible || len(c.Candidates) == 0 {
		return
	}
	n := len(c.Candidates)
	c.Selected = ((c.Selected+delta)%n + n) % n
}

// Insert replaces the active token in buf with the selected candidate (sigil
// prefix + value + trailing space) and closes the menu. Callers must reapply
// the filter after.
func (c *Completion) Insert(buf *string) {
	if !c.Visible || len(c.Candidates) == 0 {
		return
	}
	inserted := string(c.sigil) + c.Candidates[c.Selected] + " "
	s := *buf
	*buf = s[:c.replaceFrom] + inserted + s[c.replaceTo:]
	*c = Completion{}
}

// RenderCompletion returns the floating menu lines drawn below the search
// input. Returns "" when the menu is hidden.
func RenderCompletion(c Completion) string {
	if !c.Visible || len(c.Candidates) == 0 {
		return ""
	}
	const maxRows = 6
	n := len(c.Candidates)
	start := 0
	if c.Selected >= maxRows {
		start = c.Selected - maxRows + 1
	}
	end := start + maxRows
	if end > n {
		end = n
	}
	var sb strings.Builder
	for i := start; i < end; i++ {
		body := "  " + string(c.sigil) + c.Candidates[i] + "  "
		if i == c.Selected {
			sb.WriteString(renderSelectedRow(body))
		} else {
			sb.WriteString(styleCompletionPopup.Render(body))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
