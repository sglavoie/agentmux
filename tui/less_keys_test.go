package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
)

func planSearchNavModel(total, height int) Model {
	results := make([]plans.Result, total)
	for i := range results {
		results[i] = plans.Result{Meta: plans.PlanMeta{Name: indexedRowName(i)}}
	}
	return Model{
		mode:        ModePlanSearch,
		width:       100,
		height:      height,
		cfg:         &config.Config{},
		planResults: results,
	}
}

func sessionSearchNavModel(total, height int) Model {
	results := make([]transcript.Result, total)
	for i := range results {
		results[i] = transcript.Result{
			Meta: transcript.SessionMeta{
				ID:      indexedRowName(i),
				Project: indexedRowName(i),
			},
		}
	}
	return Model{
		mode:           ModeSessionSearch,
		width:          100,
		height:         height,
		cfg:            &config.Config{},
		sessionResults: results,
	}
}

func sessionViewNavModel(total, height int) Model {
	messages := make([]transcript.Message, total)
	for i := range messages {
		messages[i] = transcript.Message{Role: "user", Text: indexedRowName(i)}
	}
	return Model{
		mode:           ModeSessionView,
		width:          100,
		height:         height,
		cfg:            &config.Config{},
		viewerMessages: messages,
	}
}

func wrappedPlanViewNavModel() Model {
	return Model{
		mode:             ModePlanView,
		width:            8,
		height:           8,
		cfg:              &config.Config{},
		planViewerLines:  []string{"abcdefghijklmnopqrstuvwxTAIL"},
		planViewerCursor: 0,
	}
}

func TestPlanSearchLessKeysHalfAndFullPage(t *testing.T) {
	base := planSearchNavModel(40, 15)
	half := halfPageStep(planSearchVisibleHeight(base))

	m := pressKey(base, "ctrl+d")
	if m.planCursor != half {
		t.Fatalf("ctrl+d plan cursor = %d, want %d", m.planCursor, half)
	}
	m = pressKey(m, "ctrl+u")
	if m.planCursor != 0 {
		t.Fatalf("ctrl+u plan cursor = %d, want 0", m.planCursor)
	}

	pg := pressKey(base, "pgdown")
	ctrl := pressKey(base, "ctrl+f")
	if ctrl.planCursor != pg.planCursor || ctrl.planOffset != pg.planOffset {
		t.Fatalf("ctrl+f plan state = cursor %d offset %d, want pgdown cursor %d offset %d",
			ctrl.planCursor, ctrl.planOffset, pg.planCursor, pg.planOffset)
	}
	pg = pressKey(pg, "pgup")
	ctrl = pressKey(ctrl, "ctrl+b")
	if ctrl.planCursor != pg.planCursor || ctrl.planOffset != pg.planOffset {
		t.Fatalf("ctrl+b plan state = cursor %d offset %d, want pgup cursor %d offset %d",
			ctrl.planCursor, ctrl.planOffset, pg.planCursor, pg.planOffset)
	}

	m = planSearchNavModel(3, 15)
	m.planCursor = len(m.planResults) - 1
	m = pressKey(m, "ctrl+d")
	if m.planCursor != len(m.planResults)-1 {
		t.Fatalf("ctrl+d at bottom plan cursor = %d, want last row", m.planCursor)
	}
}

func TestSessionSearchLessKeysHalfAndFullPage(t *testing.T) {
	base := sessionSearchNavModel(40, 15)
	half := halfPageStep(sessionSearchVisibleHeight(base))

	m := pressKey(base, "ctrl+d")
	if m.sessionCursor != half {
		t.Fatalf("ctrl+d session cursor = %d, want %d", m.sessionCursor, half)
	}
	m = pressKey(m, "ctrl+u")
	if m.sessionCursor != 0 {
		t.Fatalf("ctrl+u session cursor = %d, want 0", m.sessionCursor)
	}

	pg := pressKey(base, "pgdown")
	ctrl := pressKey(base, "ctrl+f")
	if ctrl.sessionCursor != pg.sessionCursor || ctrl.sessionOffset != pg.sessionOffset {
		t.Fatalf("ctrl+f session state = cursor %d offset %d, want pgdown cursor %d offset %d",
			ctrl.sessionCursor, ctrl.sessionOffset, pg.sessionCursor, pg.sessionOffset)
	}
	pg = pressKey(pg, "pgup")
	ctrl = pressKey(ctrl, "ctrl+b")
	if ctrl.sessionCursor != pg.sessionCursor || ctrl.sessionOffset != pg.sessionOffset {
		t.Fatalf("ctrl+b session state = cursor %d offset %d, want pgup cursor %d offset %d",
			ctrl.sessionCursor, ctrl.sessionOffset, pg.sessionCursor, pg.sessionOffset)
	}

	m = sessionSearchNavModel(3, 15)
	m.sessionCursor = len(m.sessionResults) - 1
	m = pressKey(m, "ctrl+d")
	if m.sessionCursor != len(m.sessionResults)-1 {
		t.Fatalf("ctrl+d at bottom session cursor = %d, want last row", m.sessionCursor)
	}
}

func TestSessionViewerLessKeysHalfAndFullPage(t *testing.T) {
	base := sessionViewNavModel(40, 15)
	half := halfPageStep(sessionViewVisibleHeight(base))

	m := pressKey(base, "ctrl+d")
	if m.viewerCursor != half {
		t.Fatalf("ctrl+d viewer cursor = %d, want %d", m.viewerCursor, half)
	}
	m = pressKey(m, "ctrl+u")
	if m.viewerCursor != 0 {
		t.Fatalf("ctrl+u viewer cursor = %d, want 0", m.viewerCursor)
	}

	pg := pressKey(base, "pgdown")
	ctrl := pressKey(base, "ctrl+f")
	if ctrl.viewerCursor != pg.viewerCursor || ctrl.viewerScroll != pg.viewerScroll {
		t.Fatalf("ctrl+f viewer state = cursor %d scroll %d, want pgdown cursor %d scroll %d",
			ctrl.viewerCursor, ctrl.viewerScroll, pg.viewerCursor, pg.viewerScroll)
	}
	pg = pressKey(pg, "pgup")
	ctrl = pressKey(ctrl, "ctrl+b")
	if ctrl.viewerCursor != pg.viewerCursor || ctrl.viewerScroll != pg.viewerScroll {
		t.Fatalf("ctrl+b viewer state = cursor %d scroll %d, want pgup cursor %d scroll %d",
			ctrl.viewerCursor, ctrl.viewerScroll, pg.viewerCursor, pg.viewerScroll)
	}

	m = sessionViewNavModel(3, 15)
	m.viewerCursor = len(m.viewerMessages) - 1
	m = pressKey(m, "ctrl+d")
	if m.viewerCursor != len(m.viewerMessages)-1 {
		t.Fatalf("ctrl+d at bottom viewer cursor = %d, want last row", m.viewerCursor)
	}
}

func TestPlanViewerLessKeysScrollWrappedRows(t *testing.T) {
	base := wrappedPlanViewNavModel()
	rows := planViewerRows(base)
	if len(rows) < 4 {
		t.Fatalf("setup: wrapped plan rows = %d, want at least 4", len(rows))
	}
	visible := planViewerVisibleHeight(base)
	wantHalf := halfPageStep(tuiplans.PageStep(len(rows), 0, visible))

	m := pressKey(base, "ctrl+d")
	if m.planViewerCursor != 0 {
		t.Fatalf("ctrl+d plan viewer cursor = %d, want same logical line 0", m.planViewerCursor)
	}
	if m.planViewerScroll != wantHalf {
		t.Fatalf("ctrl+d plan viewer scroll = %d, want %d visual row", m.planViewerScroll, wantHalf)
	}
	m = pressKey(m, "ctrl+u")
	if m.planViewerCursor != 0 || m.planViewerScroll != 0 {
		t.Fatalf("ctrl+u plan viewer state = cursor %d scroll %d, want cursor 0 scroll 0",
			m.planViewerCursor, m.planViewerScroll)
	}

	pg := pressKey(base, "pgdown")
	ctrl := pressKey(base, "ctrl+f")
	if ctrl.planViewerCursor != pg.planViewerCursor || ctrl.planViewerScroll != pg.planViewerScroll {
		t.Fatalf("ctrl+f plan viewer state = cursor %d scroll %d, want pgdown cursor %d scroll %d",
			ctrl.planViewerCursor, ctrl.planViewerScroll, pg.planViewerCursor, pg.planViewerScroll)
	}
	pg = pressKey(pg, "pgup")
	ctrl = pressKey(ctrl, "ctrl+b")
	if ctrl.planViewerCursor != pg.planViewerCursor || ctrl.planViewerScroll != pg.planViewerScroll {
		t.Fatalf("ctrl+b plan viewer state = cursor %d scroll %d, want pgup cursor %d scroll %d",
			ctrl.planViewerCursor, ctrl.planViewerScroll, pg.planViewerCursor, pg.planViewerScroll)
	}
}
