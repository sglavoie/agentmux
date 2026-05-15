package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

func TestParseEmpty(t *testing.T) {
	f := Parse("")
	if !f.IsEmpty() {
		t.Errorf("expected empty filter, got %+v", f)
	}
	if f.Raw != "" {
		t.Errorf("expected Raw=\"\", got %q", f.Raw)
	}
}

func TestParseWhitespaceOnly(t *testing.T) {
	f := Parse("   \t  ")
	if !f.IsEmpty() {
		t.Errorf("expected empty filter from whitespace, got %+v", f)
	}
}

func TestParseSingleSubstring(t *testing.T) {
	f := Parse("api")
	if !reflect.DeepEqual(f.Substrings, []string{"api"}) {
		t.Errorf("expected [api], got %v", f.Substrings)
	}
	if f.Account != "" || f.State != "" {
		t.Errorf("expected only substring, got %+v", f)
	}
}

func TestParseAccount(t *testing.T) {
	f := Parse("@work")
	if f.Account != "work" {
		t.Errorf("expected Account=work, got %q", f.Account)
	}
	if len(f.Substrings) != 0 {
		t.Errorf("expected no substrings, got %v", f.Substrings)
	}
}

func TestParseState(t *testing.T) {
	f := Parse("!key")
	if f.State != "key" {
		t.Errorf("expected State=key, got %q", f.State)
	}
}

func TestParseCombined(t *testing.T) {
	f := Parse("api @work !key")
	if !reflect.DeepEqual(f.Substrings, []string{"api"}) {
		t.Errorf("expected substrings=[api], got %v", f.Substrings)
	}
	if f.Account != "work" {
		t.Errorf("expected Account=work, got %q", f.Account)
	}
	if f.State != "key" {
		t.Errorf("expected State=key, got %q", f.State)
	}
}

func TestParseMultipleSubstrings(t *testing.T) {
	f := Parse("api backend")
	if !reflect.DeepEqual(f.Substrings, []string{"api", "backend"}) {
		t.Errorf("expected two substrings, got %v", f.Substrings)
	}
}

func TestParseEscapes(t *testing.T) {
	f := Parse(`\@literal \!banged`)
	if !reflect.DeepEqual(f.Substrings, []string{"@literal", "!banged"}) {
		t.Errorf("expected escaped substrings, got %v", f.Substrings)
	}
	if f.Account != "" || f.State != "" {
		t.Errorf("escapes should not populate Account/State, got %+v", f)
	}
}

func TestParseLoneMarkersLenient(t *testing.T) {
	f := Parse("@ ! api")
	if !reflect.DeepEqual(f.Substrings, []string{"api"}) {
		t.Errorf("expected only [api], got %v", f.Substrings)
	}
	if f.Account != "" || f.State != "" {
		t.Errorf("lone markers should not populate Account/State, got %+v", f)
	}
}

func TestParseLastAccountWins(t *testing.T) {
	f := Parse("@work @personal")
	if f.Account != "personal" {
		t.Errorf("expected last @ to win (personal), got %q", f.Account)
	}
}

func TestParseTrailingSpace(t *testing.T) {
	f := Parse("api   ")
	if !reflect.DeepEqual(f.Substrings, []string{"api"}) {
		t.Errorf("expected trailing space stripped, got %v", f.Substrings)
	}
}

func TestIsEmpty(t *testing.T) {
	cases := map[string]bool{
		"":             true,
		"   ":          true,
		"@":            true, // lone marker
		"!":            true,
		"api":          false,
		"@work":        false,
		"!key":         false,
		"api @w !bolt": false,
	}
	for in, want := range cases {
		got := Parse(in).IsEmpty()
		if got != want {
			t.Errorf("IsEmpty(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMatchEmptyFilter(t *testing.T) {
	f := Filter{}
	s := session.RunningSession{Base: "x", Account: "work"}
	if !f.Match(s, nil, 0) {
		t.Error("empty filter should match every session")
	}
}

func TestMatchSubstringTitle(t *testing.T) {
	f := Parse("api")
	s := session.RunningSession{Base: "W:api", Account: "work"}
	if !f.Match(s, nil, 0) {
		t.Error("expected substring match against Base")
	}
}

func TestMatchSubstringDirectory(t *testing.T) {
	f := Parse("dotfiles")
	s := session.RunningSession{Base: "W:home", Account: "work", Directory: "/home/user/dotfiles"}
	if !f.Match(s, nil, 0) {
		t.Error("expected substring match against Directory")
	}
}

func TestMatchSubstringAccount(t *testing.T) {
	f := Parse("personal")
	s := session.RunningSession{Base: "X", Account: "personal"}
	if !f.Match(s, nil, 0) {
		t.Error("expected substring match against Account")
	}
}

func TestMatchSubstringCaseInsensitive(t *testing.T) {
	f := Parse("API")
	s := session.RunningSession{Base: "w:api"}
	if !f.Match(s, nil, 0) {
		t.Error("expected case-insensitive substring match")
	}
}

func TestMatchMultipleSubstringsAND(t *testing.T) {
	f := Parse("api work")
	good := session.RunningSession{Base: "W:api", Account: "work"}
	bad := session.RunningSession{Base: "W:api", Account: "personal"}
	if !f.Match(good, nil, 0) {
		t.Error("expected match when both substrings present (across columns)")
	}
	if f.Match(bad, nil, 0) {
		t.Error("expected no match when one substring is missing")
	}
}

func TestMatchAccount(t *testing.T) {
	f := Parse("@work")
	good := session.RunningSession{Account: "work"}
	bad := session.RunningSession{Account: "personal"}
	if !f.Match(good, nil, 0) {
		t.Error("expected match for @work on work session")
	}
	if f.Match(bad, nil, 0) {
		t.Error("expected no match for @work on personal session")
	}
}

func TestMatchAccountCaseInsensitive(t *testing.T) {
	f := Parse("@Work")
	s := session.RunningSession{Account: "work"}
	if !f.Match(s, nil, 0) {
		t.Error("expected case-insensitive account match")
	}
}

func TestMatchStateBolt(t *testing.T) {
	f := Parse("!bolt")
	good := session.RunningSession{State: "bolt"}
	bad := session.RunningSession{State: "key"}
	if !f.Match(good, nil, 0) {
		t.Error("expected !bolt to match bolt state")
	}
	if f.Match(bad, nil, 0) {
		t.Error("expected !bolt to reject key state")
	}
}

func TestMatchStateKeyStopError(t *testing.T) {
	cases := map[string]string{
		"!key":   "key",
		"!stop":  "stop",
		"!error": "error",
	}
	for expr, st := range cases {
		f := Parse(expr)
		s := session.RunningSession{State: st}
		if !f.Match(s, nil, 0) {
			t.Errorf("expected %s to match state=%s", expr, st)
		}
	}
}

func TestMatchStateActiveAlias(t *testing.T) {
	f := Parse("!active")
	bolt := session.RunningSession{State: "bolt", WindowID: 1}
	if !f.Match(bolt, nil, time.Minute) {
		t.Error("expected !active to include bolt sessions (group 1)")
	}
	keyS := session.RunningSession{State: "key", WindowID: 2}
	if f.Match(keyS, nil, time.Minute) {
		t.Error("expected !active to exclude attention-group sessions")
	}
}

func TestMatchStateIdleAlias(t *testing.T) {
	f := Parse("!idle")
	stale := session.RunningSession{State: "", WindowID: 1}
	activity := map[int]time.Time{1: time.Now().Add(-time.Hour)}
	if !f.Match(stale, activity, time.Second) {
		t.Error("expected !idle to match stale empty-state session")
	}
	fresh := session.RunningSession{State: "", WindowID: 2}
	if f.Match(fresh, nil, time.Second) {
		t.Error("expected !idle to reject fresh no-record session (Active bucket)")
	}
}

func TestMatchStateUnknownIsLenient(t *testing.T) {
	f := Parse("!fooo")
	s := session.RunningSession{State: "bolt"}
	if !f.Match(s, nil, 0) {
		t.Error("expected unknown state to pass everything through (lenient)")
	}
}

func TestMatchEscapedSubstring(t *testing.T) {
	f := Parse(`\@work`)
	s := session.RunningSession{Base: "@work"}
	if !f.Match(s, nil, 0) {
		t.Error("expected escaped @ to match literal substring")
	}
}

func TestParseHarness(t *testing.T) {
	f := Parse("%claude")
	if f.Harness != "claude" {
		t.Errorf("expected Harness=claude, got %q", f.Harness)
	}
	if len(f.Substrings) != 0 || f.Account != "" || f.State != "" {
		t.Errorf("expected only harness, got %+v", f)
	}
}

func TestParseHarnessUppercaseNormalized(t *testing.T) {
	f := Parse("%Copilot")
	if f.Harness != "copilot" {
		t.Errorf("expected Harness normalized to lowercase, got %q", f.Harness)
	}
}

func TestParseLoneHarnessMarkerLenient(t *testing.T) {
	f := Parse("% api")
	if f.Harness != "" {
		t.Errorf("lone harness marker should not set Harness, got %q", f.Harness)
	}
	if !reflect.DeepEqual(f.Substrings, []string{"api"}) {
		t.Errorf("expected [api] substring, got %v", f.Substrings)
	}
}

func TestIsEmptyHarness(t *testing.T) {
	if !Parse("%").IsEmpty() {
		t.Error("lone harness marker should produce an empty filter")
	}
	if Parse("%claude").IsEmpty() {
		t.Error("harness filter should not be empty")
	}
}

func TestMatchHarnessClaude(t *testing.T) {
	f := Parse("%claude")
	good := session.RunningSession{Harness: "claude"}
	bad := session.RunningSession{Harness: "copilot"}
	empty := session.RunningSession{Harness: ""}
	if !f.Match(good, nil, 0) {
		t.Error("expected harness:claude filter to match claude session")
	}
	if f.Match(bad, nil, 0) {
		t.Error("expected harness:claude filter to reject copilot session")
	}
	if f.Match(empty, nil, 0) {
		t.Error("expected harness:claude filter to reject empty-harness session")
	}
}

func TestMatchHarnessCopilot(t *testing.T) {
	f := Parse("%copilot")
	good := session.RunningSession{Harness: "copilot"}
	bad := session.RunningSession{Harness: "claude"}
	if !f.Match(good, nil, 0) {
		t.Error("expected harness:copilot filter to match copilot session")
	}
	if f.Match(bad, nil, 0) {
		t.Error("expected harness:copilot filter to reject claude session")
	}
}

func TestMatchHarnessCaseInsensitive(t *testing.T) {
	f := Parse("%Claude")
	s := session.RunningSession{Harness: "claude"}
	if !f.Match(s, nil, 0) {
		t.Error("expected case-insensitive harness match")
	}
}

func TestMatchHarnessCombinedWithAccount(t *testing.T) {
	f := Parse("@work %claude")
	good := session.RunningSession{Account: "work", Harness: "claude"}
	wrongHarness := session.RunningSession{Account: "work", Harness: "copilot"}
	wrongAccount := session.RunningSession{Account: "personal", Harness: "claude"}
	if !f.Match(good, nil, 0) {
		t.Error("expected account+harness filter to match work+claude session")
	}
	if f.Match(wrongHarness, nil, 0) {
		t.Error("expected account+harness filter to reject work+copilot session")
	}
	if f.Match(wrongAccount, nil, 0) {
		t.Error("expected account+harness filter to reject personal+claude session")
	}
}
