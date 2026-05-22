package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestTokenAtCaret_EmptyInput(t *testing.T) {
	sigil, prefix, from, to := TokenAtCaret("", 0)
	if sigil != 0 || prefix != "" || from != 0 || to != 0 {
		t.Errorf("expected zero values, got (%q, %q, %d, %d)", sigil, prefix, from, to)
	}
}

func TestTokenAtCaret_BareWord(t *testing.T) {
	sigil, prefix, _, _ := TokenAtCaret("api", 3)
	if sigil != 0 {
		t.Errorf("expected no sigil for bare word, got %q", sigil)
	}
	if prefix != "" {
		t.Errorf("expected empty prefix when no sigil, got %q", prefix)
	}
}

func TestTokenAtCaret_AccountSigil(t *testing.T) {
	sigil, prefix, from, to := TokenAtCaret("@wo", 3)
	if sigil != '@' {
		t.Errorf("expected sigil=@, got %q", sigil)
	}
	if prefix != "wo" {
		t.Errorf("expected prefix=wo, got %q", prefix)
	}
	if from != 0 || to != 3 {
		t.Errorf("expected replace range [0..3], got [%d..%d]", from, to)
	}
}

func TestTokenAtCaret_StateSigilAfterWhitespace(t *testing.T) {
	sigil, prefix, from, to := TokenAtCaret("api !ke", 7)
	if sigil != '!' {
		t.Errorf("expected sigil=!, got %q", sigil)
	}
	if prefix != "ke" {
		t.Errorf("expected prefix=ke, got %q", prefix)
	}
	if from != 4 || to != 7 {
		t.Errorf("expected replace range [4..7], got [%d..%d]", from, to)
	}
}

func TestTokenAtCaret_HarnessSigilNoPrefix(t *testing.T) {
	sigil, prefix, _, _ := TokenAtCaret("%", 1)
	if sigil != '%' {
		t.Errorf("expected sigil=%%, got %q", sigil)
	}
	if prefix != "" {
		t.Errorf("expected empty prefix, got %q", prefix)
	}
}

func TestTokenAtCaret_AfterTrailingSpace(t *testing.T) {
	// Caret right after a space → no active token.
	sigil, _, _, _ := TokenAtCaret("@work ", 6)
	if sigil != 0 {
		t.Errorf("expected no sigil after trailing space, got %q", sigil)
	}
}

func TestCandidatesForSigil_Account(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work"}, {Name: "personal"}},
	}
	got := candidatesForSigil('@', cfg)
	want := []string{"work", "personal"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestCandidatesForSigil_AccountDedupesLegacyAlias(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work"}},
		Accounts: []config.Account{{Name: "work"}, {Name: "personal"}},
	}
	got := candidatesForSigil('@', cfg)
	want := []string{"work", "personal"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected dedup, got %v", got)
	}
}

func TestCandidatesForSigil_State(t *testing.T) {
	got := candidatesForSigil('!', nil)
	for _, want := range []string{"active", "idle", "bolt", "key", "stop", "warning", "error"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected state %q in candidates, got %v", want, got)
		}
	}
}

// TestCandidatesForSigil_StateMirrorsMatchState pins the completion popup to
// the full set of state names that matchState in filter.go actually
// recognizes. Without subagent/compact/question, typing "!s" would suggest
// only "stop" even though "!subagent" is a real, working filter.
func TestCandidatesForSigil_StateMirrorsMatchState(t *testing.T) {
	got := candidatesForSigil('!', nil)
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, want := range []string{"subagent", "compact", "question"} {
		if !have[want] {
			t.Errorf("expected state %q (matchState accepts it) in candidates, got %v", want, got)
		}
	}
}

func TestCandidatesForSigil_HarnessFromRegistry(t *testing.T) {
	got := candidatesForSigil('%', nil)
	if len(got) == 0 {
		t.Fatal("expected non-empty harness names")
	}
	for _, want := range []string{"claude", "copilot"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected harness %q, got %v", want, got)
		}
	}
}

func TestFilterCandidates_PrefixFirst(t *testing.T) {
	got := filterCandidates([]string{"alpha", "beta", "alfresco"}, "al")
	// Both alpha and alfresco are prefix matches.
	if len(got) != 2 || got[0] != "alpha" || got[1] != "alfresco" {
		t.Errorf("expected prefix matches, got %v", got)
	}
}

func TestFilterCandidates_SubstringFallback(t *testing.T) {
	got := filterCandidates([]string{"foo", "barfoo", "bar"}, "foo")
	// Prefix: foo; substring: barfoo.
	if len(got) != 2 || got[0] != "foo" || got[1] != "barfoo" {
		t.Errorf("expected foo,barfoo, got %v", got)
	}
}

func TestFilterCandidates_CaseInsensitive(t *testing.T) {
	got := filterCandidates([]string{"Work", "Personal"}, "wo")
	if len(got) != 1 || got[0] != "Work" {
		t.Errorf("expected Work only, got %v", got)
	}
}

func TestFilterCandidates_EmptyPrefixReturnsAll(t *testing.T) {
	got := filterCandidates([]string{"a", "b"}, "")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("expected full set, got %v", got)
	}
}

func completionTestModel() Model {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "green"},
		},
	}
	return Model{cfg: cfg, focusedChipIdx: -1}
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
