package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func visibleDashboardBases(m Model) []string {
	bases := make([]string, 0, len(m.filtered))
	for _, running := range m.filtered {
		bases = append(bases, running.Base)
	}
	return bases
}

func TestDashboardProfileCycleFiltersRunningProfilesAndWraps(t *testing.T) {
	running := []session.RunningSession{
		{Base: "work-one", Profile: "Work", Account: "legacy"},
		{Base: "personal", Profile: "personal"},
		{Base: "work-two", Profile: "work"},
		{Base: "unprofiled"},
	}
	m := loadSessions(testModel(), running)

	if got := dashboard.SessionProfiles(m.sessions); !slices.Equal(got, []string{"personal", "Work"}) {
		t.Fatalf("running profile cycle = %v, want [personal Work]", got)
	}

	m = pressKey(m, "f")
	if m.activeFilter.Account != "personal" {
		t.Fatalf("first profile filter = %q, want personal", m.activeFilter.Account)
	}
	if got := visibleDashboardBases(m); !slices.Equal(got, []string{"personal"}) {
		t.Fatalf("personal rows = %v, want [personal]", got)
	}
	if chips := stripAnsi(renderChips(m, 120)); !strings.Contains(chips, "@personal") {
		t.Fatalf("profile chip missing @personal: %q", chips)
	}

	m = pressKey(m, "f")
	if m.activeFilter.Account != "Work" {
		t.Fatalf("second profile filter = %q, want Work", m.activeFilter.Account)
	}
	if got := visibleDashboardBases(m); !slices.Equal(got, []string{"work-one", "work-two"}) {
		t.Fatalf("work rows = %v, want [work-one work-two]", got)
	}

	m = pressKey(m, "f")
	if m.activeFilter.Account != "" {
		t.Fatalf("wrapped profile filter = %q, want all", m.activeFilter.Account)
	}
	if len(m.filtered) != len(running) {
		t.Fatalf("wrapped visible rows = %d, want %d", len(m.filtered), len(running))
	}
}

func TestDashboardProfileCycleCombinesWithOtherFilterComponents(t *testing.T) {
	running := []session.RunningSession{
		{Base: "api-personal", Profile: "personal", Harness: "claude", State: "key"},
		{Base: "api-work", Profile: "work", Harness: "claude", State: "key"},
		{Base: "api-copilot", Profile: "personal", Harness: "copilot", State: "key"},
		{Base: "notes", Profile: "personal", Harness: "claude", State: "key"},
		{Base: "api-active", Profile: "personal", Harness: "claude", State: "bolt"},
	}
	m := loadSessions(testModel(), running)
	m.applyFilterChange(dashboard.Parse("api %claude !key"))

	m = pressKey(m, "f")
	if got := m.activeFilter; !slices.Equal(got.Substrings, []string{"api"}) ||
		got.Account != "personal" || got.Harness != "claude" || got.State != "key" {
		t.Fatalf("combined profile filter = %+v", got)
	}
	if m.activeFilter.Raw != "api @personal %claude !key" {
		t.Fatalf("serialized combined filter = %q", m.activeFilter.Raw)
	}
	if got := visibleDashboardBases(m); !slices.Equal(got, []string{"api-personal"}) {
		t.Fatalf("personal combined rows = %v, want [api-personal]", got)
	}

	m = pressKey(m, "f")
	if got := visibleDashboardBases(m); !slices.Equal(got, []string{"api-work"}) {
		t.Fatalf("work combined rows = %v, want [api-work]", got)
	}

	m = pressKey(m, "f")
	if m.activeFilter.Account != "" {
		t.Fatalf("wrapped account = %q, want all", m.activeFilter.Account)
	}
	if m.activeFilter.Raw != "api %claude !key" {
		t.Fatalf("wrapped filter = %q, want other components preserved", m.activeFilter.Raw)
	}
	if got := visibleDashboardBases(m); !slices.Equal(got, []string{"api-personal", "api-work"}) {
		t.Fatalf("all-profile combined rows = %v", got)
	}
}

func TestDashboardProfileCycleHandlesMissingAndEmptyProfileSets(t *testing.T) {
	running := []session.RunningSession{
		{Base: "work", Profile: "work"},
		{Base: "personal", Profile: "personal"},
	}
	if got := dashboard.NextProfileFilter(running, "gone"); got != "personal" {
		t.Fatalf("missing current profile advanced to %q, want personal", got)
	}
	if got := dashboard.NextProfileFilter(nil, "gone"); got != "" {
		t.Fatalf("empty profile set advanced to %q, want all", got)
	}

	m := loadSessions(testModel(), nil)
	m.applyFilterChange(dashboard.Parse("api @gone !key"))
	m = pressKey(m, "f")
	if m.activeFilter.Account != "" || m.activeFilter.Raw != "api !key" {
		t.Fatalf("empty cycle did not clear only profile: %+v", m.activeFilter)
	}
}

func TestDashboardProfileCycleUsesLatestRunningProfiles(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{Base: "personal", Profile: "personal"},
		{Base: "work", Profile: "work"},
	})
	m = pressKey(m, "f")
	if m.activeFilter.Account != "personal" {
		t.Fatalf("setup profile = %q, want personal", m.activeFilter.Account)
	}

	m = applySessions(m, commands.SessionsMsg{Sessions: []session.RunningSession{
		{Base: "work", Profile: "work"},
	}})
	if m.activeFilter.Account != "personal" || len(m.filtered) != 0 {
		t.Fatalf("refresh changed active filter or retained rows: filter=%q rows=%v", m.activeFilter.Account, visibleDashboardBases(m))
	}

	m = pressKey(m, "f")
	if m.activeFilter.Account != "work" || !slices.Equal(visibleDashboardBases(m), []string{"work"}) {
		t.Fatalf("next cycle did not use refreshed profiles: filter=%q rows=%v", m.activeFilter.Account, visibleDashboardBases(m))
	}
}

func TestDashboardProfileCycleResetsNavigationAndRefreshesPreview(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{Base: "personal", Profile: "personal", WindowID: 11},
		{Base: "work", Profile: "work", WindowID: 22},
	})
	// A size the split fits at: retargeting is something only a visible pane
	// does, so a zero-size model would assert the refresh half of this test
	// against an inspector that was never drawn.
	m.width, m.height = 160, 50
	requireInspector(t, m)
	m.cursor = 1
	m.listOffset = 1
	m.focusedChipIdx = 2
	m.previewWindowID = 22

	updated, cmd := m.Update(keyPress("f"))
	got := updated.(Model)
	if got.cursor != 0 || got.listOffset != 0 || got.focusedChipIdx != -1 {
		t.Fatalf("cycle navigation = cursor %d offset %d chip %d, want 0/0/-1",
			got.cursor, got.listOffset, got.focusedChipIdx)
	}
	if got.previewWindowID != 11 || cmd == nil {
		t.Fatalf("preview refresh = window %d cmd=%v, want window 11 and command", got.previewWindowID, cmd != nil)
	}
}
