package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// TestSortKey_Alpha verifies that alpha mode sorts by full base title.
func TestSortKey_Alpha(t *testing.T) {
	cases := []struct {
		account, title string
		starred        bool
		wantLT         string // key must be less than this other key
	}{
		{"personal", "P:dotfiles", false, session.SortKey("work", "W:api", false, "alpha")},
		{"work", "W:api", false, session.SortKey("work", "W:dashboard", false, "alpha")},
	}
	for _, tc := range cases {
		k := session.SortKey(tc.account, tc.title, tc.starred, "alpha")
		if k >= tc.wantLT {
			t.Errorf("SortKey(%q, %q, alpha) = %q, want < %q", tc.account, tc.title, k, tc.wantLT)
		}
	}
}

// TestSortKey_Account verifies that account mode groups by account then title.
func TestSortKey_Account(t *testing.T) {
	// personal < work, so P:dotfiles comes first in account mode.
	kPersonal := session.SortKey("personal", "P:dotfiles", false, "account")
	kWork := session.SortKey("work", "W:api", false, "account")
	if kPersonal >= kWork {
		t.Errorf("personal key %q should be < work key %q", kPersonal, kWork)
	}

	// Within work group, api < dashboard.
	kApi := session.SortKey("work", "W:api", false, "account")
	kDash := session.SortKey("work", "W:dashboard", false, "account")
	if kApi >= kDash {
		t.Errorf("work:api key %q should be < work:dashboard key %q", kApi, kDash)
	}
}

// TestSortKey_StarredFirst verifies pinned tabs sort before unpinned.
func TestSortKey_StarredFirst(t *testing.T) {
	pinned := session.SortKey("work", "W:api", true, "alpha")
	unpinned := session.SortKey("work", "W:api", false, "alpha")
	if pinned >= unpinned {
		t.Errorf("pinned key %q should be < unpinned key %q", pinned, unpinned)
	}
}

// TestComputeInsertLocation_Alpha verifies sorted insertion in alpha mode.
// Fixture has: P:dotfiles (id=2), W:api (id=4), W:dashboard (id=6).
// Inserting W:backend (work) should go after W:api (id=4).
func TestComputeInsertLocation_Alpha(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_multi.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "work", "W:backend")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "after" {
		t.Errorf("location: got %q, want %q", loc, "after")
	}
	if tabID != 4 {
		t.Errorf("tabID: got %d, want %d", tabID, 4)
	}
}

// TestComputeInsertLocation_Account verifies sorted insertion in account mode.
// W:backend (work) should still go after W:api (id=4) in account mode.
func TestComputeInsertLocation_Account(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_multi.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "account", "work", "W:backend")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "after" {
		t.Errorf("location: got %q, want %q", loc, "after")
	}
	if tabID != 4 {
		t.Errorf("tabID: got %d, want %d", tabID, 4)
	}
}

// TestComputeInsertLocation_Before verifies that a tab that sorts before all
// existing ones returns location "before" with the first session tab's ID.
// Fixture has P:dotfiles (id=2) as the first sorted session tab.
func TestComputeInsertLocation_Before(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_multi.json")
	// "A:aardvark" sorts before "P:dotfiles" in alpha mode.
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "personal", "A:aardvark")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "before" {
		t.Errorf("location: got %q, want %q", loc, "before")
	}
	if tabID != 2 {
		t.Errorf("tabID: got %d, want 2 (P:dotfiles)", tabID)
	}
}

// TestComputeInsertLocation_EmptyKitty verifies first insertion into empty Kitty
// uses default Kitty placement (no --location flag).
func TestComputeInsertLocation_EmptyKitty(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_empty.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "work", "W:api")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "" {
		t.Errorf("location: got %q, want %q", loc, "")
	}
	if tabID != 0 {
		t.Errorf("tabID: got %d, want 0", tabID)
	}
}

// TestSortRunning_Alpha verifies alphabetical sort.
func TestSortRunning_Alpha(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_multi.json")
	sessions, _ := session.List(client)
	sorted := session.SortRunning(sessions, "alpha")
	if len(sorted) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sorted))
	}
	if sorted[0].Base != "P:dotfiles" {
		t.Errorf("sorted[0]: got %q, want %q", sorted[0].Base, "P:dotfiles")
	}
	if sorted[1].Base != "W:api" {
		t.Errorf("sorted[1]: got %q, want %q", sorted[1].Base, "W:api")
	}
	if sorted[2].Base != "W:dashboard" {
		t.Errorf("sorted[2]: got %q, want %q", sorted[2].Base, "W:dashboard")
	}
}

// TestSortRunning_Account verifies account-grouped sort.
// personal (P:dotfiles) comes before work (W:api, W:dashboard).
func TestSortRunning_Account(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_multi.json")
	sessions, _ := session.List(client)
	sorted := session.SortRunning(sessions, "account")
	if sorted[0].Account != "personal" {
		t.Errorf("sorted[0].Account: got %q, want %q", sorted[0].Account, "personal")
	}
	if sorted[1].Account != "work" || sorted[1].Base != "W:api" {
		t.Errorf("sorted[1]: got account=%q base=%q", sorted[1].Account, sorted[1].Base)
	}
	if sorted[2].Account != "work" || sorted[2].Base != "W:dashboard" {
		t.Errorf("sorted[2]: got account=%q base=%q", sorted[2].Account, sorted[2].Base)
	}
}
