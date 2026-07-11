package harness

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildInvocation_Golden(t *testing.T) {
	input := InvocationInput{
		ProfileArgs:   []string{"--model", "gpt 5", "--permission-mode", "plan"},
		Environment:   map[string]string{"HARNESS_HOME": "/tmp/profile home", "FEATURE": "enabled"},
		CWD:           "/tmp/project with space",
		SessionID:     "session-123",
		InitialPrompt: "continue with the next task",
	}

	tests := []struct {
		name        string
		harness     string
		kind        InvocationKind
		wantArgs    []string
		wantHarness string
		wantExec    string
	}{
		{
			name:        "claude fresh",
			harness:     Claude,
			kind:        InvocationFresh,
			wantHarness: Claude,
			wantExec:    "claude",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "continue with the next task"},
		},
		{
			name:        "claude resume",
			harness:     Claude,
			kind:        InvocationResume,
			wantHarness: Claude,
			wantExec:    "claude",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "--resume", "session-123", "continue with the next task"},
		},
		{
			name:        "claude fork",
			harness:     Claude,
			kind:        InvocationFork,
			wantHarness: Claude,
			wantExec:    "claude",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "--resume", "session-123", "--fork-session", "continue with the next task"},
		},
		{
			name:        "codex fresh",
			harness:     Codex,
			kind:        InvocationFresh,
			wantHarness: Codex,
			wantExec:    "codex",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "continue with the next task"},
		},
		{
			name:        "codex resume keeps globals before subcommand",
			harness:     Codex,
			kind:        InvocationResume,
			wantHarness: Codex,
			wantExec:    "codex",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "resume", "session-123", "continue with the next task"},
		},
		{
			name:        "codex fork keeps globals before subcommand",
			harness:     Codex,
			kind:        InvocationFork,
			wantHarness: Codex,
			wantExec:    "codex",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "fork", "session-123", "continue with the next task"},
		},
		{
			name:        "copilot fresh interactive",
			harness:     Copilot,
			kind:        InvocationFresh,
			wantHarness: Copilot,
			wantExec:    "copilot",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "continue with the next task"},
		},
		{
			name:        "copilot resume",
			harness:     Copilot,
			kind:        InvocationResume,
			wantHarness: Copilot,
			wantExec:    "copilot",
			wantArgs:    []string{"--model", "gpt 5", "--permission-mode", "plan", "--resume=session-123", "continue with the next task"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildInvocation(tt.harness, tt.kind, input)
			if err != nil {
				t.Fatalf("BuildInvocation() error = %v", err)
			}
			if got.Harness != tt.wantHarness || got.Executable != tt.wantExec {
				t.Errorf("identity = (%q, %q), want (%q, %q)", got.Harness, got.Executable, tt.wantHarness, tt.wantExec)
			}
			if !reflect.DeepEqual(got.Args, tt.wantArgs) {
				t.Errorf("Args = %#v, want %#v", got.Args, tt.wantArgs)
			}
			if got.CWD != input.CWD || got.InitialPrompt != input.InitialPrompt {
				t.Errorf("context = (cwd %q, prompt %q), want (cwd %q, prompt %q)", got.CWD, got.InitialPrompt, input.CWD, input.InitialPrompt)
			}
			if !reflect.DeepEqual(got.Environment, input.Environment) {
				t.Errorf("Environment = %#v, want %#v", got.Environment, input.Environment)
			}
		})
	}
}

func TestBuildInvocation_ClonesInputCollections(t *testing.T) {
	input := InvocationInput{
		ProfileArgs: []string{"--model", "initial"},
		Environment: map[string]string{"CODEX_HOME": "/tmp/initial"},
	}
	got, err := BuildInvocation(Codex, InvocationFresh, input)
	if err != nil {
		t.Fatalf("BuildInvocation() error = %v", err)
	}
	input.ProfileArgs[1] = "mutated"
	input.Environment["CODEX_HOME"] = "/tmp/mutated"
	if got.Args[1] != "initial" {
		t.Errorf("Args shares input backing array: %v", got.Args)
	}
	if got.Environment["CODEX_HOME"] != "/tmp/initial" {
		t.Errorf("Environment shares input map: %v", got.Environment)
	}
}

func TestBuildInvocation_CopilotForkIsTypedUnsupported(t *testing.T) {
	_, err := BuildInvocation(Copilot, InvocationFork, InvocationInput{SessionID: "session-123"})
	if !errors.Is(err, ErrUnsupportedInvocation) {
		t.Fatalf("error = %v, want errors.Is(ErrUnsupportedInvocation)", err)
	}
	var unsupported *UnsupportedInvocationError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %T, want *UnsupportedInvocationError", err)
	}
	if unsupported.Reason != copilotForkUnavailableReason {
		t.Errorf("reason = %q, want %q", unsupported.Reason, copilotForkUnavailableReason)
	}
	capability := Lookup(Copilot).InvocationCapabilities().For(InvocationFork)
	if capability.Supported || capability.Reason != copilotForkUnavailableReason {
		t.Errorf("fork capability = %#v, want unsupported with reason", capability)
	}
}

func TestBuildInvocation_RequiresSessionIDForContinuity(t *testing.T) {
	for _, name := range []string{Claude, Copilot, Codex} {
		for _, kind := range []InvocationKind{InvocationResume, InvocationFork} {
			if name == Copilot && kind == InvocationFork {
				continue
			}
			_, err := BuildInvocation(name, kind, InvocationInput{})
			if err == nil {
				t.Errorf("BuildInvocation(%q, %q) error = nil, want session ID error", name, kind)
			}
		}
	}
}
