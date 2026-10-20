package chrome

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBindingsReadAsKeyThenLabel(t *testing.T) {
	cases := map[string]string{
		ansi.Strip(Binding("esc", "close")):                                       "esc close",
		ansi.Strip(UnavailableBinding("d", "delete")):                             "d delete",
		ansi.Strip(BindingWithLabel("[y]", "", "es", FooterLabel)):                "[y]es",
		ansi.Strip(Keys("page", Key{"pgup", false}, Key{"pgdn", true})):           "pgup/pgdn page",
		ansi.Strip(Keys("", Key{"g", true}, Key{"G", true})):                      "g/G",
		ansi.Strip(JoinBindings(" · ", Binding("a", "x"), "", Binding("b", "y"))): "a x · b y",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestAvailabilityChangesOnlyTheKeyStyle(t *testing.T) {
	if Binding("d", "delete") == UnavailableBinding("d", "delete") {
		t.Fatal("an unavailable binding must be styled apart from an available one")
	}
	if FooterKey.Render("d") == FooterUnavailableKey.Render("d") {
		t.Fatal("available and unavailable key styles must differ")
	}
}
