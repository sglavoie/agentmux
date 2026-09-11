package tui

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestListModeLoopRunKeyIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{{
		WindowID:  10,
		Directory: t.TempDir(),
	}})
	m.actionErr = errors.New("keep me")

	updated, cmd := m.Update(keyPress("F"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("F returned cmd %v, want nil", cmd)
	}
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
	if !errors.Is(got.actionErr, m.actionErr) {
		t.Fatalf("actionErr = %v, want preserved %v", got.actionErr, m.actionErr)
	}
}

func TestListModeLoopTailKeyIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{{
		WindowID:  10,
		Directory: t.TempDir(),
	}})
	m.actionErr = errors.New("keep me")

	updated, cmd := m.Update(keyPress("t"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("t returned cmd %v, want nil", cmd)
	}
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
	if !errors.Is(got.actionErr, m.actionErr) {
		t.Fatalf("actionErr = %v, want preserved %v", got.actionErr, m.actionErr)
	}
}

// The timeline is reached by name now — `T` was its dashboard key and is
// retired with the rest of them — but what it opens is unchanged: the detail
// view for the row under the cursor, already switched to the phase timeline.
func TestTimelineCommandOpensDetailTimeline(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{{
		WindowID:  42,
		Directory: t.TempDir(),
	}})

	got, cmd := runDashboardCommand(t, m, commands.ShowTimeline)
	if got.mode != ModeDetail {
		t.Fatalf("mode = %v, want ModeDetail", got.mode)
	}
	if !got.detailViewTimeline {
		t.Fatal("detailViewTimeline = false, want true")
	}
	if got.detailWindowID != 42 {
		t.Fatalf("detailWindowID = %d, want 42", got.detailWindowID)
	}
	if cmd == nil {
		t.Fatal("Show Timeline returned nil cmd, want detail event load")
	}
}

func TestListModeTimelineKeyIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{{
		WindowID:  42,
		Directory: t.TempDir(),
	}})

	updated, cmd := m.Update(keyPress("T"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("T returned cmd %v, want nil", cmd)
	}
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
}
