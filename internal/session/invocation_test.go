package session

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestBuildInvocation_ResolvesProfileAndProjectContext(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")

	sess := config.Session{
		Directory: "/tmp/project with space",
		Args:      []string{"--project-flag", "project value"},
		Env:       map[string]string{"APP_MODE": "review"},
	}
	acc := config.Account{
		Harness: "codex",
		HomeDir: "/tmp/codex home",
		Args:    []string{"--profile-flag"},
	}

	got, err := BuildInvocation(sess, acc, config.Defaults{Args: []string{"--default-flag"}}, harness.InvocationResume, "session-123", "continue safely")
	if err != nil {
		t.Fatalf("BuildInvocation() error = %v", err)
	}
	if want := []string{"--project-flag", "project value", "resume", "session-123", "continue safely"}; !reflect.DeepEqual(got.Args, want) {
		t.Errorf("Args = %#v, want %#v", got.Args, want)
	}
	if got.CWD != "/tmp/project with space" {
		t.Errorf("CWD = %q, want project directory", got.CWD)
	}
	if want := map[string]string{"CODEX_HOME": "/tmp/codex home", "APP_MODE": "review"}; !reflect.DeepEqual(got.Environment, want) {
		t.Errorf("Environment = %#v, want %#v", got.Environment, want)
	}
}

func TestRenderInvocation_QuotesAfterHarnessOrdersArgs(t *testing.T) {
	t.Parallel()

	invocation, err := BuildInvocation(
		config.Session{Directory: "/tmp/project", Args: []string{"--model", "gpt 5"}},
		config.Account{Harness: "codex", HomeDir: "/tmp/codex home"},
		config.Defaults{},
		harness.InvocationFork,
		"session 123",
		"continue safely",
	)
	if err != nil {
		t.Fatalf("BuildInvocation() error = %v", err)
	}
	if got, want := RenderInvocation(invocation), "exec env 'CODEX_HOME=/tmp/codex home' codex --model 'gpt 5' fork 'session 123' 'continue safely'"; got != want {
		t.Errorf("RenderInvocation() = %q, want %q", got, want)
	}
}

func TestRenderInvocationPinsCodexNativeLogLevel(t *testing.T) {
	t.Parallel()

	invocation := harness.Invocation{
		Harness:    harness.Codex,
		Executable: "codex",
		Environment: map[string]string{
			"CODEX_HOME": "/tmp/codex home",
			"RUST_LOG":   "codex_core=trace,info",
		},
		NativeLogging: harness.NativeLogging{Coverage: harness.NativeLogExternal},
	}
	if got, want := RenderInvocation(invocation), "exec env 'CODEX_HOME=/tmp/codex home' RUST_LOG=codex_core=trace,info codex"; got != want {
		t.Errorf("RenderInvocation() = %q, want %q", got, want)
	}
}
