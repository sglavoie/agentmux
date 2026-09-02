package launch

import (
	"slices"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestPickerDSLAccountFilter(t *testing.T) {
	// Drive the DSL filter via ApplyFilter directly to verify the underlying
	// semantics — the live-typing path (in search mode) is covered by TestPickerTypingNarrowsLive.
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
		{Title: "notes", Account: "personal"},
	}
	got := ApplyFilter(sessions, "@personal")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("expected [1,2] for @personal, got %v", got)
	}
}

func TestApplyPickerFilterDSLSubstring(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Directory: "/home/user/api", Account: "work"},
		{Title: "backend", Directory: "/home/user/backend", Account: "work"},
		{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
	}
	got := ApplyFilter(sessions, "back")
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1] for 'back', got %v", got)
	}
}

func TestApplyPickerFilterDSLAccount(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
	}
	got := ApplyFilter(sessions, "@personal")
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1] for '@personal', got %v", got)
	}
}

func TestApplyPickerFilterEmptyReturnsAll(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
	}
	got := ApplyFilter(sessions, "")
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("expected [0,1] for empty query, got %v", got)
	}
}

func TestPickerProfileCycleEmptySingleAndCaseInsensitive(t *testing.T) {
	if got := NextProfileFilter(nil, ""); got != "" {
		t.Fatalf("empty cycle = %q, want all", got)
	}
	one := []config.Session{{Title: "api", Profile: "Work"}}
	if got := NextProfileFilter(one, ""); got != "Work" {
		t.Fatalf("single-profile first cycle = %q, want Work", got)
	}
	if got := NextProfileFilter(one, "work"); got != "" {
		t.Fatalf("single-profile wrap = %q, want all", got)
	}
	if got := filterIndicesByProfile(one, []int{0}, "work"); !slices.Equal(got, []int{0}) {
		t.Fatalf("case-insensitive profile match = %v, want [0]", got)
	}
}
