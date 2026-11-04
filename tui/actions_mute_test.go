package tui

import (
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
	"testing"
)

func TestFindAccountByNameCaseInsensitive(t *testing.T) {
	t.Parallel()

	accounts := []config.Account{
		{Name: "work"},
		{Name: "personal"},
	}
	for _, name := range []string{"work", "Work", "WORK"} {
		got, ok := commands.FindAccountByName(accounts, name)
		if !ok {
			t.Errorf("findAccountByName(%q): not found", name)
			continue
		}
		if got.Name != "work" {
			t.Errorf("findAccountByName(%q): resolved to %q, want %q", name, got.Name, "work")
		}
	}
	if _, ok := commands.FindAccountByName(accounts, "unknown"); ok {
		t.Error("findAccountByName(unknown): unexpectedly found")
	}
}
