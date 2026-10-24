package tmux

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
)

// Two terminals that attached in the same second are told apart by which was
// used last. The one Competing keeps must be the one owner names, or a focus
// key moves the terminal the reconciler is about to detach.
func TestOwnerIsTheTerminalCompetingKeeps(t *testing.T) {
	group := backend.Ref{ID: "$1"}
	clients := []Client{
		{Name: "/dev/ttys009", Group: group, Created: 100, Activity: 100},
		{Name: "/dev/ttys001", Group: group, Created: 100, Activity: 150},
	}
	survey := Survey{Clients: clients}
	owner, ok := survey.owner()
	if !ok {
		t.Fatal("no owner")
	}
	competing := survey.Competing()
	if len(competing) != 1 {
		t.Fatalf("competing = %+v, want one", competing)
	}
	if competing[0].Name == owner.Name {
		t.Fatalf("owner %s is the terminal Competing would detach", owner.Name)
	}
	if owner.Name != "/dev/ttys001" {
		t.Fatalf("owner = %s, want the terminal used last", owner.Name)
	}
}
