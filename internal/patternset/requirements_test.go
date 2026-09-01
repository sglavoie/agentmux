package patternset

import (
	"strings"
	"testing"
)

// decodeForTest builds a set the way the loader does, so these tests exercise
// the same Patterns values production validates rather than a hand-built struct
// that could satisfy an accessor no real file could.
func decodeForTest(t *testing.T, body string) Patterns {
	t.Helper()
	set, err := Decode([]byte("harness = \"claude\"\n"+body), "claude")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return set
}

// TestRequirementKindDistinguishesSections is the reason Kind exists. Regexp
// returns nil for a name defined under [phrases] and Phrases returns nil for a
// name defined under [regex], so a clause moved between sections is present by
// name, matches nothing, and would otherwise validate.
func TestRequirementKindDistinguishesSections(t *testing.T) {
	set := decodeForTest(t, `
[regex]
as_regex = 'x'

[phrases]
as_phrases = ["x"]

[group.as_group]
any = ["as_regex", "as_phrases"]
`)

	cases := []struct {
		kind ClauseKind
		name string
		want bool
	}{
		{RegexClause, "as_regex", true},
		{RegexClause, "as_phrases", false},
		{RegexClause, "as_group", false},
		{PhraseClause, "as_phrases", true},
		{PhraseClause, "as_regex", false},
		{PhraseClause, "as_group", false},
		// AnyClause is for the clauses the Go side reads through Match, which
		// resolves all three shapes; it must accept all three.
		{AnyClause, "as_regex", true},
		{AnyClause, "as_phrases", true},
		{AnyClause, "as_group", true},
		{AnyClause, "absent", false},
	}
	for _, tc := range cases {
		t.Run(tc.kind.String()+"/"+tc.name, func(t *testing.T) {
			requirements := Requirements{{Name: tc.name, Kind: tc.kind, Reason: "because"}}
			if got := requirements.Satisfied(set); got != tc.want {
				t.Fatalf("Satisfied = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRequirementMisfiledClauseSaysSo separates the two failures an operator can
// hit, because "not defined" sends someone hunting for a clause that is sitting
// right there in the file.
func TestRequirementMisfiledClauseSaysSo(t *testing.T) {
	set := decodeForTest(t, "[phrases]\nworking = [\"x\"]\n")

	err := Requirements{{Name: "working", Kind: RegexClause,
		Reason: "it is the only source of the live-status veto"}}.Validate(set)
	if err == nil {
		t.Fatal("a clause in the wrong section must not validate")
	}
	for _, want := range []string{"working", "defined, but not in [regex]", "live-status veto"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	err = Requirements{{Name: "missing", Kind: RegexClause, Reason: "the reason"}}.Validate(set)
	if err == nil {
		t.Fatal("an absent clause must not validate")
	}
	if !strings.Contains(err.Error(), "not defined") {
		t.Errorf("error = %q, want it to say the clause is absent", err)
	}
}

// TestRequirementMinSubmatches covers the case where the pattern matches all the
// right lines and is still wrong: an adapter that reads submatch 1 gets an empty
// capture from a regex with no group, which is a value it cannot distinguish
// from a genuinely empty match.
func TestRequirementMinSubmatches(t *testing.T) {
	requirements := Requirements{{Name: "composer_line", Kind: RegexClause, MinSubmatches: 1,
		Reason: "the classifier reads the remainder"}}

	withGroup := decodeForTest(t, "[regex]\ncomposer_line = '^>\\s*(.*)$'\n")
	if err := requirements.Validate(withGroup); err != nil {
		t.Fatalf("a regex with the capture group must validate, got %v", err)
	}

	// Same matched lines, no capture group.
	withoutGroup := decodeForTest(t, "[regex]\ncomposer_line = '^>\\s*.*$'\n")
	err := requirements.Validate(withoutGroup)
	if err == nil {
		t.Fatal("a regex with no capture group must not satisfy MinSubmatches")
	}
	for _, want := range []string{"0 capture group", "reads 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	// Extra groups are fine: the contract is a floor, so a user free to write a
	// more precise regex is not forced to match the shipped one group-for-group.
	extra := decodeForTest(t, "[regex]\ncomposer_line = '^(>|\\x{203a})\\s*(.*)$'\n")
	if err := requirements.Validate(extra); err != nil {
		t.Fatalf("additional capture groups must be allowed, got %v", err)
	}

	// MinSubmatches only applies to a regex; a phrase clause has no submatches
	// and must not be judged against a number that cannot mean anything for it.
	phrase := Requirements{{Name: "hint", Kind: PhraseClause, MinSubmatches: 3, Reason: "r"}}
	if err := phrase.Validate(decodeForTest(t, "[phrases]\nhint = [\"x\"]\n")); err != nil {
		t.Fatalf("MinSubmatches must be inert for a phrase clause, got %v", err)
	}
}

// TestRequirementsValidateReportsEveryProblem is a usability contract, not a
// cosmetic one: these files are hand-edited, and a validator that surfaces one
// missing clause per daemon restart turns a five-minute fix into five restarts.
func TestRequirementsValidateReportsEveryProblem(t *testing.T) {
	requirements := Requirements{
		{Name: "first", Kind: AnyClause, Reason: "first reason"},
		{Name: "second", Kind: RegexClause, Reason: "second reason"},
		{Name: "third", Kind: PhraseClause, Reason: "third reason"},
	}
	err := requirements.Validate(decodeForTest(t, "[regex]\nsecond = 'x'\n"))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, name := range []string{"first", "third"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error = %q, want it to name %q", err, name)
		}
	}
	if strings.Contains(err.Error(), "second") {
		t.Errorf("error = %q, want the satisfied clause left out", err)
	}
	if got := requirements.Names(); len(got) != 3 || got[0] != "first" || got[2] != "third" {
		t.Errorf("Names() = %v, want declaration order", got)
	}
}

// TestEmptyRequirementsAcceptAnything pins the default in requirementsFor's
// switch: a pattern set nobody has audited is accepted rather than rejected, so
// adding a harness cannot break the loader before its requirements are written.
func TestEmptyRequirementsAcceptAnything(t *testing.T) {
	var none Requirements
	if !none.Satisfied(Patterns{}) {
		t.Error("no requirements must be satisfied by anything, including the zero set")
	}
	if err := none.Validate(Patterns{}); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
	if got := none.Names(); len(got) != 0 {
		t.Errorf("Names() = %v, want empty", got)
	}
}

// TestZeroPatternsSatisfyNoRequirement is the fail direction that matters: a set
// that failed to build must never look complete, because the settle path in the
// Claude adapter asks Satisfied about whatever set it was handed.
func TestZeroPatternsSatisfyNoRequirement(t *testing.T) {
	for _, kind := range []ClauseKind{AnyClause, RegexClause, PhraseClause} {
		requirements := Requirements{{Name: "anything", Kind: kind, Reason: "r"}}
		if requirements.Satisfied(Patterns{}) {
			t.Errorf("the zero Patterns satisfied a %s requirement", kind)
		}
	}
}
