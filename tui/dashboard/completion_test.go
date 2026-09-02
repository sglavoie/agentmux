package dashboard

import (
	"reflect"
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
		Profiles: []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "yellow"}},
	}
	got := CandidatesForSigil('@', cfg)
	want := []string{"work", "personal"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestCandidatesForSigil_AccountDedupesLegacyAlias(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Color: "blue"}},
		Accounts: []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "yellow"}},
	}
	got := CandidatesForSigil('@', cfg)
	want := []string{"work", "personal"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected dedup, got %v", got)
	}
}

func TestCandidatesForSigil_State(t *testing.T) {
	got := CandidatesForSigil('!', nil)
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
	got := CandidatesForSigil('!', nil)
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
	got := CandidatesForSigil('%', nil)
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
	got := FilterCandidates([]string{"alpha", "beta", "alfresco"}, "al")
	// Both alpha and alfresco are prefix matches.
	if len(got) != 2 || got[0] != "alpha" || got[1] != "alfresco" {
		t.Errorf("expected prefix matches, got %v", got)
	}
}

func TestFilterCandidates_SubstringFallback(t *testing.T) {
	got := FilterCandidates([]string{"foo", "barfoo", "bar"}, "foo")
	// Prefix: foo; substring: barfoo.
	if len(got) != 2 || got[0] != "foo" || got[1] != "barfoo" {
		t.Errorf("expected foo,barfoo, got %v", got)
	}
}

func TestFilterCandidates_CaseInsensitive(t *testing.T) {
	got := FilterCandidates([]string{"Work", "Personal"}, "wo")
	if len(got) != 1 || got[0] != "Work" {
		t.Errorf("expected Work only, got %v", got)
	}
}

func TestFilterCandidates_EmptyPrefixReturnsAll(t *testing.T) {
	got := FilterCandidates([]string{"a", "b"}, "")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("expected full set, got %v", got)
	}
}
