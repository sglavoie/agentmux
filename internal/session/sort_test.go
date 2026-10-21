package session_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestSortKey_Alpha verifies that alpha mode sorts by project title and treats
// legacy prefixed titles the same as project-only titles for the same account.
func TestSortKey_Alpha(t *testing.T) {
	t.Parallel()

	kAPI := session.SortKey("work", "api", false, "alpha")
	kDash := session.SortKey("work", "dashboard", false, "alpha")
	if kAPI >= kDash {
		t.Errorf("api key %q should be < dashboard key %q", kAPI, kDash)
	}

	if got, want := session.SortKey("work", "W:api", false, "alpha"), kAPI; got != want {
		t.Errorf("legacy alpha key = %q, want %q", got, want)
	}
}

// TestSortKey_Account verifies that account mode groups by account then title.
func TestSortKey_Account(t *testing.T) {
	t.Parallel()

	// personal < work, so dotfiles comes first in account mode.
	kPersonal := session.SortKey("personal", "dotfiles", false, "account")
	kWork := session.SortKey("work", "api", false, "account")
	if kPersonal >= kWork {
		t.Errorf("personal key %q should be < work key %q", kPersonal, kWork)
	}

	// Within work group, api < dashboard.
	kAPI := session.SortKey("work", "api", false, "account")
	kDash := session.SortKey("work", "dashboard", false, "account")
	if kAPI >= kDash {
		t.Errorf("work:api key %q should be < work:dashboard key %q", kAPI, kDash)
	}

	if got := session.SortKey("work", "W:api", false, "account"); got != kAPI {
		t.Errorf("legacy account key = %q, want %q", got, kAPI)
	}
}

// TestSortKey_StarredFirst verifies pinned tabs sort before unpinned.
func TestSortKey_StarredFirst(t *testing.T) {
	t.Parallel()

	pinned := session.SortKey("work", "api", true, "alpha")
	unpinned := session.SortKey("work", "api", false, "alpha")
	if pinned >= unpinned {
		t.Errorf("pinned key %q should be < unpinned key %q", pinned, unpinned)
	}
}

// TestSortKey_AccountEmojiOrdering pins the screenshot scenario at the
// sort-key level: emoji-prefixed projects must sort by their first
// alphabetic rune, and personal < tools < vtacyber must hold across
// the whole tab set so accounts stay grouped.
func TestSortKey_AccountEmojiOrdering(t *testing.T) {
	t.Parallel()

	ordered := []struct {
		account, base string
	}{
		{"personal", "🐱amx"},
		{"personal", "📝tt"},
		{"tools", "Katana"},
		{"vtacyber", "🎵muse"},
		{"vtacyber", "sis"},
	}
	for i := 1; i < len(ordered); i++ {
		prev := session.SortKey(ordered[i-1].account, ordered[i-1].base, false, "account")
		curr := session.SortKey(ordered[i].account, ordered[i].base, false, "account")
		if prev >= curr {
			t.Errorf("expected %q (%s/%s) < %q (%s/%s)",
				prev, ordered[i-1].account, ordered[i-1].base,
				curr, ordered[i].account, ordered[i].base)
		}
	}
}

// TestComputeInsertLocation_EmojiScreenshot reproduces the bug from the
// user-supplied screenshot: launching 🐱amx (personal) into a tab set of
// [📝tt, Katana, sis, 🎵muse] must land before P:📝tt (tab id 2), not between
// the vtacyber tabs.
func TestComputeInsertLocation_EmojiScreenshot(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_emoji.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "account", "personal", "🐱amx")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "before" {
		t.Errorf("location: got %q, want %q", loc, "before")
	}
	if tabID != 2 {
		t.Errorf("tabID: got %d, want 2 (P:📝tt)", tabID)
	}
}

// TestComputeInsertLocation_Alpha verifies sorted insertion in alpha mode.
// Legacy fixture titles still carry prefixes, but alpha mode now sorts them by
// project title: api (id=4), dashboard (id=6), dotfiles (id=2). Inserting
// backend should go after W:api (id=4).
func TestComputeInsertLocation_Alpha(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_multi.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "work", "backend")
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
// backend (work) should still go after W:api (id=4) in account mode.
func TestComputeInsertLocation_Account(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_multi.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "account", "work", "backend")
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

// TestComputeInsertLocation_DuplicateKeyInsertsAfter verifies that when the
// new tab's sort key equals an existing entry's key, insertion lands AFTER the
// equal entry rather than before it.
func TestComputeInsertLocation_DuplicateKeyInsertsAfter(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_multi.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "work", "api")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "after" {
		t.Errorf("location: got %q, want %q", loc, "after")
	}
	if tabID != 4 {
		t.Errorf("tabID: got %d, want 4 (existing W:api)", tabID)
	}
}

// TestComputeInsertLocation_Before verifies that a tab that sorts before all
// existing ones returns location "before" with the first sorted session tab's
// ID. In alpha mode, aardvark sorts before api.
func TestComputeInsertLocation_Before(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_multi.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "personal", "aardvark")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "before" {
		t.Errorf("location: got %q, want %q", loc, "before")
	}
	if tabID != 4 {
		t.Errorf("tabID: got %d, want 4 (W:api)", tabID)
	}
}

// TestComputeInsertLocation_EmptyKitty verifies first insertion into empty Kitty
// uses default Kitty placement (no --location flag).
func TestComputeInsertLocation_EmptyKitty(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_empty.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "work", "api")
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

func TestComputeInsertLocation_UsesFocusedWindow(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_two_windows.json")
	loc, tabID, err := session.ComputeInsertLocation(client, "alpha", "work", "beta")
	if err != nil {
		t.Fatalf("ComputeInsertLocation: %v", err)
	}
	if loc != "before" {
		t.Errorf("location: got %q, want %q", loc, "before")
	}
	if tabID != 4 {
		t.Errorf("tabID: got %d, want 4 (W:zeta in focused window)", tabID)
	}
}

// TestSortRunning_Alpha verifies alphabetical sort by project title.
func TestSortRunning_Alpha(t *testing.T) {
	t.Parallel()

	client := newClientWithFixture(t, "kitty_ls_multi.json")
	sessions, _ := session.List(client)
	sorted := session.SortRunning(sessions, "alpha")
	if len(sorted) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sorted))
	}
	if sorted[0].Base != "W:api" {
		t.Errorf("sorted[0]: got %q, want %q", sorted[0].Base, "W:api")
	}
	if sorted[1].Base != "W:dashboard" {
		t.Errorf("sorted[1]: got %q, want %q", sorted[1].Base, "W:dashboard")
	}
	if sorted[2].Base != "P:dotfiles" {
		t.Errorf("sorted[2]: got %q, want %q", sorted[2].Base, "P:dotfiles")
	}
}

// TestSortRunning_Account verifies account-grouped sort.
// personal (P:dotfiles) comes before work (W:api, W:dashboard).
func TestSortRunning_Account(t *testing.T) {
	t.Parallel()

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

// Order is the one order every surface lists sessions in: whatever carries
// the sessions, the same profile, base title, and pin sort the same way
// SortRunning sorts them, and equal sessions keep their places.
func TestOrderMatchesSortRunningForAnyCarrier(t *testing.T) {
	t.Parallel()

	sessions := []session.RunningSession{
		{WindowID: 1, Account: "work", Base: "🤖 dashboard"},
		{WindowID: 2, Account: "personal", Base: "dotfiles"},
		{WindowID: 3, Account: "work", Base: "api"},
		{WindowID: 4, Account: "work", Base: "zeta", Starred: true},
		{WindowID: 5, Account: "work", Base: "api"},
	}
	type row struct {
		id    int
		entry session.OrderEntry
	}
	for mode, want := range map[string][]int{
		"alpha":   {4, 3, 5, 1, 2},
		"account": {4, 2, 3, 5, 1},
		"":        {4, 3, 5, 1, 2},
	} {
		rows := make([]row, len(sessions))
		for i, s := range sessions {
			rows[i] = row{id: s.WindowID, entry: session.OrderEntry{Profile: s.Account, Base: s.Base, Starred: s.Starred}}
		}
		session.Order(rows, mode, func(r row) session.OrderEntry { return r.entry })
		sorted := session.SortRunning(sessions, mode)
		for i, id := range want {
			if rows[i].id != id || sorted[i].WindowID != id {
				t.Fatalf("mode %q: position %d is row %d and session %d, want %d in both", mode, i, rows[i].id, sorted[i].WindowID, id)
			}
		}
	}
	if sessions[0].WindowID != 1 {
		t.Fatal("SortRunning reordered its argument")
	}
}

// Sessions with no tab to give them a place are told apart by their
// runtime-session ID, whatever order their backend lists them in, and follow
// the Kitty tabs they tie with.
func TestSortRunningBreaksTiesByRuntimeSession(t *testing.T) {
	t.Parallel()

	tmux := func(id string) session.RunningSession {
		return session.RunningSession{Account: "work", Base: "api", Backend: runtimesession.BackendTmux, RuntimeSessionID: runtimesession.ID(id)}
	}
	sessions := []session.RunningSession{
		tmux("rs-c"), tmux("rs-a"),
		{WindowID: 2, Account: "work", Base: "api"},
		tmux("rs-b"),
		{WindowID: 1, Account: "work", Base: "api"},
	}
	for _, mode := range []string{"alpha", "account"} {
		var got []string
		for _, s := range session.SortRunning(sessions, mode) {
			got = append(got, fmt.Sprint(s.WindowID, s.RuntimeSessionID))
		}
		if want := []string{"2", "1", "0rs-a", "0rs-b", "0rs-c"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %q: order = %v, want %v", mode, got, want)
		}
	}
}
