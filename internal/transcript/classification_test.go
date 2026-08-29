package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexSessionClassification(t *testing.T) {
	tests := []struct {
		name       string
		originator string
		source     string
		want       SessionClass
	}{
		{name: "interactive cli", originator: "codex-tui", source: `"cli"`, want: SessionClassMain},
		{name: "desktop interactive", originator: "Codex Desktop", source: `"vscode"`, want: SessionClassMain},
		{name: "native subagent wins over main origin", originator: "codex-tui", source: `{"subagent":{"thread_spawn":{"depth":1}}}`, want: SessionClassAutomated},
		{name: "asl wins over interactive source", originator: "agent-session-loop", source: `"vscode"`, want: SessionClassAutomated},
		{name: "programmatic exec", originator: "codex_exec", source: `"exec"`, want: SessionClassAutomated},
		{name: "unrecognized", originator: "future-client", source: `"future"`, want: SessionClassUnknown},
	}

	reader := NewCodexReader()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout-test.jsonl")
			body := fmt.Sprintf(`{"type":"session_meta","payload":{"originator":%q,"source":%s}}`+"\n", tc.originator, tc.source)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := reader.ClassifySession(path)
			if err != nil {
				t.Fatalf("ClassifySession: %v", err)
			}
			if got != tc.want {
				t.Fatalf("class = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeSessionClassification(t *testing.T) {
	tests := []struct {
		name   string
		record string
		want   SessionClass
	}{
		{name: "interactive cli", record: `{"type":"user","entrypoint":"cli"}`, want: SessionClassMain},
		{name: "typed prompt", record: `{"type":"user","promptSource":"typed"}`, want: SessionClassMain},
		{name: "sidechain wins over cli", record: `{"type":"user","entrypoint":"cli","isSidechain":true}`, want: SessionClassAutomated},
		{name: "agent record", record: `{"type":"user","entrypoint":"cli","agentId":"agent-1"}`, want: SessionClassAutomated},
		{name: "sdk cli", record: `{"type":"user","entrypoint":"sdk-cli"}`, want: SessionClassAutomated},
		{name: "sdk prompt wins over typed", record: `{"type":"user","promptSource":"sdk","entrypoint":"cli"}`, want: SessionClassAutomated},
		{name: "unrecognized", record: `{"type":"user","entrypoint":"future-client"}`, want: SessionClassUnknown},
	}

	reader := NewClaudeReader()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(tc.record+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := reader.ClassifySession(path)
			if err != nil {
				t.Fatalf("ClassifySession: %v", err)
			}
			if got != tc.want {
				t.Fatalf("class = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCopilotTopLevelSessionClassification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"subagent.started","data":{"agentId":"nested"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := NewCopilotReader().ClassifySession(path)
	if err != nil {
		t.Fatalf("ClassifySession: %v", err)
	}
	if got != SessionClassMain {
		t.Fatalf("class = %q, want %q", got, SessionClassMain)
	}
}
