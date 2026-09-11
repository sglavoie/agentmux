package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func TestAllPastSessionsClearsFiltersAndOrdersAcrossProjects(t *testing.T) {
	for _, route := range []string{"S", "palette"} {
		for _, refreshing := range []bool{false, true} {
			t.Run(route+map[bool]string{false: "/ready", true: "/refreshing"}[refreshing], func(t *testing.T) {
				m := sessionSearchFilterModel(t)
				m.mode = ModeList
				m.activeFilter = dashboard.Parse("project:Foo")
				m.sessionQuery = "needle"
				m.filterProject = []string{"Foo"}
				m.filterProfile = []string{"work"}
				m.filterHarness = []string{harness.Claude}
				m.sessionResults = m.sessionResults[:1]
				stale := append([]transcript.Result(nil), m.sessionResults...)
				m.sessionRefreshInFlight = refreshing
				oldSeq := m.sessionSearchSeq
				var got Model
				if route == "S" {
					got = pressKey(m, "S")
				} else {
					got, _ = runDashboardCommand(t, m, commands.SearchAllSessions)
				}
				if got.mode != ModeSessionSearch {
					t.Fatalf("mode = %v, want Past Sessions", got.mode)
				}
				if !sessionQueryIsEmpty(got.currentSessionQuery()) {
					t.Fatalf("all-session query retained filters: %+v", got.currentSessionQuery())
				}
				if got.activeFilter.Raw != m.activeFilter.Raw {
					t.Fatal("opening history changed the dashboard filter")
				}
				if len(got.sessionResults) != 0 || !got.sessionLoading {
					t.Fatal("old project results shown as the all-projects result set")
				}
				updated, cmd := got.Update(sessionSearchDebounceMsg{seq: got.sessionSearchSeq})
				got = updated.(Model)
				got = send(got, drainCmd(t, cmd))
				if len(got.sessionResults) != 2 || got.sessionResults[0].Meta.Project != "Foo" || got.sessionResults[1].Meta.Project != "Bar" {
					t.Fatalf("want both projects newest first, got %+v", got.sessionResults)
				}
				got = send(got, sessionSearchDoneMsg{seq: oldSeq, results: stale})
				got = send(got, sessionWarmRefreshDoneMsg{seq: oldSeq, results: stale})
				if len(got.sessionResults) != 2 {
					t.Fatal("stale project search replaced all-project results")
				}
			})
		}
	}
}

func TestAllPastSessionsShortcutJoinsStartupCacheLoad(t *testing.T) {
	m := testModel()
	m.sessionCacheLoadInFlight = true
	m.filterProject = []string{"Foo"}
	updated, cmd := m.Update(keyPress("S"))
	got := updated.(Model)
	if got.mode != ModeSessionSearch || !got.sessionLoading || !sessionQueryIsEmpty(got.currentSessionQuery()) {
		t.Fatalf("shortcut did not open unfiltered history during startup: mode=%v loading=%v query=%+v", got.mode, got.sessionLoading, got.currentSessionQuery())
	}
	if cmd != nil {
		t.Fatal("shortcut duplicated the startup cache load")
	}
	fixture := sessionSearchFilterModel(t)
	got = send(got, sessionCacheLoadDoneMsg{seq: got.sessionSearchSeq, idx: fixture.transcriptIndex, readers: fixture.transcriptReaders, results: fixture.sessionResults})
	if len(got.sessionResults) != 2 || got.sessionLoading {
		t.Fatal("startup cache did not populate all-project history")
	}
}
