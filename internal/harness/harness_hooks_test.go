package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestClaudeHooksIncludeCompletionSignals(t *testing.T) {
	hooks := buildClaudeHooks(nil, "amx")
	for _, event := range []string{"Stop", "SessionEnd", "Notification", "TeammateIdle"} {
		if !slices.Contains(claudeHookEvents, event) {
			t.Fatalf("claudeHookEvents missing %q", event)
		}
		if _, ok := hooks[event]; !ok {
			t.Fatalf("buildClaudeHooks missing %q", event)
		}
	}
}

// TestClaudeHooksPostCompactMatcherSplit pins the manual/auto matcher split:
// Claude fires PostCompact for both manual /compact and automatic mid-turn
// compaction, and only hook-config matchers distinguish them, so the merge
// output must carry two matcher groups dispatching distinct pseudo-events.
func TestClaudeHooksPostCompactMatcherSplit(t *testing.T) {
	hooks := buildClaudeHooks(nil, "amx")
	groups, ok := hooks["PostCompact"]
	if !ok {
		t.Fatal("buildClaudeHooks missing PostCompact")
	}
	if len(groups) != 2 {
		t.Fatalf("PostCompact groups = %d, want 2 matcher groups", len(groups))
	}
	want := map[string]string{
		"manual": "amx hook-indicator PostCompactManual --harness=claude",
		"auto":   "amx hook-indicator PostCompactAuto --harness=claude",
	}
	for _, g := range groups {
		wantCmd, ok := want[g.Matcher]
		if !ok {
			t.Fatalf("PostCompact group has unexpected matcher %q", g.Matcher)
		}
		delete(want, g.Matcher)
		if len(g.Hooks) != 1 || g.Hooks[0].Command != wantCmd {
			t.Fatalf("PostCompact matcher %q hooks = %+v, want single command %q", g.Matcher, g.Hooks, wantCmd)
		}
		if g.Hooks[0].Async == nil || !*g.Hooks[0].Async {
			t.Fatalf("PostCompact matcher %q hook is not async", g.Matcher)
		}
	}
	if len(want) > 0 {
		t.Fatalf("PostCompact missing matcher groups: %v", want)
	}

	// No other Claude event picks up a matcher.
	for event, groups := range hooks {
		if event == "PostCompact" {
			continue
		}
		for _, g := range groups {
			if g.Matcher != "" {
				t.Fatalf("event %q unexpectedly has matcher %q", event, g.Matcher)
			}
		}
	}
}

// TestClaudeHooksDisabledPostCompactDropsBothMatcherGroups verifies that
// disabled_events keys on the real event name, so disabling PostCompact
// removes both the manual and auto matcher groups.
func TestClaudeHooksDisabledPostCompactDropsBothMatcherGroups(t *testing.T) {
	hooks := buildClaudeHooks([]string{"PostCompact"}, "amx")
	if groups, ok := hooks["PostCompact"]; ok {
		t.Fatalf("PostCompact should be absent when disabled, got %+v", groups)
	}

	contract := claudeHookContract{}
	out, err := contract.MergeData(nil, []string{"PostCompact"}, "amx")
	if err != nil {
		t.Fatalf("MergeData err: %v", err)
	}
	if strings.Contains(string(out), "PostCompact") {
		t.Fatalf("merged settings should not mention PostCompact when disabled:\n%s", out)
	}
}

// TestClaudeMergeDataPostCompactIdempotent verifies the strip-then-append
// merge keeps exactly the two PostCompact matcher groups across re-merges and
// that ParseInstalledEvents still reports the real event name.
func TestClaudeMergeDataPostCompactIdempotent(t *testing.T) {
	contract := claudeHookContract{}
	first, err := contract.MergeData(nil, nil, "amx")
	if err != nil {
		t.Fatalf("MergeData (first) err: %v", err)
	}
	for _, cmd := range []string{"amx hook-indicator PostCompactManual --harness=claude", "amx hook-indicator PostCompactAuto --harness=claude"} {
		if !strings.Contains(string(first), cmd) {
			t.Fatalf("merged settings missing %q:\n%s", cmd, first)
		}
	}

	second, err := contract.MergeData(first, nil, "amx")
	if err != nil {
		t.Fatalf("MergeData (re-merge) err: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("re-merge is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	// ParseInstalledEvents keys on the settings event name, so the matcher
	// split must not change the reported event list.
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, second, 0o644); err != nil {
		t.Fatalf("writing settings.json: %v", err)
	}
	events, err := contract.ParseInstalledEvents(path)
	if err != nil {
		t.Fatalf("ParseInstalledEvents err: %v", err)
	}
	if !slices.Contains(events, "PostCompact") {
		t.Fatalf("ParseInstalledEvents = %v, want PostCompact present", events)
	}
	for _, e := range events {
		if e == "PostCompactManual" || e == "PostCompactAuto" {
			t.Fatalf("ParseInstalledEvents leaked pseudo-event %q", e)
		}
	}
}

func TestCopilotHooksIncludeCompletionSignals(t *testing.T) {
	hooks := buildCopilotHooks(nil, "amx")
	for _, event := range []string{"agentStop", "sessionEnd", "notification"} {
		if !slices.Contains(copilotHookEvents, event) {
			t.Fatalf("copilotHookEvents missing %q", event)
		}
		if _, ok := hooks.Hooks[event]; !ok {
			t.Fatalf("buildCopilotHooks missing %q", event)
		}
	}
}

func TestKnownEvents_Codex(t *testing.T) {
	got := KnownEvents("codex")
	if !slices.Equal(got, codexHookEvents) {
		t.Fatalf("KnownEvents(codex) = %v, want %v", got, codexHookEvents)
	}
}

func TestCodexHooksIncludeCompletionSignals(t *testing.T) {
	hooks := buildCodexHooks(nil, "amx")
	for _, event := range []string{"Stop", "PostCompact", "SubagentStop"} {
		if !slices.Contains(codexHookEvents, event) {
			t.Fatalf("codexHookEvents missing %q", event)
		}
		if _, ok := hooks[event]; !ok {
			t.Fatalf("buildCodexHooks missing %q", event)
		}
	}
}

func TestCodexHooksDoNotInstallUnsupportedPromptHooks(t *testing.T) {
	hooks := buildCodexHooks(nil, "amx")
	for _, event := range []string{"Notification", "Elicitation"} {
		if slices.Contains(codexHookEvents, event) {
			t.Fatalf("codexHookEvents includes unsupported %q", event)
		}
		if _, ok := hooks[event]; ok {
			t.Fatalf("buildCodexHooks installed unsupported %q hook", event)
		}
	}
}
