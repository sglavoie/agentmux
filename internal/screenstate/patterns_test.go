package screenstate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/contextlimit"
)

// restorePatterns snapshots the installed sets so a test that calls Load — which
// mutates a package global by design — cannot leak into the next one. Load also
// installs contextlimit's set in that package, so the snapshot spans both.
func restorePatterns(t *testing.T) {
	t.Helper()
	patternsMu.RLock()
	saved := patternSets
	patternsMu.RUnlock()
	savedContextLimit := contextlimit.InstalledPatterns()
	t.Cleanup(func() {
		install(saved)
		contextlimit.InstallPatterns(savedContextLimit)
	})
}

// writeOverride writes a screenstate override into a fresh config dir and
// returns the config dir Load should be pointed at.
func writeOverride(t *testing.T, harnessName, body string) string {
	t.Helper()
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, patternsDirName)
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}
	path := filepath.Join(overrideDir, harnessName+".toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	return dir
}

// embeddedPatternFile returns a shipped pattern file's text, so a test can
// build an override that differs from the release's own copy in exactly one
// clause rather than in every clause it did not think to include.
func embeddedPatternFile(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(defaultPatternFiles, embeddedPatternPrefix+name+".toml")
	if err != nil {
		t.Fatalf("read embedded %s patterns: %v", name, err)
	}
	return string(data)
}

func warningsOfKind(warnings []LoadWarning, kind LoadWarningKind) []LoadWarning {
	var found []LoadWarning
	for _, warning := range warnings {
		if warning.Kind == kind {
			found = append(found, warning)
		}
	}
	return found
}

func TestEmbeddedPatternsLoadForEveryAdapter(t *testing.T) {
	sets, warnings := loadEmbedded()
	if len(warnings) != 0 {
		t.Fatalf("embedded patterns must validate, got warnings: %v", warnings)
	}
	for harnessName := range adapters {
		set, ok := sets[harnessName]
		if !ok {
			t.Fatalf("no embedded pattern file for adapter %q", harnessName)
		}
		if set.Harness() != harnessName {
			t.Fatalf("embedded %q declares harness %q", harnessName, set.Harness())
		}
		if set.Source() != SourceEmbedded {
			t.Fatalf("embedded %q source = %q, want %q", harnessName, set.Source(), SourceEmbedded)
		}
	}
}

func TestPatternsForReturnsEmbeddedWithoutLoad(t *testing.T) {
	set, ok := PatternsFor("Claude")
	if !ok {
		t.Fatal("PatternsFor(claude) missing before Load; defaults must be installed at init")
	}
	if set.Source() != SourceEmbedded {
		t.Fatalf("source = %q, want %q", set.Source(), SourceEmbedded)
	}
	if _, ok := PatternsFor("copilot"); ok {
		t.Fatal("PatternsFor must not invent a set for an unknown harness")
	}
}

func TestDecodePatternsRejectsUnknownKey(t *testing.T) {
	cases := map[string]string{
		"top level": `
harness = "claude"
priority = 1100
`,
		"unknown section": `
harness = "claude"
[line_regex]
working = "x"
`,
		"unknown group key": `
harness = "claude"
[phrases]
allow = ["allow"]
[group.grant]
all = ["allow"]
none = ["allow"]
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodePatterns([]byte(body), "claude")
			if err == nil {
				t.Fatal("want an error for an unknown key")
			}
			if !strings.Contains(err.Error(), "unknown key") {
				t.Fatalf("error = %v, want it to name the unknown key", err)
			}
		})
	}
}

func TestDecodePatternsRejectsUncompilableRegex(t *testing.T) {
	_, err := decodePatterns([]byte(`
harness = "claude"
[regex]
composer_line = "([unclosed"
`), "claude")
	if err == nil || !strings.Contains(err.Error(), "regex.composer_line") {
		t.Fatalf("err = %v, want a compile failure naming regex.composer_line", err)
	}
}

func TestDecodePatternsRejectsOversizedPattern(t *testing.T) {
	oversized := strings.Repeat("a", maxPatternChars+1)
	_, err := decodePatterns([]byte(`
harness = "claude"
[regex]
huge = "`+oversized+`"
`), "claude")
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("err = %v, want an over-limit rejection", err)
	}

	atLimit := strings.Repeat("a", maxPatternChars)
	if _, err := decodePatterns([]byte(`
harness = "claude"
[regex]
big = "`+atLimit+`"
`), "claude"); err != nil {
		t.Fatalf("a pattern exactly at the limit must be accepted, got %v", err)
	}
}

func TestDecodePatternsRejectsOversizedPhraseList(t *testing.T) {
	phrases := make([]string, 0, maxPhraseListEntries+1)
	for i := 0; i <= maxPhraseListEntries; i++ {
		phrases = append(phrases, `"phrase `+string(rune('a'+i%26))+string(rune('a'+i/26))+`"`)
	}
	_, err := decodePatterns([]byte(`
harness = "claude"
[phrases]
blocking = [`+strings.Join(phrases, ", ")+`]
`), "claude")
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("err = %v, want an over-limit rejection", err)
	}

	if _, err := decodePatterns([]byte(`
harness = "claude"
[phrases]
blocking = [`+strings.Join(phrases[:maxPhraseListEntries], ", ")+`]
`), "claude"); err != nil {
		t.Fatalf("a phrase list exactly at the limit must be accepted, got %v", err)
	}
}

func TestDecodePatternsRejectsEmptyRequiredFields(t *testing.T) {
	cases := map[string]string{
		"no harness":      "[regex]\nx = \"a\"\n",
		"empty regex":     "harness = \"claude\"\n[regex]\nx = \"\"\n",
		"empty phrases":   "harness = \"claude\"\n[phrases]\nx = []\n",
		"blank phrase":    "harness = \"claude\"\n[phrases]\nx = [\"  \"]\n",
		"empty group":     "harness = \"claude\"\n[group.g]\nall = []\nany = []\n",
		"wrong harness":   "harness = \"codex\"\n",
		"uppercase copy":  "harness = \"claude\"\n[phrases]\nx = [\"Do You Want\"]\n",
		"unknown ref":     "harness = \"claude\"\n[group.g]\nall = [\"nope\"]\n",
		"duplicate name":  "harness = \"claude\"\n[regex]\nx = \"a\"\n[phrases]\nx = [\"a\"]\n",
		"reference cycle": "harness = \"claude\"\n[group.a]\nall = [\"b\"]\n[group.b]\nall = [\"a\"]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodePatterns([]byte(body), "claude"); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
}

// TestPatternsMatchCoversAdapterBooleanShapes pins the three boolean shapes the
// extraction sessions must be able to express: `allow AND permission`
// (codex_prompt.go:55), `(nav || submit) && paneAction` (codex_prompt.go:90),
// and the context-limit marker's nested three-group conditional
// (contextlimit.hasMarker).
//
// The third shape is expressible here but is NOT how contextlimit ships: that
// conditional early-returns, which no all/any group can say, so session 5 kept
// the shape in Go and externalized only the copy. This case stays as the
// schema's proof that a three-group nest resolves.
func TestPatternsMatchCoversAdapterBooleanShapes(t *testing.T) {
	set, err := decodePatterns([]byte(`
harness = "claude"

[regex]
choice_line = "(?m)^\\s*❯\\s*\\d+\\."

[phrases]
allow = ["allow"]
permission = ["permission"]
question_navigation = ["to navigate questions", "change question"]
answer_submission = ["to submit answer", "to submit all"]
request_pane_action = ["add notes", "none of the above", "proceed"]
limit_reached = ["context limit reached"]
window_full = ["context window is full", "context window full"]
compact = ["/compact"]
clear = ["/clear"]
compact_or_clear = ["/compact or /clear"]
continue_hint = ["continue"]

[group.permission_grant]
all = ["allow", "permission"]

[group.request_user_input]
all = ["request_pane_action"]
any = ["question_navigation", "answer_submission"]

[group.compact_and_clear_and_continue]
all = ["compact", "clear", "continue_hint"]

[group.window_full_recovery]
all = ["window_full"]
any = ["compact_or_clear", "compact_and_clear_and_continue"]

[group.limit_reached_recovery]
all = ["limit_reached", "compact"]

[group.context_limit]
any = ["limit_reached_recovery", "window_full_recovery"]
`), "claude")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	cases := []struct {
		name   string
		clause string
		screen string
		want   bool
	}{
		{"and both", "permission_grant", "Allow this Permission?", true},
		{"and one half", "permission_grant", "Allow this?", false},
		{"or-and left", "request_user_input", "use tab to navigate questions / add notes", true},
		{"or-and right", "request_user_input", "enter to submit answer, none of the above", true},
		{"or-and no pane action", "request_user_input", "tab to navigate questions", false},
		{"or-and no branch", "request_user_input", "add notes", false},
		{"limit reached with compact", "context_limit", "Context limit reached · run /compact", true},
		{"limit reached alone", "context_limit", "Context limit reached, sorry", false},
		{"window full short form", "context_limit", "Context window is full — /compact or /clear", true},
		{"window full long form", "context_limit",
			"Context window full. Use /compact then /clear to continue.", true},
		{"window full incomplete", "context_limit", "Context window full. Use /compact.", false},
		{"regex clause", "choice_line", "  ❯ 1. Yes, proceed", true},
		{"regex clause negative", "choice_line", "1. Yes, proceed", false},
		{"unknown clause is false", "nope", "anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := set.Match(tc.clause, tc.screen, strings.ToLower(tc.screen))
			if got != tc.want {
				t.Fatalf("Match(%q) = %v, want %v", tc.clause, got, tc.want)
			}
		})
	}

	if set.Regexp("choice_line") == nil {
		t.Fatal("Regexp(choice_line) = nil")
	}
	if set.Regexp("absent") != nil {
		t.Fatal("Regexp of an absent clause must be nil, not a panic")
	}
	if got := set.Phrases("allow"); len(got) != 1 || got[0] != "allow" {
		t.Fatalf("Phrases(allow) = %v", got)
	}
	if _, ok := set.Group("context_limit"); !ok {
		t.Fatal("Group(context_limit) missing")
	}
}

func TestZeroPatternsMatchesNothing(t *testing.T) {
	var zero Patterns
	if zero.Match("anything", "screen", "screen") {
		t.Fatal("the zero Patterns must match nothing")
	}
	if zero.Has("anything") {
		t.Fatal("the zero Patterns must hold no clause")
	}
}
