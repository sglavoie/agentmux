package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
)

func TestWindowSizeMsg_ResizeKeepsSessionSearchCursorVisible(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func TestWindowSizeMsg_ResizeClampsPlanViewerVisualScroll(t *testing.T) {
	t.Parallel()

	m := Model{
		mode:   ModePlanView,
		width:  8,
		height: 8,
		cfg:    &config.Config{},
		planViewerLines: []string{
			"abcdefghijklmnopqrstuvwx",
			"cursor line",
		},
		planViewerCursor: 1,
		planViewerScroll: 99,
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = updated.(Model)

	maxScroll := tuiplans.MaxScrollOffset(len(planViewerRows(m)), planViewerVisibleHeight(m))
	if m.planViewerScroll > maxScroll {
		t.Fatalf("plan viewer scroll = %d, want <= wrapped-row max %d", m.planViewerScroll, maxScroll)
	}
	out := stripAnsi(renderPlanViewBody(m, planViewerVisibleHeight(m)))
	if !strings.Contains(out, "cursor line") {
		t.Fatalf("plan viewer cursor row not rendered after resize (cursor=%d scroll=%d)\n%s",
			m.planViewerCursor, m.planViewerScroll, out)
	}
}
