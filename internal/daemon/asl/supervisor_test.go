package asl

import (
	"sort"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
)

// TestSupervisorRegistry_PendingSpansWindows pins Pending as PendingForWindow
// without the window filter: every lease with unanswered questions on any
// window, and nothing whose projection is answered or whose target is gone.
func TestSupervisorRegistry_PendingSpansWindows(t *testing.T) {
	var r SupervisorRegistry
	for _, target := range []SupervisorTarget{
		{WindowID: 1, Candidate: agentloop.Candidate{RunID: "run-a", StateDir: "/plans/a"}},
		{WindowID: 2, Candidate: agentloop.Candidate{RunID: "run-b", StateDir: "/plans/b"}},
		{WindowID: 3, Candidate: agentloop.Candidate{RunID: "run-c", StateDir: "/plans/c"}},
	} {
		r.Register(target)
	}
	r.SetProjection(LeaseRef{WindowID: 1, Key: "run-a"}, agentloop.SupervisorQuestionProjection{RunID: "run-a", UnansweredQuestions: 2})
	r.SetProjection(LeaseRef{WindowID: 2, Key: "run-b"}, agentloop.SupervisorQuestionProjection{RunID: "run-b", UnansweredQuestions: 1})
	r.SetProjection(LeaseRef{WindowID: 3, Key: "run-c"}, agentloop.SupervisorQuestionProjection{RunID: "run-c"})
	// A projection whose target was never registered has nothing to name.
	r.SetProjection(LeaseRef{WindowID: 4, Key: "run-d"}, agentloop.SupervisorQuestionProjection{RunID: "run-d", UnansweredQuestions: 1})

	pending := r.Pending()
	var runs []string
	for _, p := range pending {
		runs = append(runs, p.Target.Candidate.RunID)
	}
	sort.Strings(runs)
	if len(runs) != 2 || runs[0] != "run-a" || runs[1] != "run-b" {
		t.Fatalf("Pending runs = %v, want [run-a run-b]", runs)
	}
	for _, windowID := range []int{1, 2} {
		if got := r.PendingForWindow(windowID); len(got) != 1 {
			t.Fatalf("PendingForWindow(%d) = %d entries, want 1 — Pending must agree with it", windowID, len(got))
		}
	}
}
