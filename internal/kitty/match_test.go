package kitty_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestMatchTabByID(t *testing.T) {
	got := kitty.MatchTabByID(42)
	want := "id:42"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMatchTabByTitle(t *testing.T) {
	got := kitty.MatchTabByTitle("W:api")
	want := "title:W:api"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMatchAccountWindows(t *testing.T) {
	got := kitty.MatchAccountWindows("work")
	want := "var:^cc_account$=^work$"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestMatchWindowByVar_EscapesRegexMetachars guards against prefix bleed and
// invalid-regex errors when the var name or value contains regex
// metacharacters. Kitty's `var:` field interprets both sides as Python regexes
// applied unanchored, so raw names/values could otherwise match unintended
// user vars or values.
func TestMatchWindowByVar_EscapesRegexMetachars(t *testing.T) {
	tests := []struct {
		name    string
		varName string
		value   string
		want    string
	}{
		{"plain", "cc_account", "work", "var:^cc_account$=^work$"},
		{"var name with dot", "cc.account", "work", `var:^cc\.account$=^work$`},
		{"value with dot", "cc_account", "work.dev", `var:^cc_account$=^work\.dev$`},
		{"value with brackets", "cc_account", "team[1]", `var:^cc_account$=^team\[1\]$`},
		{"value with plus and star", "cc_account", "a+b*c", `var:^cc_account$=^a\+b\*c$`},
		{"empty value", "cc_account", "", "var:^cc_account$=^$"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kitty.MatchWindowByVar(tt.varName, tt.value)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchWindowByVar_AnchorsVarName(t *testing.T) {
	got := kitty.MatchWindowByVar("cc_account", "work")
	if got == "var:cc_account=^work$" {
		t.Fatal("var name must be anchored; Kitty applies var-name regexes unanchored")
	}
	if got != "var:^cc_account$=^work$" {
		t.Fatalf("got %q, want anchored var-name match", got)
	}
}
