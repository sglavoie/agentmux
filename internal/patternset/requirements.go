package patternset

import (
	"fmt"
	"strings"
)

// This file holds the SEMANTIC half of validation. Decode answers "is this a
// well-formed pattern file"; Requirements answers "is this pattern set one the
// Go side can actually classify with".
//
// The two are separate because a syntactically perfect override can still be
// unsafe. Patterns.Match answers false for a clause the set does not define,
// and that tolerance is deliberate — an override written against an older
// release should lose a check rather than crash the probe. But the checks are
// not interchangeable: some are POSITIVE evidence, whose absence merely
// withholds a conclusion, and some are REFUSALS, whose absence deletes the only
// reason a screen would not settle. Losing one of the latter turns a running
// pane into a finished one, which is the exact failure the whole classifier
// exists to prevent.
//
// So the packages that own the adapters declare which of their clauses are
// refusals, and the loader refuses an override that is missing one. The set is
// deliberately a SUBSET of every clause an adapter reads: requiring all of them
// would make every release bump invalidate every override in the field, for
// clauses whose absence is provably safe.

// ClauseKind constrains what kind of clause satisfies a Requirement.
//
// The kind is part of the contract because the accessors are not
// interchangeable: Patterns.Regexp returns nil for a name defined as a phrase
// list, and Patterns.Phrases returns nil for a name defined as a regex. A user
// who moved a clause between sections would otherwise get a set that decodes,
// installs, and silently never runs that check.
type ClauseKind uint8

const (
	// AnyClause accepts a regex, a phrase list or a group. Use it for a clause
	// the Go side evaluates through Patterns.Match, which handles all three.
	AnyClause ClauseKind = iota
	// RegexClause requires a [regex]. Use it for a clause the Go side resolves
	// with Patterns.Regexp.
	RegexClause
	// PhraseClause requires a [phrases] list, for a clause the Go side resolves
	// with Patterns.Phrases.
	PhraseClause
)

// String renders a ClauseKind as the file section that satisfies it.
func (k ClauseKind) String() string {
	switch k {
	case RegexClause:
		return "[regex]"
	case PhraseClause:
		return "[phrases]"
	default:
		return "any section"
	}
}

// Requirement is one clause a pattern set must define for the Go side to be
// able to classify safely with it.
type Requirement struct {
	// Name is the clause name, as written in the file and read by the adapter.
	Name string
	// Kind is the section the adapter can actually read this clause from.
	Kind ClauseKind
	// MinSubmatches is the number of capture groups the adapter reads from a
	// RegexClause. It is part of the contract for the same reason Kind is: a
	// regex that matches the right lines but exposes no submatch hands the
	// adapter an empty capture, and an adapter that reads a capture is reading
	// it to make a decision. Zero means the adapter reads no submatch.
	MinSubmatches int
	// Reason says what breaks when this requirement is unmet. It is mandatory
	// in practice rather than in code: this string is what an operator sees in
	// the doctor finding, and "clause missing" without it is not actionable.
	Reason string
}

// Requirements is one pattern set's semantic contract.
type Requirements []Requirement

// Names lists the required clause names in declaration order.
func (r Requirements) Names() []string {
	names := make([]string, 0, len(r))
	for _, requirement := range r {
		names = append(names, requirement.Name)
	}
	return names
}

// Satisfied reports whether p meets every requirement. It is the hot-path form
// used by adapters that re-check the installed set per classification, and it
// builds no error strings.
func (r Requirements) Satisfied(p Patterns) bool {
	for _, requirement := range r {
		if requirement.unmet(p) != "" {
			return false
		}
	}
	return true
}

// Validate reports every unmet requirement in one error, so an operator fixing
// a hand-edited override sees the whole list rather than discovering it one
// daemon restart at a time.
func (r Requirements) Validate(p Patterns) error {
	var problems []string
	for _, requirement := range r {
		if problem := requirement.unmet(p); problem != "" {
			problems = append(problems, problem)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("incomplete pattern set: %s", strings.Join(problems, "; "))
}

// unmet returns a human-readable problem, or "" when the requirement holds.
func (r Requirement) unmet(p Patterns) string {
	switch r.Kind {
	case RegexClause:
		re := p.Regexp(r.Name)
		if re == nil {
			return r.problem(p)
		}
		if got := re.NumSubexp(); got < r.MinSubmatches {
			return fmt.Sprintf(
				"%s: regex declares %d capture group(s) but the classifier reads %d; %s",
				r.Name, got, r.MinSubmatches, r.Reason)
		}
	case PhraseClause:
		if len(p.Phrases(r.Name)) == 0 {
			return r.problem(p)
		}
	default:
		if !p.Has(r.Name) {
			return r.problem(p)
		}
	}
	return ""
}

// problem distinguishes "absent" from "present in the wrong section", because
// the second is a much easier mistake to make and a much harder one to see.
func (r Requirement) problem(p Patterns) string {
	if p.Has(r.Name) {
		return fmt.Sprintf("%s: defined, but not in %s, so the classifier cannot read it; %s",
			r.Name, r.Kind, r.Reason)
	}
	return fmt.Sprintf("%s: required clause is not defined; %s", r.Name, r.Reason)
}
