package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func TestReviewPaletteCapturesProjectMembershipAndEpochs(t *testing.T) {
	first := tmuxTestRow("bolt")
	first.Review.Epoch = "old"
	second := first
	second.RuntimeSessionID = runtimesession.ID("rs-22222222222222222222222222222222")
	second.WindowID++
	second.TabID++
	second.State = ""
	m := loadSessions(testModelWithSessions(), []session.RunningSession{first, second})
	m.openCommandPalette()
	ctx := m.commandContext
	// A new session and new attention arrive while the palette is open.
	third := second
	third.RuntimeSessionID = runtimesession.ID("rs-33333333333333333333333333333333")
	third.WindowID++
	third.TabID++
	first.Review.Epoch = "new"
	m = loadSessions(m, []session.RunningSession{first, second, third})
	var sent protocol.Request
	m.daemonCommand = func(r protocol.Request) (protocol.Response, error) {
		sent = r
		return protocol.Response{OK: true, Review: &protocol.SessionReviewResult{UndoToken: "undo", Count: 2}}, nil
	}
	candidate := commandByID(t, "review.ProjectAccounted")
	m, cmd := candidate.Run(m, ctx)
	if cmd == nil {
		t.Fatal("no annotation dispatched")
	}
	msg := cmd().(reviewDoneMsg)
	if len(sent.Review.Targets) != 2 || sent.Review.Targets[string(first.RuntimeSessionID)] != "old" {
		t.Fatalf("retargeted review: %+v", sent.Review)
	}
	next, _ := m.handleReviewDone(msg)
	if next.(Model).reviewUndo != "undo" {
		t.Fatal("missing undo receipt")
	}
}

func TestReviewNoteEditorRetainsDraftOnFailureAndDoesNotMarkAccounted(t *testing.T) {
	s := tmuxTestRow("key")
	s.Review.Note = "Parked"
	m, ctx := commandTestModel(s)
	m, cmd := commandByID(t, "review.SessionNote").Run(m, ctx)
	if cmd != nil || m.mode != ModeReviewNote || m.reviewEditor.Input.Draft != "Parked" {
		t.Fatal("note editor did not open with saved note")
	}
	m.insertReviewNote("\nfor later")
	if strings.Contains(m.reviewEditor.Input.Draft, "\n") {
		t.Fatal("multiline paste not flattened")
	}
	m.daemonCommand = func(r protocol.Request) (protocol.Response, error) {
		if r.Review.Accounted != nil || r.Review.Note == nil {
			t.Fatalf("note unexpectedly acknowledges: %+v", r.Review)
		}
		return protocol.Response{}, errors.New("disk full")
	}
	next, cmd := m.handleReviewNoteKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	result, _ := m.handleReviewDone(cmd().(reviewDoneMsg))
	m = result.(Model)
	if m.mode != ModeReviewNote || m.reviewEditor.Input.Draft != "Parked for later" || !strings.Contains(m.reviewEditor.Error, "disk full") {
		t.Fatal("failed save discarded draft")
	}
	for _, width := range []int{24, 60, 100} {
		m.width = width
		if got := renderReviewNoteBox(m); !strings.Contains(got, "note") {
			t.Fatalf("missing editor at width %d", width)
		}
	}
}

func TestReviewDoesNotHideUnresolvedAttention(t *testing.T) {
	for _, icon := range []string{"key", "question", "error", "stop"} {
		s := tmuxTestRow(icon)
		s.Unseen = true
		s.Review = protocol.SessionReview{Accounted: true, Reviewed: true}
		got := dashboard.NeedsYou(s, nil, 0)
		if got != (icon != "stop") {
			t.Fatalf("%s attention=%v", icon, got)
		}
	}
}

func TestReviewGroupSummaryIncludesNewAndResurfacedSessions(t *testing.T) {
	first := tmuxTestRow("bolt")
	first.Review = protocol.SessionReview{Reviewed: true, Accounted: true}
	second := first
	second.State = ""
	if got := dashboard.ReviewSummary([]session.RunningSession{first, second}); got != "✓ Accounted for" {
		t.Fatal(got)
	}
	second.Review.Accounted = false
	if got := dashboard.ReviewSummary([]session.RunningSession{first, second}); got != "1 needs review" {
		t.Fatal(got)
	}
	second.Review.Reviewed = false
	if got := dashboard.ReviewSummary([]session.RunningSession{first, second}); got != "1 needs review" {
		t.Fatal(got)
	}
}

func TestReviewUndoRunsWithoutSelectedSession(t *testing.T) {
	m := testModelWithSessions()
	m.reviewUndo = "receipt"
	var sent protocol.Request
	m.daemonCommand = func(r protocol.Request) (protocol.Response, error) {
		sent = r
		return protocol.Response{OK: true, Review: &protocol.SessionReviewResult{}}, nil
	}
	candidate := commandByID(t, "review.undo")
	if !candidate.AvailabilityFor(m, commands.Context{}).Available {
		t.Fatal("undo requires a selected session")
	}
	_, cmd := candidate.Run(m, commands.Context{})
	cmd()
	if sent.Review.UndoToken != "receipt" || len(sent.Review.Targets) != 0 {
		t.Fatal("undo was retargeted")
	}
}
