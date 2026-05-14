package kitty_test

import (
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
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
	want := "var:cc_account=work"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
