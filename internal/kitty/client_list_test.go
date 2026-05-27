package kitty_test

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("loading fixture %s: %v", name, err)
	}
	return data
}

func TestListWindows_ParsesJSON(t *testing.T) {
	fixture := loadFixture(t, "kitty_ls.json")
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	windows, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 OS window, got %d", len(windows))
	}
	if len(windows[0].Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(windows[0].Tabs))
	}

	tab1 := windows[0].Tabs[0]
	if tab1.Title != "P:dotfiles" {
		t.Errorf("tab 1 title: got %q, want %q", tab1.Title, "P:dotfiles")
	}
	win := tab1.Windows[0]
	if win.UserVars.Account != "personal" {
		t.Errorf("user var cc_account: got %q, want %q", win.UserVars.Account, "personal")
	}
}

func TestListWindows_Empty(t *testing.T) {
	fixture := loadFixture(t, "kitty_ls_empty.json")
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	windows, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows[0].Tabs) != 0 {
		t.Errorf("expected 0 tabs, got %d", len(windows[0].Tabs))
	}
}

func TestListWindows_InvalidJSON(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": []byte("not-json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	_, err := client.ListWindows()
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
