package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestTokenAtCaret_EmptyInput(t *testing.T) {
	sigil, prefix, from, to := tokenAtCaret("", 0)
	if sigil != 0 || prefix != "" || from != 0 || to != 0 {
		t.Errorf("expected zero values, got (%q, %q, %d, %d)", sigil, prefix, from, to)
	}
}

func TestTokenAtCaret_BareWord(t *testing.T) {
	sigil, prefix, _, _ := tokenAtCaret("api", 3)
	if sigil != 0 {
		t.Errorf("expected no sigil for bare word, got %q", sigil)
	}
	if prefix != "" {
		t.Errorf("expected empty prefix when no sigil, got %q", prefix)
	}
}

func TestTokenAtCaret_AccountSigil(t *testing.T) {
	sigil, prefix, from, to := tokenAtCaret("@wo", 3)
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
	sigil, prefix, from, to := tokenAtCaret("api !ke", 7)
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
	sigil, prefix, _, _ := tokenAtCaret("%", 1)
	if sigil != '%' {
		t.Errorf("expected sigil=%%, got %q", sigil)
	}
	if prefix != "" {
		t.Errorf("expected empty prefix, got %q", prefix)
	}
}

func TestTokenAtCaret_AfterTrailingSpace(t *testing.T) {
	// Caret right after a space → no active token.
	sigil, _, _, _ := tokenAtCaret("@work ", 6)
	if sigil != 0 {
		t.Errorf("expected no sigil after trailing space, got %q", sigil)
	}
}

func TestCandidatesForSigil_Account(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "yellow"}},
	}
	got := candidatesForSigil('@', cfg)
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

// TestCandidatesForSigil_StateCandidatesAreAllRecognized closes the loop the
// two tests above only check one direction of. Each hardcodes a list and asks
// whether the popup offers it; neither notices a candidate the popup offers
// that matchState does NOT recognize, because an unrecognized name is lenient —
// it matches everything, so the filter reads as a working one that just happens
// to select the whole list.
//
// The discriminator is exactly that leniency: a recognized state name rejects
// at least one of the probe rows below, and an unrecognized one cannot reject
// any of them.
func TestCandidatesForSigil_StateCandidatesAreAllRecognized(t *testing.T) {
	probes := []session.RunningSession{
		{State: "key", WindowID: 1},
		{State: "stop", Unseen: true, WindowID: 2},
		{State: "stop", WindowID: 3},
		{State: "bolt", WindowID: 4},
		{State: "warning", WindowID: 5},
		{State: "error", WindowID: 6},
		{State: "subagent", WindowID: 7},
		{State: "compact", WindowID: 8},
		{State: "question", WindowID: 9},
	}
	for _, name := range candidatesForSigil('!', nil) {
		rejected := false
		for _, s := range probes {
			if !matchState(s, name, nil, time.Minute) {
				rejected = true
				break
			}
		}
		if !rejected {
			t.Errorf("completion offers !%s but matchState accepts every probe row, which is what an UNRECOGNIZED name does", name)
		}
	}
}

// TestCandidatesForSigil_StateLeadsWithTheGroupAliases pins the popup order.
// The three aliases select what the list is already grouped by, so they are the
// answer to "!" with no prefix typed; burying them under the icon names would
// make the most useful completion the least reachable one.
func TestCandidatesForSigil_StateLeadsWithTheGroupAliases(t *testing.T) {
	got := candidatesForSigil('!', nil)
	want := []string{"attention", "active", "idle"}
	if len(got) < len(want) {
		t.Fatalf("candidates = %v, too short", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("candidates[%d] = %q, want %q (aliases lead, in bucket order): %v", i, got[i], w, got)
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

func TestTokenAtCaret_QuotedProjectPrefixAndReplacementRange(t *testing.T) {
	input := `api #"My Project" !key`
	caret := len(`api #"My Pro`)
	sigil, prefix, from, to := tokenAtCaret(input, caret)
	if sigil != '#' || prefix != "My Pro" {
		t.Fatalf("tokenAtCaret() = (%q, %q), want (#, My Pro)", sigil, prefix)
	}
	if from != len("api ") || to != len(`api #"My Project"`) {
		t.Fatalf("replacement range = [%d..%d], want [%d..%d]", from, to, len("api "), len(`api #"My Project"`))
	}
}

func TestTokenAtCaret_UnterminatedQuotedProjectRemainsCompletable(t *testing.T) {
	input := `@work #"My Pro`
	sigil, prefix, from, to := tokenAtCaret(input, len(input))
	if sigil != '#' || prefix != "My Pro" || from != len("@work ") || to != len(input) {
		t.Fatalf("tokenAtCaret(%q) = (%q, %q, %d, %d)", input, sigil, prefix, from, to)
	}
}

func TestTokenAtCaret_UnterminatedQuotedProjectDecodesEscapedQuote(t *testing.T) {
	input := `#"My \"`
	sigil, prefix, _, _ := tokenAtCaret(input, len(input))
	if sigil != '#' || prefix != `My "` {
		t.Fatalf("tokenAtCaret(%q) = (%q, %q), want (#, %q)", input, sigil, prefix, `My "`)
	}
}

func TestTokenAtCaret_RejectsQuotedProjectWithAttachedSuffix(t *testing.T) {
	input := `#"My Project"suffix`
	sigil, _, _, _ := tokenAtCaret(input, len(input))
	if sigil != 0 {
		t.Fatalf("malformed quoted selector offered completion for sigil %q", sigil)
	}
}

func TestProjectCompletionUsesContextAndCanonicalLabels(t *testing.T) {
	now := time.Now()
	rows := []session.RunningSession{
		{FamilyLabel: "api", Account: "work", Harness: "claude", State: "bolt", PresentationChangedAt: now.Add(-time.Minute)},
		{FamilyLabel: "API", Account: "work", Harness: "claude", State: "error", PresentationChangedAt: now},
		{FamilyLabel: "Web", Account: "personal", Harness: "claude", State: "key"},
		{FamilyLabel: "Worker", Account: "work", Harness: "copilot", State: "warning"},
		{FamilyLabel: "My Project", Account: "work", Harness: "claude", State: "bolt"},
	}
	var c Completion
	c.Recompute(`@work %claude #`, nil, rows, nil, time.Minute)
	want := []string{"API", "My Project"}
	if !c.Visible || !reflect.DeepEqual(c.Candidates, want) {
		t.Fatalf("project completion = visible %v candidates %v, want %v", c.Visible, c.Candidates, want)
	}
}

func TestProjectCompletionIgnoresExistingProjectSelectorInContext(t *testing.T) {
	rows := []session.RunningSession{
		{FamilyLabel: "API", Account: "work", State: "bolt"},
		{FamilyLabel: "Web", Account: "work", State: "bolt"},
	}
	var c Completion
	c.Recompute(`#old @work #`, nil, rows, nil, time.Minute)
	if !reflect.DeepEqual(c.Candidates, []string{"API", "Web"}) {
		t.Fatalf("existing project selector narrowed contextual source: %v", c.Candidates)
	}
}

func TestProjectCompletionInsertionQuotesAndEscapes(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{label: "My Project", want: `#"My Project" `},
		{label: `quote " and slash \`, want: `#"quote \" and slash \\" `},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			buf := `#"`
			rows := []session.RunningSession{{FamilyLabel: tt.label, State: "bolt"}}
			var c Completion
			c.Recompute(buf, nil, rows, nil, time.Minute)
			if !c.Visible || len(c.Candidates) != 1 {
				t.Fatalf("setup: visible=%v candidates=%v", c.Visible, c.Candidates)
			}
			c.Insert(&buf)
			if buf != tt.want {
				t.Fatalf("Insert() = %q, want %q", buf, tt.want)
			}
			if Parse(buf).Project != tt.label {
				t.Fatalf("inserted selector did not parse back to %q: %q", tt.label, buf)
			}
		})
	}
}
