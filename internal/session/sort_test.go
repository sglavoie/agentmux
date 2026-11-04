package session_test

import (
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

// Order is the one order every surface lists sessions in: whatever carries
// the sessions, the same profile, base title, and pin sort the same way
// SortRunning sorts them, and equal sessions keep their places.
func TestOrderMatchesSortRunningForAnyCarrier(t *testing.T) {
	t.Parallel()

	sessions := []session.RunningSession{
		{RuntimeSessionID: "rs-1", Account: "work", Base: "🤖 dashboard"},
		{RuntimeSessionID: "rs-2", Account: "personal", Base: "dotfiles"},
		{RuntimeSessionID: "rs-3", Account: "work", Base: "api"},
		{RuntimeSessionID: "rs-4", Account: "work", Base: "zeta", Starred: true},
		{RuntimeSessionID: "rs-5", Account: "work", Base: "api"},
	}
	type row struct {
		id    string
		entry session.OrderEntry
	}
	for mode, want := range map[string][]string{
		"alpha":   {"rs-4", "rs-3", "rs-5", "rs-1", "rs-2"},
		"account": {"rs-4", "rs-2", "rs-3", "rs-5", "rs-1"},
		"":        {"rs-4", "rs-3", "rs-5", "rs-1", "rs-2"},
	} {
		rows := make([]row, len(sessions))
		for i, s := range sessions {
			rows[i] = row{id: string(s.RuntimeSessionID), entry: session.OrderEntry{Profile: s.Account, Base: s.Base, Starred: s.Starred}}
		}
		session.Order(rows, mode, func(r row) session.OrderEntry { return r.entry })
		sorted := session.SortRunning(sessions, mode)
		for i, id := range want {
			if rows[i].id != id || string(sorted[i].RuntimeSessionID) != id {
				t.Fatalf("mode %q: position %d is row %s and session %s, want %s in both", mode, i, rows[i].id, sorted[i].RuntimeSessionID, id)
			}
		}
	}
	if sessions[0].RuntimeSessionID != "rs-1" {
		t.Fatal("SortRunning reordered its argument")
	}
}

// Equal project and profile keys are ordered by runtime session ID.
func TestSortRunningBreaksTiesByRuntimeSession(t *testing.T) {
	t.Parallel()
	var sessions []session.RunningSession
	for _, id := range []string{"rs-c", "rs-a", "rs-b"} {
		sessions = append(sessions, session.RunningSession{Account: "work", Base: "api", Backend: runtimesession.BackendTmux, RuntimeSessionID: runtimesession.ID(id)})
	}
	for _, mode := range []string{"alpha", "account"} {
		var got []string
		for _, s := range session.SortRunning(sessions, mode) {
			got = append(got, string(s.RuntimeSessionID))
		}
		if want := []string{"rs-a", "rs-b", "rs-c"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %q: order = %v, want %v", mode, got, want)
		}
	}
}
