package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestWindowSizeMsg_ResizeKeepsSessionSearchCursorVisible(t *testing.T) {
	results := make([]transcript.Result, 60)
	for i := range results {
		results[i] = transcript.Result{
			Meta: transcript.SessionMeta{
				ID:      indexedRowName(i),
				Project: indexedRowName(i),
			},
		}
	}
	m := Model{
		mode:           ModeSessionSearch,
		width:          100,
		height:         40,
		cfg:            &config.Config{},
		sessionResults: results,
		sessionCursor:  50,
	}
	scrollSessionSearchToCursor(&m)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updated.(Model)

	out := stripAnsi(renderSessionSearchList(m, sessionSearchVisibleHeight(m)))
	cursorLine := indexedRowName(m.sessionCursor)
	if !strings.Contains(out, cursorLine) {
		t.Fatalf("session search cursor row %q not rendered after resize (cursor=%d offset=%d)\n%s",
			cursorLine, m.sessionCursor, m.sessionOffset, out)
	}
}

func TestWindowSizeMsg_ResizeKeepsPlanSearchCursorVisible(t *testing.T) {
	results := make([]plans.Result, 60)
	for i := range results {
		name := indexedRowName(i)
		results[i] = plans.Result{
			Meta: plans.PlanMeta{
				Name: name,
			},
		}
	}
	m := Model{
		mode:        ModePlanSearch,
		width:       100,
		height:      40,
		cfg:         &config.Config{},
		planResults: results,
		planCursor:  50,
	}
	scrollPlanSearchToCursor(&m)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updated.(Model)

	out := stripAnsi(renderPlanSearchList(m, planSearchVisibleHeight(m)))
	cursorLine := indexedRowName(m.planCursor)
	if !strings.Contains(out, cursorLine) {
		t.Fatalf("plan search cursor row %q not rendered after resize (cursor=%d offset=%d)\n%s",
			cursorLine, m.planCursor, m.planOffset, out)
	}
}
