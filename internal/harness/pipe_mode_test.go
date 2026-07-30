package harness

import (
	"strings"
	"testing"
)

func TestInvocationIsPipeMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		harness string
		args    string
		want    bool
	}{
		{name: "claude interactive", harness: Claude, args: "", want: false},
		{name: "claude short flag", harness: Claude, args: "-p hello", want: true},
		{name: "claude long flag", harness: Claude, args: "--print hello", want: true},
		{name: "claude short flag value", harness: Claude, args: "-p=hello", want: true},
		{name: "claude long flag value", harness: Claude, args: "--print=hello", want: true},
		{name: "claude flag after positional", harness: Claude, args: "prompt -p", want: true},
		{name: "claude option terminator", harness: Claude, args: "-- -p", want: false},
		{name: "copilot short flag", harness: Copilot, args: "-p hello", want: true},
		{name: "copilot long flag value", harness: Copilot, args: "--prompt=hello", want: true},
		{name: "copilot option terminator", harness: Copilot, args: "-- --prompt", want: false},
		{name: "codex interactive", harness: Codex, args: "", want: false},
		{name: "codex exec", harness: Codex, args: "exec --json", want: true},
		{name: "codex e", harness: Codex, args: "e --json", want: true},
		{name: "codex review", harness: Codex, args: "review --uncommitted", want: true},
		{name: "codex leading boolean flag", harness: Codex, args: "--full-auto exec --json", want: true},
		{name: "codex leading valued flag", harness: Codex, args: "--profile work review", want: true},
		{name: "codex leading inline flag", harness: Codex, args: "--model=gpt-5 e", want: true},
		{name: "codex profile value collision", harness: Codex, args: "--profile review", want: false},
		{name: "codex short flag value collision", harness: Codex, args: "-m exec", want: false},
		{name: "codex profile collision before exec", harness: Codex, args: "--profile review exec", want: true},
		{name: "codex variadic short image before exec", harness: Codex, args: "-i a.png exec", want: false},
		{name: "codex variadic long image before review", harness: Codex, args: "--image a.png review", want: false},
		{name: "codex variadic multiple images before exec", harness: Codex, args: "-i a.png b.png exec", want: false},
		{name: "codex flag ends variadic image values", harness: Codex, args: "-i a.png --full-auto exec", want: true},
		{name: "codex image after exec", harness: Codex, args: "exec -i a.png", want: true},
		{name: "codex later positional collision", harness: Codex, args: "resume e", want: false},
		{name: "codex option terminator", harness: Codex, args: "-- review", want: false},
		// Lookup defaults unknown names to Claude, so Claude's tokens apply.
		{name: "unknown harness falls back to claude tokens", harness: "unknown", args: "exec", want: false},
		{name: "unknown harness claude token", harness: "unknown", args: "-p hi", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := InvocationIsPipeMode(tc.harness, strings.Fields(tc.args)); got != tc.want {
				t.Fatalf("InvocationIsPipeMode(%q, %q) = %t, want %t", tc.harness, tc.args, got, tc.want)
			}
		})
	}
}
