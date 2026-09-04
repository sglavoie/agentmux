package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func railTestModel() Model {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Project: "api", FamilyLabel: "API", Profile: "work", State: "key"},
		{TabID: 2, WindowID: 22, Project: "docs", Profile: "work", State: "bolt"},
		{TabID: 3, WindowID: 33, Project: "notes", Profile: "personal", State: "stop"},
	})
	m.width = 140
	m.height = 30
	m.syncRailState()
	return m
}

func TestRailKeyboardFocusNavigationAndScopeApplication(t *testing.T) {
	m := railTestModel()
	if !m.railRequested || !dashboardGeometry(m).rail {
		t.Fatalf("new model rail requested/visible = %v/%v", m.railRequested, dashboardGeometry(m).rail)
	}
	m = pressKey(m, keyLeft)
	if !m.railFocused {
		t.Fatal("Left did not enter visible rail")
	}
	m = pressKey(m, keyDown)
	if m.railCursor != 1 {
		t.Fatalf("rail cursor = %d, want first project", m.railCursor)
	}
	m.activeFilter.Account = "work"
	m.activeFilter.Raw = dashboard.SerializeFilter(m.activeFilter)
	m = pressKey(m, keyEnter)
	if m.activeFilter.Project != "API" || m.activeFilter.Account != "work" || !m.railFocused {
		t.Fatalf("applied filter/focus = %+v focused=%v", m.activeFilter, m.railFocused)
	}
	m.railCursor = 0
	m = pressKey(m, keyEnter)
	if m.activeFilter.Project != "" || m.activeFilter.Account != "work" {
		t.Fatalf("All changed more than Project: %+v", m.activeFilter)
	}
	m = pressKey(m, keyRight)
	if m.railFocused {
		t.Fatal("Right did not return focus to session list")
	}
}

func TestRailFocusMakesSessionActionsInertAndKeepsGlobals(t *testing.T) {
	m := railTestModel()
	m = pressKey(m, keyLeft)
	beforeCursor := m.cursor
	for _, key := range []string{keyA, keySpace, keyClose, keyR, keyC, keyClearStatus, keyColon} {
		after := pressKey(m, key)
		if after.mode != ModeList || after.cursor != beforeCursor || !after.railFocused {
			t.Fatalf("rail-focused %q mutated session surface: mode=%v cursor=%d focus=%v", key, after.mode, after.cursor, after.railFocused)
		}
		m = after
	}
	m = pressKey(m, keyQuestion)
	if m.mode != ModeHelp || m.railFocused {
		t.Fatalf("? from rail = mode %v focus %v", m.mode, m.railFocused)
	}
}

func TestRailToggleViewResizeAndPreviewPreserveProjectScope(t *testing.T) {
	m := railTestModel()
	m.activeFilter = dashboard.Filter{Project: "API"}
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	m = pressKey(m, keyLeft)
	m = pressKey(m, keyToggleRail)
	if m.railRequested || m.railFocused || m.activeFilter.Project != "API" {
		t.Fatalf("hide rail state = requested %v focused %v filter %+v", m.railRequested, m.railFocused, m.activeFilter)
	}
	m = pressKey(m, keyToggleRail)
	m = pressKey(m, keyLeft)
	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	if dashboardGeometry(m).rail || m.railFocused || m.activeFilter.Project != "API" {
		t.Fatalf("resize suppression mutated focus/scope: geom=%+v focused=%v filter=%+v", dashboardGeometry(m), m.railFocused, m.activeFilter)
	}
	updated, _ = m.handleWindowSize(tea.WindowSizeMsg{Width: 220, Height: 30})
	m = updated.(Model)
	m.cfg.Defaults.Preview.Direction = "horizontal"
	m.cfg.Defaults.Preview.Ratio = .5
	m.previewEnabled = true
	m.syncRailState()
	if !dashboardGeometry(m).rail {
		t.Fatal("wide horizontal preview should retain the rail")
	}
	m = pressKey(m, keyToggleView)
	if m.listView != dashboard.ViewProjects || m.railFocused || dashboardGeometry(m).rail || m.activeFilter.Project != "API" {
		t.Fatalf("Projects switch state = view %v focus %v geom=%+v filter=%+v", m.listView, m.railFocused, dashboardGeometry(m), m.activeFilter)
	}
	before := m.railRequested
	m = pressKey(m, keyToggleRail)
	if m.railRequested != before {
		t.Fatal("b toggled the rail outside Urgency view")
	}
}
