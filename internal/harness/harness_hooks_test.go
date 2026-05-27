package harness

import (
	"slices"
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
