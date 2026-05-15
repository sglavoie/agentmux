package tui

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
//	!<state>     state filter; recognized: active, idle, bolt, key, stop, error
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
		case len(tok) >= 2 && tok[0] == '\\' && (tok[1] == '@' || tok[1] == '!'):
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
// stateGroup classification.
func (f Filter) Match(s session.RunningSession, lastActivity map[int]time.Time, idle time.Duration) bool {
	if f.Account != "" && !strings.EqualFold(s.Account, f.Account) {
		return false
	}
	if f.Harness != "" && !strings.EqualFold(s.Harness, f.Harness) {
		return false
	}
	if f.State != "" && !matchState(s, f.State, lastActivity, idle) {
		return false
	}
	if len(f.Substrings) > 0 {
		title := strings.ToLower(s.Base)
		dir := strings.ToLower(s.Directory)
		acct := strings.ToLower(s.Account)
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

// chipKind classifies a removable filter chip rendered above the list.
type chipKind int

const (
	chipSubstring chipKind = iota
	chipAccount
	chipHarness
	chipState
)

// chipComponent is one removable chip extracted from a Filter. The label
// includes a leading sigil (`/`, `@`, `!`) so chips read like the DSL the
// user typed.
type chipComponent struct {
	kind  chipKind
	label string // sigil + value, e.g. "/api", "@work", "!key"
	raw   string // value without the sigil, e.g. "api", "work", "key"
}

// Components returns the ordered chip list backing the filter: substrings
// first (in token order), then account, then state. Removal keys (1..N) map
// onto this order so the indices are stable as long as Components and
// RemoveComponent are kept in sync.
func (f Filter) Components() []chipComponent {
	var out []chipComponent
	for _, s := range f.Substrings {
		out = append(out, chipComponent{kind: chipSubstring, label: "/" + s, raw: s})
	}
	if f.Account != "" {
		out = append(out, chipComponent{kind: chipAccount, label: "@" + f.Account, raw: f.Account})
	}
	if f.Harness != "" {
		out = append(out, chipComponent{kind: chipHarness, label: "%" + f.Harness, raw: f.Harness})
	}
	if f.State != "" {
		out = append(out, chipComponent{kind: chipState, label: "!" + f.State, raw: f.State})
	}
	return out
}

// RemoveComponent returns a new Filter with the component at idx removed.
// Indices match the order returned by Components. An out-of-range idx is a
// no-op (returns the original filter unchanged).
func (f Filter) RemoveComponent(idx int) Filter {
	comps := f.Components()
	if idx < 0 || idx >= len(comps) {
		return f
	}
	out := Filter{
		Substrings: append([]string(nil), f.Substrings...),
		Account:    f.Account,
		Harness:    f.Harness,
		State:      f.State,
	}
	target := comps[idx]
	switch target.kind {
	case chipSubstring:
		// Substrings sit at positions 0..len(Substrings)-1 in Components,
		// so idx is a direct slice index.
		out.Substrings = append(out.Substrings[:idx], out.Substrings[idx+1:]...)
	case chipAccount:
		out.Account = ""
	case chipHarness:
		out.Harness = ""
	case chipState:
		out.State = ""
	}
	out.Raw = serializeFilter(out)
	return out
}

// serializeFilter rebuilds a DSL string from a Filter so its Raw stays in
// sync after components are removed. Bare substrings that would otherwise
// be misparsed as @account / !state get an escape prefix.
func serializeFilter(f Filter) string {
	var parts []string
	for _, s := range f.Substrings {
		if len(s) > 0 && (s[0] == '@' || s[0] == '!') {
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

// matchState applies the !<state> filter, including the active/idle aliases
// that fan out to stateGroup buckets.
func matchState(s session.RunningSession, state string, lastActivity map[int]time.Time, idle time.Duration) bool {
	switch strings.ToLower(state) {
	case "active":
		return stateGroup(s, lastActivity, idle) == 1
	case "idle":
		return stateGroup(s, lastActivity, idle) == 2
	case "bolt", "key", "stop", "error":
		return s.State == strings.ToLower(state)
	}
	// Unknown state name — lenient: pass everything through so the user can
	// see they have a typo without the list going eerily empty.
	return true
}
