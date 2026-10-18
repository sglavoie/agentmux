package dashboard

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/session"
)

// Filter is the parsed representation of a dashboard filter expression.
//
// Grammar (tokens are whitespace-separated, AND semantics):
//
//	@<account>   exact account match (case-insensitive)
//	%<harness>   exact harness match (case-insensitive); e.g. %claude, %copilot
//	#<project>   exact project-label match (case-insensitive); quote whitespace
//	!<state>     state filter; recognized: attention, active, idle, bolt, key,
//	             stop, warning, error, subagent, compact, question
//	<word>       case-insensitive substring against title, directory, or account
//	\@foo / \#x  literal leading sigil (lenient: also accepted bare in titles)
//
// Lone sigil tokens, trailing whitespace, and unknown !<state> values are
// ignored rather than treated as errors (Postel's Law).
type Filter struct {
	Substrings []string
	Project    string
	Account    string
	Harness    string
	State      string
	Raw        string
}

// Parse turns a filter expression into a Filter. An empty input produces a
// zero Filter (matches everything).
func Parse(input string) Filter {
	f := Filter{Raw: input}
	for _, tok := range filterTokens(input) {
		if !tok.valid {
			continue
		}
		raw := input[tok.start:tok.end]
		switch {
		case len(raw) >= 2 && raw[0] == '\\' && isFilterSigil(raw[1]):
			// Backslash-escape lets the user type a literal leading sigil
			// (e.g. "\%opt" matches a title containing "%opt"). The
			// supported set tracks the sigil set parsed below — escaping
			// %, @, #, and ! all need to round-trip through SerializeFilter.
			f.Substrings = append(f.Substrings, raw[1:])
		case raw == "@" || raw == "!" || raw == "%" || raw == "#":
			// Lone marker — lenient: ignore.
		case tok.quotedProject:
			if tok.value != "" {
				f.Project = tok.value
			}
		case raw[0] == '#':
			f.Project = raw[1:]
		case raw[0] == '@':
			f.Account = raw[1:]
		case raw[0] == '%':
			f.Harness = strings.ToLower(raw[1:])
		case raw[0] == '!':
			f.State = raw[1:]
		default:
			f.Substrings = append(f.Substrings, raw)
		}
	}
	return f
}

type filterToken struct {
	start, end    int
	value         string
	valid         bool
	quotedProject bool
	unterminated  bool
}

// filterTokens splits the DSL while retaining byte spans for completion. Only
// project selectors have quoted syntax; every other token keeps the historical
// whitespace-delimited behavior.
func filterTokens(input string) []filterToken {
	var out []filterToken
	for i := 0; i < len(input); {
		if filterSpaceAt(input, i) {
			_, size := utf8.DecodeRuneInString(input[i:])
			i += size
			continue
		}

		start := i
		if strings.HasPrefix(input[i:], `#"`) {
			i += 2
			var value strings.Builder
			closed := false
			for i < len(input) {
				switch input[i] {
				case '\\':
					if i+1 >= len(input) {
						value.WriteByte('\\')
						i++
						continue
					}
					next := input[i+1]
					if next == '\\' || next == '"' {
						value.WriteByte(next)
						i += 2
						continue
					}
					value.WriteByte('\\')
					i++
				case '"':
					i++
					closed = true
				default:
					_, size := utf8.DecodeRuneInString(input[i:])
					value.WriteString(input[i : i+size])
					i += size
				}
				if closed {
					break
				}
			}

			if !closed {
				out = append(out, filterToken{start: start, end: len(input), value: value.String(), quotedProject: true, unterminated: true})
				break
			}
			if i < len(input) && !filterSpaceAt(input, i) {
				for i < len(input) && !filterSpaceAt(input, i) {
					_, size := utf8.DecodeRuneInString(input[i:])
					i += size
				}
				out = append(out, filterToken{start: start, end: i, value: value.String(), quotedProject: true})
				continue
			}
			out = append(out, filterToken{start: start, end: i, value: value.String(), valid: true, quotedProject: true})
			continue
		}

		for i < len(input) && !filterSpaceAt(input, i) {
			_, size := utf8.DecodeRuneInString(input[i:])
			i += size
		}
		out = append(out, filterToken{start: start, end: i, valid: true})
	}
	return out
}

func filterSpaceAt(input string, i int) bool {
	r, _ := utf8.DecodeRuneInString(input[i:])
	return unicode.IsSpace(r)
}

func isFilterSigil(b byte) bool {
	return b == '@' || b == '!' || b == '%' || b == '#'
}

// IsEmpty reports whether the filter would match every session.
func (f Filter) IsEmpty() bool {
	return len(f.Substrings) == 0 && f.Project == "" && f.Account == "" && f.Harness == "" && f.State == ""
}

// WithoutProject returns an independent filter with only its project selector
// removed. It is the contextual source filter shared by project completion and
// the project switcher.
func (f Filter) WithoutProject() Filter {
	out := f
	out.Substrings = append([]string(nil), f.Substrings...)
	out.Project = ""
	out.Raw = SerializeFilter(out)
	return out
}

// Match reports whether a session passes the filter. lastActivity and idle
// are used only for the !attention / !active / !idle state aliases, which
// require StateGroup classification.
func (f Filter) Match(s session.RunningSession, lastActivity map[int]time.Time, idle time.Duration) bool {
	profile := s.EffectiveProfile()
	if f.Project != "" && !strings.EqualFold(ProjectLabel(s), f.Project) {
		return false
	}
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
		if len(s) > 0 && isFilterSigil(s[0]) {
			parts = append(parts, "\\"+s)
		} else {
			parts = append(parts, s)
		}
	}
	if f.Project != "" {
		parts = append(parts, serializeProjectSelector(f.Project))
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

func serializeProjectSelector(value string) string {
	if strings.IndexFunc(value, unicode.IsSpace) < 0 && !strings.ContainsAny(value, `"\\`) {
		return "#" + value
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `#"` + escaped + `"`
}

// tokenAtCaret inspects input around caretPos and reports whether the caret
// sits in a sigil-led token (`@`, `!`, `%`, `#`). When it does, prefix is the text
// already typed after the sigil and replaceFrom..replaceTo are the byte
// indices a completion should overwrite. sigil is 0 when the caret is not in
// a sigil token (or the input is malformed) — callers should hide the popup.
func tokenAtCaret(input string, caretPos int) (sigil byte, prefix string, replaceFrom, replaceTo int) {
	if caretPos < 0 {
		caretPos = 0
	}
	if caretPos > len(input) {
		caretPos = len(input)
	}
	for _, tok := range filterTokens(input) {
		if caretPos <= tok.start || caretPos > tok.end {
			continue
		}
		raw := input[tok.start:tok.end]
		if tok.quotedProject {
			if !tok.valid && !tok.unterminated {
				return 0, "", tok.start, tok.end
			}
			prefixEnd := caretPos
			if tok.valid && prefixEnd == tok.end && prefixEnd > tok.start+2 && input[prefixEnd-1] == '"' {
				prefixEnd--
			}
			return '#', decodeQuotedProjectPrefix(input[tok.start+2 : prefixEnd]), tok.start, tok.end
		}
		if tok.valid && isFilterSigil(raw[0]) {
			return raw[0], input[tok.start+1 : caretPos], tok.start, tok.end
		}
		return 0, "", tok.start, tok.end
	}
	return 0, "", caretPos, caretPos
}

func decodeQuotedProjectPrefix(raw string) string {
	var value strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) && (raw[i+1] == '\\' || raw[i+1] == '"') {
			i++
			value.WriteByte(raw[i])
			continue
		}
		value.WriteByte(raw[i])
	}
	return value.String()
}

// matchState applies the !<state> filter, including the attention/active/idle
// aliases that fan out to StateGroup buckets.
//
// The three aliases name the three buckets and nothing else, which is what
// makes `!attention` mean the same thing as the group heading above the rows
// it selects. In particular it is NOT "the key, error, and question states":
// an unseen 🛑 is in the bucket and a seen one is not, and only StateGroup
// knows which. Spelling the set out here would have quietly disagreed with the
// heading the moment the unseen demotion moved a row.
func matchState(s session.RunningSession, state string, lastActivity map[int]time.Time, idle time.Duration) bool {
	switch strings.ToLower(state) {
	case "attention":
		return StateGroup(s, lastActivity, idle) == attentionGroup
	case "active":
		return StateGroup(s, lastActivity, idle) == activeGroup
	case "idle":
		return StateGroup(s, lastActivity, idle) == idleGroup
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

// FilterHint explains tokens that currently do nothing without rejecting live
// edits or changing matching. Escaped sigils remain ordinary search text.
func FilterHint(input string) string {
	for _, tok := range filterTokens(input) {
		if tok.unterminated {
			return "Project filter incomplete — close the quote to apply it"
		}
		if !tok.valid {
			return "Project filter not applied — separate it with a space"
		}
		raw := input[tok.start:tok.end]
		if len(raw) <= 1 || raw[0] != '!' {
			continue
		}
		state := strings.ToLower(raw[1:])
		if slices.Contains(completionStateNames, state) {
			continue
		}
		suggestion := ""
		for _, name := range completionStateNames {
			// Abbreviations such as attn are subsequences, not prefixes.
			remainder := state
			for _, r := range name {
				if len(remainder) > 0 && rune(remainder[0]) == r {
					remainder = remainder[1:]
				}
			}
			if remainder == "" && len(state) >= 2 {
				suggestion = name
				break
			}
		}
		if suggestion != "" {
			return fmt.Sprintf("Unknown state %q — try !%s (not applied)", raw[1:], suggestion)
		}
		return fmt.Sprintf("Unknown state %q — not applied; type ! for choices", raw[1:])
	}
	return ""
}
