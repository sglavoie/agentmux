package searchquery

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]Terms{
		"":                            nil,
		"   ":                         nil,
		"Auth":                        {"auth"},
		"auth  Refactor":              {"auth", "refactor"},
		`"auth refactor"`:             {"auth refactor"},
		`fix "Auth Refactor" tests`:   {"fix", "auth refactor", "tests"},
		`word"quoted phrase"`:         {"word", "quoted phrase"},
		`""  x`:                       {"x"},
		`"unterminated phrase`:        {"unterminated phrase"},
		"tab\tand\nnewline separated": {"tab", "and", "newline", "separated"},
	}
	for in, want := range cases {
		if got := Parse(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestTallyRequiresEveryTermAcrossPieces(t *testing.T) {
	terms := Parse("auth refactor")
	tally := terms.NewTally()
	if first := tally.Add("we discussed auth twice: auth"); first != 13 {
		t.Fatalf("first = %d, want 13", first)
	}
	if tally.Total() != 0 {
		t.Fatal("Total must be 0 while a term is still missing")
	}
	if first := tally.Add("then the refactor landed"); first != 9 {
		t.Fatalf("first = %d, want 9", first)
	}
	if got := tally.Total(); got != 3 {
		t.Fatalf("Total = %d, want 3 (auth×2 + refactor×1)", got)
	}
	if first := tally.Add("nothing here"); first != -1 {
		t.Fatalf("first = %d, want -1", first)
	}
}

func TestTallyFirstIsEarliestOfAnyTerm(t *testing.T) {
	tally := Parse("zeta alpha").NewTally()
	if first := tally.Add("alpha then zeta"); first != 0 {
		t.Fatalf("first = %d, want 0 (alpha precedes zeta)", first)
	}
}

func TestPhraseIsOneTerm(t *testing.T) {
	terms := Parse(`"auth refactor"`)
	if terms.ContainsAll("auth and refactor") {
		t.Fatal("a quoted phrase must match only as a contiguous span")
	}
	if !terms.ContainsAll("the auth refactor") {
		t.Fatal("quoted phrase did not match its own text")
	}
}

func TestContainsAnyAndAll(t *testing.T) {
	terms := Parse("alpha beta")
	if !terms.ContainsAny("only alpha") || terms.ContainsAll("only alpha") {
		t.Fatal("one term present: want ContainsAny true, ContainsAll false")
	}
	if !terms.ContainsAll("beta and alpha") {
		t.Fatal("both terms present in any order must satisfy ContainsAll")
	}
	if Terms(nil).ContainsAll("anything") || Terms(nil).ContainsAny("anything") {
		t.Fatal("the empty query matches nothing")
	}
}
