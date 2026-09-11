package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func completionTestModel() Model {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
	}
	return Model{cfg: cfg}
}

func TestRecomputeCompletion_HidesWithoutSigil(t *testing.T) {
	m := completionTestModel()
	m.filterText = "api"
	m.recomputeCompletion()
	if m.completion.Visible {
		t.Error("expected completion hidden for bare word")
	}
}

func TestRecomputeCompletion_ShowsForAccountSigil(t *testing.T) {
	m := completionTestModel()
	m.filterText = "@"
	m.recomputeCompletion()
	if !m.completion.Visible {
		t.Fatal("expected completion visible for @")
	}
	if len(m.completion.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %v", m.completion.Candidates)
	}
}

func TestRecomputeCompletion_NarrowsByPrefix(t *testing.T) {
	m := completionTestModel()
	m.filterText = "@p"
	m.recomputeCompletion()
	if !m.completion.Visible {
		t.Fatal("expected completion visible")
	}
	if len(m.completion.Candidates) != 1 || m.completion.Candidates[0] != "personal" {
		t.Errorf("expected [personal], got %v", m.completion.Candidates)
	}
}

func TestRecomputeCompletion_HidesAfterTrailingSpace(t *testing.T) {
	m := completionTestModel()
	m.filterText = "@work "
	m.recomputeCompletion()
	if m.completion.Visible {
		t.Error("expected popup hidden after token-closing space")
	}
}

func TestRecomputeCompletion_HidesWhenNoMatch(t *testing.T) {
	m := completionTestModel()
	m.filterText = "@zzz"
	m.recomputeCompletion()
	if m.completion.Visible {
		t.Error("expected popup hidden when prefix matches nothing")
	}
}

func TestCycleCompletion_WrapsBothDirections(t *testing.T) {
	m := completionTestModel()
	m.filterText = "@"
	m.recomputeCompletion()
	if !m.completion.Visible || len(m.completion.Candidates) != 2 {
		t.Fatalf("setup: visible=%v candidates=%v", m.completion.Visible, m.completion.Candidates)
	}
	m.cycleCompletion(1)
	if m.completion.Selected != 1 {
		t.Errorf("after +1: expected selected=1, got %d", m.completion.Selected)
	}
	m.cycleCompletion(1)
	if m.completion.Selected != 0 {
		t.Errorf("after wrap: expected selected=0, got %d", m.completion.Selected)
	}
	m.cycleCompletion(-1)
	if m.completion.Selected != 1 {
		t.Errorf("after -1 wrap: expected selected=1, got %d", m.completion.Selected)
	}
}

func TestInsertCompletion_ReplacesTokenAndAppendsSpace(t *testing.T) {
	m := completionTestModel()
	m.filterText = "api @wo"
	m.recomputeCompletion()
	if !m.completion.Visible {
		t.Fatal("setup: expected completion visible")
	}
	// Candidates filtered to "work" only.
	if len(m.completion.Candidates) != 1 || m.completion.Candidates[0] != "work" {
		t.Fatalf("setup: expected [work], got %v", m.completion.Candidates)
	}
	m.insertCompletion()
	if m.filterText != "api @work " {
		t.Errorf("expected 'api @work ', got %q", m.filterText)
	}
	if m.completion.Visible {
		t.Error("expected popup closed after insert")
	}
}

func TestInsertCompletion_StateAtEnd(t *testing.T) {
	m := completionTestModel()
	m.filterText = "!ke"
	m.recomputeCompletion()
	if !m.completion.Visible {
		t.Fatal("setup: expected popup visible")
	}
	// 'ke' prefix-matches 'key' only.
	if len(m.completion.Candidates) != 1 || m.completion.Candidates[0] != "key" {
		t.Fatalf("setup: expected [key], got %v", m.completion.Candidates)
	}
	m.insertCompletion()
	if m.filterText != "!key " {
		t.Errorf("expected '!key ', got %q", m.filterText)
	}
}

func TestRenderCompletion_HiddenReturnsEmpty(t *testing.T) {
	m := completionTestModel()
	if got := renderCompletion(m); got != "" {
		t.Errorf("expected empty when hidden, got %q", got)
	}
}

func TestRenderCompletion_RendersCandidates(t *testing.T) {
	m := completionTestModel()
	m.filterText = "@"
	m.recomputeCompletion()
	out := stripAnsi(renderCompletion(m))
	for _, want := range []string{"@work", "@personal"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in rendered popup, got %q", want, out)
		}
	}
}

func TestRecomputeCompletion_ProjectCandidatesUseLiveSessions(t *testing.T) {
	m := completionTestModel()
	m.sessions = []session.RunningSession{
		{FamilyLabel: "API", Account: "work", State: "error"},
		{FamilyLabel: "Web", Account: "personal", State: "bolt"},
	}
	m.filterText = "@work #"
	m.recomputeCompletion()
	if !m.completion.Visible || len(m.completion.Candidates) != 1 || m.completion.Candidates[0] != "API" {
		t.Fatalf("project completion = visible %v candidates %v", m.completion.Visible, m.completion.Candidates)
	}
	m.insertCompletion()
	if m.filterText != "@work #API " {
		t.Fatalf("inserted project completion = %q", m.filterText)
	}
}
