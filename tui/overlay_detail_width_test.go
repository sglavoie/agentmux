package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestDetailHeaderPadsEmojiTitleIconColumn locks in that the detail
// overlay header routes the project title through layout.RenderTitleIcon
// so a leading pictograph occupies the 2-cell title-icon column without
// gaining a phantom space.
func TestDetailHeaderPadsEmojiTitleIconColumn(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, WindowID: 10, Base: "🗣commscoach", Project: "🗣commscoach", Account: "work", State: "bolt"},
	})
	mm, _ := m.enterDetailMode(10, false)
	m = mm.(Model)
	m.detailLoading = false

	out := stripAnsi(detailHeader(m))
	if !strings.Contains(out, "🗣commscoach") {
		t.Errorf("expected title-icon column without phantom space, got:\n%s", out)
	}
}

func TestActionsHeaderPadsEmojiTitleIcon(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, WindowID: 10, Base: "🗣commscoach", Project: "🗣commscoach", Account: "work", State: "bolt"},
	})
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m, 80))
	if !strings.Contains(out, "Actions for 🗣commscoach") {
		t.Errorf("expected title-icon column without phantom space in actions header, got:\n%s", out)
	}
}

// TestConfirmHeaderPadsEmojiTitleIcon mirrors the detail test for the
// confirm overlay: the project title in the y/n header should route
// through layout.RenderTitleIcon so leading pictographs use the title-icon
// width policy.
func TestConfirmHeaderPadsEmojiTitleIcon(t *testing.T) {
	m := Model{
		mode:               ModeConfirm,
		confirmAction:      "close",
		confirmConsequence: wantCloseConsequence,
		targetSession: session.RunningSession{
			Base:    "🗣commscoach",
			Project: "🗣commscoach",
		},
	}
	out := stripAnsi(renderConfirm(m))
	if !strings.Contains(out, `Close "🗣commscoach"?`) {
		t.Errorf("expected title-icon column without phantom space in confirm header, got:\n%s", out)
	}
}

// TestDeleteChoiceHeaderPadsEmojiTitleIcon mirrors the confirm test for
// the delete-choice overlay header.
func TestDeleteChoiceHeaderPadsEmojiTitleIcon(t *testing.T) {
	target := transcript.SessionMeta{
		ID:      "abc123",
		Harness: "claude",
		Profile: "work",
		Project: "🗣commscoach",
		Path:    "/tmp/demo.jsonl",
	}
	m := Model{
		mode:         ModeDeleteChoice,
		width:        100,
		height:       40,
		deleteTarget: &target,
	}
	out := stripAnsi(renderDeleteChoice(m))
	if !strings.Contains(out, `Delete "🗣commscoach"?`) {
		t.Errorf("expected title-icon column without phantom space in delete-choice header, got:\n%s", out)
	}
}
