package dashboard

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// Filter is the parsed representation of a dashboard filter expression.
//
// Grammar (tokens are whitespace-separated, AND semantics):
//
//	@<account>   exact account match (case-insensitive)
//	%<harness>   exact harness match (case-insensitive); e.g. %claude, %copilot
//	!<state>     state filter; recognized: active, idle, bolt, key, stop,
//	             warning, error, subagent, compact, question
//	<word>       case-insensitive substring against title, directory, or account
//	\@foo / \!x  literal leading @ or ! (lenient: also accepted bare in titles)
//
// Lone "@" / "!" tokens, trailing whitespace, and unknown !<state> values are
// ignored rather than treated as errors (Postel's Law).
type Filter struct {
	Substrings []string
	Account    string
	Harness    string
	State      string
	Raw        string
}

// Parse turns a filter expression into a Filter. An empty input produces a
// zero Filter (matches everything).
func Parse(input string) Filter {
	f := Filter{Raw: input}
	for _, tok := range strings.Fields(input) {
		switch {
		case len(tok) >= 2 && tok[0] == '\\' && (tok[1] == '@' || tok[1] == '!' || tok[1] == '%'):
			// Backslash-escape lets the user type a literal leading sigil
			// (e.g. "\%opt" matches a title containing "%opt"). The
			// supported set tracks the sigil set parsed below — escaping
			// %, @, and ! all need to round-trip through SerializeFilter.
			f.Substrings = append(f.Substrings, tok[1:])
		case tok == "@" || tok == "!" || tok == "%":
			// Lone marker — lenient: ignore.
		case tok[0] == '@':
			f.Account = tok[1:]
		case tok[0] == '%':
			f.Harness = strings.ToLower(tok[1:])
		case tok[0] == '!':
			f.State = tok[1:]
		default:
			f.Substrings = append(f.Substrings, tok)
		}
	}
	return f
}

// IsEmpty reports whether the filter would match every session.
func (f Filter) IsEmpty() bool {
	return len(f.Substrings) == 0 && f.Account == "" && f.Harness == "" && f.State == ""
}

// Match reports whether a session passes the filter. lastActivity and idle
// are used only for the !active / !idle state aliases, which require
// StateGroup classification.
func (f Filter) Match(s session.RunningSession, lastActivity map[int]time.Time, idle time.Duration) bool {
	profile := s.EffectiveProfile()
	if f.Account != "" && !strings.EqualFold(profile, f.Account) {
		return false
	}
	if f.Harness != "" && !strings.EqualFold(s.Harness, f.Harness) {
		return false
	}
	if f.State != "" && !matchState(s, f.State, lastActivity, idle) {
		return false
	}
	if len(f.Substrings) > 0 {
		title := strings.ToLower(SessionDisplayTitle(s))
		dir := strings.ToLower(s.Directory)
		acct := strings.ToLower(profile)
		for _, sub := range f.Substrings {
			needle := strings.ToLower(sub)
			if !strings.Contains(title, needle) &&
				!strings.Contains(dir, needle) &&
				!strings.Contains(acct, needle) {
				return false
			}
		}
	}
	return true
}

// SerializeFilter rebuilds a DSL string from a Filter so its Raw stays in
// sync after components are removed or a component is swapped in place. Bare
// substrings that would otherwise be misparsed as @account / %harness /
// !state get an escape prefix; the escape set must mirror the sigil set
// Parse recognizes.
func SerializeFilter(f Filter) string {
	var parts []string
	for _, s := range f.Substrings {
		if len(s) > 0 && (s[0] == '@' || s[0] == '!' || s[0] == '%') {
			parts = append(parts, "\\"+s)
		} else {
			parts = append(parts, s)
		}
	}
	if f.Account != "" {
		parts = append(parts, "@"+f.Account)
	}
	if f.Harness != "" {
		parts = append(parts, "%"+f.Harness)
	}
	if f.State != "" {
		parts = append(parts, "!"+f.State)
	}
	return strings.Join(parts, " ")
}

// TokenAtCaret inspects input around caretPos and reports whether the caret
// sits in a sigil-led token (`@`, `!`, `%`). When it does, prefix is the text
// already typed after the sigil and replaceFrom..replaceTo are the byte
// indices a completion should overwrite. sigil is 0 when the caret is not in
// a sigil token (or the input is malformed) — callers should hide the popup.
func TokenAtCaret(input string, caretPos int) (sigil byte, prefix string, replaceFrom, replaceTo int) {
	if caretPos < 0 {
		caretPos = 0
	}
	if caretPos > len(input) {
		caretPos = len(input)
	}
	start := caretPos
	for start > 0 {
		c := input[start-1]
		if c == ' ' || c == '\t' || c == '\n' {
			break
		}
		start--
	}
	if start == caretPos {
		return 0, "", caretPos, caretPos
	}
	tok := input[start:caretPos]
	switch tok[0] {
	case '@', '!', '%':
		return tok[0], tok[1:], start, caretPos
	}
	return 0, "", start, caretPos
}

// matchState applies the !<state> filter, including the active/idle aliases
// that fan out to StateGroup buckets.
func matchState(s session.RunningSession, state string, lastActivity map[int]time.Time, idle time.Duration) bool {
	switch strings.ToLower(state) {
	case "active":
		return StateGroup(s, lastActivity, idle) == 1
	case "idle":
		return StateGroup(s, lastActivity, idle) == 2
	case "bolt", "key", "stop", "warning", "error", "subagent", "compact", "question":
		return s.State == strings.ToLower(state)
	}
	// Unknown state name — lenient: pass everything through so the user can
	// see they have a typo without the list going eerily empty.
	return true
}

// ApplyFilter returns the sessions that pass f.
func ApplyFilter(sessions []session.RunningSession, f Filter, lastActivity map[int]time.Time, idle time.Duration) []session.RunningSession {
	if f.IsEmpty() {
		out := make([]session.RunningSession, len(sessions))
		copy(out, sessions)
		return out
	}
	var out []session.RunningSession
	for _, s := range sessions {
		if f.Match(s, lastActivity, idle) {
			out = append(out, s)
		}
	}
	return out
}
