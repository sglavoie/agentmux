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
	want := "var:cc_account=^work$"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestMatchWindowByVar_EscapesRegexMetachars guards against prefix bleed and
// invalid-regex errors when the value contains regex metacharacters. Kitty's
// `var:` field interprets the value as a Python regex applied unanchored, so a
// raw account name like "work" would otherwise also match "workshop", and a
// name containing "[" would be a syntax error.
func TestMatchWindowByVar_EscapesRegexMetachars(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"plain", "work", "var:cc_account=^work$"},
		{"with dot", "work.dev", `var:cc_account=^work\.dev$`},
		{"with brackets", "team[1]", `var:cc_account=^team\[1\]$`},
		{"with plus and star", "a+b*c", `var:cc_account=^a\+b\*c$`},
		{"empty value", "", "var:cc_account=^$"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kitty.MatchWindowByVar("cc_account", tt.value)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
