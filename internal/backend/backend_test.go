package backend

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func TestRefValidate(t *testing.T) {
	valid := []Ref{GroupID("1"), PaneID("%3")}
	for _, r := range valid {
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%v) = %v", r, err)
		}
	}
	invalid := []Ref{{}, {Scope: "tab", ID: "1"}, GroupID(""), PaneID("  ")}
	for _, r := range invalid {
		err := r.Validate()
		var refErr *InvalidRefError
		if !errors.Is(err, ErrInvalidRef) || !errors.As(err, &refErr) {
			t.Errorf("Validate(%+v) = %v, want *InvalidRefError", r, err)
		}
	}
	if !(Ref{}).IsZero() || GroupID("1").IsZero() {
		t.Error("IsZero")
	}
}

func TestUnsupportedErrorMatches(t *testing.T) {
	err := error(&UnsupportedError{Kind: runtimesession.BackendTmux, Operation: OpColors})
	if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalidRef) {
		t.Errorf("errors.Is mismatch for %v", err)
	}
	if got, want := err.Error(), "backend tmux: colors not supported"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestDiscoveryHelpers(t *testing.T) {
	panes := []Pane{{Ref: PaneID("7"), Group: GroupID("3")}}
	if _, ok := FindPane(panes, PaneID("7")); !ok {
		t.Error("FindPane missed")
	}
	if _, ok := FindPane(panes, GroupID("7")); ok {
		t.Error("FindPane matched across scopes")
	}
	if !GroupPresent(panes, GroupID("3")) || GroupPresent(panes, GroupID("7")) {
		t.Error("GroupPresent")
	}
}
