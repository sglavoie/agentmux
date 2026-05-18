package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestFindManagedTabRelative_NextWrapsAndSkipsUnmanagedTabs(t *testing.T) {
	osWindows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			managedNavTab(10, 101, "W:api"),
			unmanagedNavTab(20, 201, "shell"),
			managedNavTab(30, 301, "W:web"),
			unmanagedNavTab(40, 401, "notes"),
		},
	}}

	got, ok := session.FindManagedTabRelative(osWindows, 301, 1)
	if !ok {
		t.Fatal("expected to find next managed tab")
	}
	if got.ID != 10 {
		t.Fatalf("tab ID: got %d, want 10", got.ID)
	}
}

func TestFindManagedTabRelative_PreviousWrapsAndSkipsUnmanagedTabs(t *testing.T) {
	osWindows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			managedNavTab(10, 101, "W:api"),
			unmanagedNavTab(20, 201, "shell"),
			managedNavTab(30, 301, "W:web"),
			unmanagedNavTab(40, 401, "notes"),
		},
	}}

	got, ok := session.FindManagedTabRelative(osWindows, 101, -1)
	if !ok {
		t.Fatal("expected to find previous managed tab")
	}
	if got.ID != 30 {
		t.Fatalf("tab ID: got %d, want 30", got.ID)
	}
}

func TestFindManagedTabRelative_CurrentUnmanagedTab(t *testing.T) {
	osWindows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			managedNavTab(10, 101, "W:api"),
			unmanagedNavTab(20, 201, "shell"),
			managedNavTab(30, 301, "W:web"),
		},
	}}

	next, ok := session.FindManagedTabRelative(osWindows, 201, 1)
	if !ok {
		t.Fatal("expected to find next managed tab")
	}
	if next.ID != 30 {
		t.Fatalf("next tab ID: got %d, want 30", next.ID)
	}

	prev, ok := session.FindManagedTabRelative(osWindows, 201, -1)
	if !ok {
		t.Fatal("expected to find previous managed tab")
	}
	if prev.ID != 10 {
		t.Fatalf("previous tab ID: got %d, want 10", prev.ID)
	}
}

func TestFindManagedTabRelative_StaysInCurrentOSWindow(t *testing.T) {
	osWindows := []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				managedNavTab(10, 101, "W:api"),
				managedNavTab(20, 201, "W:web"),
			},
		},
		{
			ID: 2,
			Tabs: []kitty.Tab{
				managedNavTab(30, 301, "P:notes"),
			},
		},
	}

	got, ok := session.FindManagedTabRelative(osWindows, 101, 1)
	if !ok {
		t.Fatal("expected to find next managed tab")
	}
	if got.ID != 20 {
		t.Fatalf("tab ID: got %d, want 20", got.ID)
	}
}

func TestFindManagedTabRelative_ReturnsFalseWithoutAnotherManagedTab(t *testing.T) {
	osWindows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			managedNavTab(10, 101, "W:api"),
			unmanagedNavTab(20, 201, "shell"),
		},
	}}

	if _, ok := session.FindManagedTabRelative(osWindows, 101, 1); ok {
		t.Fatal("expected no next managed tab")
	}
	if _, ok := session.FindManagedTabRelative(osWindows, 999, 1); ok {
		t.Fatal("expected missing window to return no tab")
	}
}

func managedNavTab(tabID, windowID int, title string) kitty.Tab {
	return kitty.Tab{
		ID:    tabID,
		Title: title,
		Windows: []kitty.Window{{
			ID:       windowID,
			UserVars: kitty.UserVars{Profile: "work"},
		}},
	}
}

func unmanagedNavTab(tabID, windowID int, title string) kitty.Tab {
	return kitty.Tab{
		ID:    tabID,
		Title: title,
		Windows: []kitty.Window{{
			ID: windowID,
		}},
	}
}
