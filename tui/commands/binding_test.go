package commands

import "testing"

// bindingFixture is a table shaped like the properties that matter: one command
// on two keys, two commands on one key, and a binding the footer never draws.
func bindingFixture() BindingSet {
	return BindingSet{
		{Keys: []string{"enter", "space"}, Display: "enter / space", Hint: "focus", Command: FocusSession},
		{Keys: []string{"d"}, Display: "d", Command: DismissDeprecation},
		{Keys: []string{"d"}, Display: "d", Hint: "details", Command: ShowDetails},
		{Keys: []string{"p"}, Display: "p", Hint: "preview", Command: TogglePreview, Gate: GateUnchecked},
	}
}

func TestForReturnsEveryClaimantInDeclarationOrder(t *testing.T) {
	set := bindingFixture()

	claimants := set.For("d")
	if len(claimants) != 2 {
		t.Fatalf("expected both bindings on d, got %d", len(claimants))
	}
	if claimants[0].Command != DismissDeprecation || claimants[1].Command != ShowDetails {
		t.Errorf("precedence is not declaration order: %s then %s", claimants[0].Command, claimants[1].Command)
	}
	if got := set.For("nothing-binds-this"); got != nil {
		t.Errorf("expected no claimant, got %+v", got)
	}
}

func TestSecondaryKeysDispatchButDoNotName(t *testing.T) {
	set := bindingFixture()

	for _, key := range []string{"enter", "space"} {
		claimants := set.For(key)
		if len(claimants) != 1 || claimants[0].Command != FocusSession {
			t.Fatalf("%s does not dispatch FocusSession: %+v", key, claimants)
		}
	}
	// The canonical key is what the footer draws; the display names both, because
	// naming only the first would hide the second from help.
	if got := set.Key(FocusSession); got != "enter" {
		t.Errorf("canonical key = %q, want enter", got)
	}
	if got := set.Display(FocusSession); got != "enter / space" {
		t.Errorf("display = %q, want both keys", got)
	}
}

func TestLookupsAgreeOnTheFirstClaimant(t *testing.T) {
	set := bindingFixture()

	binding, ok := set.Of(ShowDetails)
	if !ok {
		t.Fatalf("ShowDetails is bound but Of did not find it")
	}
	if binding.Key() != "d" || binding.Hint != "details" {
		t.Errorf("unexpected binding %+v", binding)
	}
	if got := set.Hint(DismissDeprecation); got != "" {
		t.Errorf("undrawn binding reported hint %q", got)
	}
}

func TestUnboundCommandReportsNothingRatherThanGuessing(t *testing.T) {
	set := bindingFixture()

	if _, ok := set.Of(Quit); ok {
		t.Fatalf("Quit is not in the fixture but Of claimed it")
	}
	for name, got := range map[string]string{
		"display": set.Display(Quit),
		"key":     set.Key(Quit),
		"hint":    set.Hint(Quit),
	} {
		if got != "" {
			t.Errorf("%s for an unbound command = %q, want empty", name, got)
		}
	}
}
