package harness

import (
	"reflect"
	"testing"
)

func TestBuildInvocation_InjectsManagedNativeLoggingBeforeContinuityControls(t *testing.T) {
	tests := []struct {
		name      string
		harness   string
		wantArgs  []string
		wantPath  string
		wantLevel string
		wantEnv   map[string]string
	}{
		{
			name: "claude", harness: Claude,
			wantArgs: []string{"--model", "sonnet", "--debug-file", "/state/launch/claude-debug.log", "--resume", "session-1"},
			wantPath: "/state/launch/claude-debug.log", wantLevel: "debug",
		},
		{
			name: "codex", harness: Codex,
			wantArgs: []string{"--model", "gpt-5", "-c", "log_dir=/state/launch", "resume", "session-1"},
			wantPath: "/state/launch", wantLevel: "debug", wantEnv: map[string]string{"RUST_LOG": "debug"},
		},
		{
			name: "copilot", harness: Copilot,
			wantArgs: []string{"--model", "gpt-5", "--log-dir", "/state/launch", "--log-level", "debug", "--resume=session-1"},
			wantPath: "/state/launch", wantLevel: "debug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation, err := BuildInvocation(tt.harness, InvocationResume, InvocationInput{
				ProfileArgs:   []string{"--model", map[string]string{Claude: "sonnet", Codex: "gpt-5", Copilot: "gpt-5"}[tt.harness]},
				SessionID:     "session-1",
				NativeLogging: NativeLoggingRequest{Enabled: true, LaunchID: "launch-1", Directory: "/state/launch"},
			})
			if err != nil {
				t.Fatalf("BuildInvocation: %v", err)
			}
			if !reflect.DeepEqual(invocation.Args, tt.wantArgs) {
				t.Fatalf("Args = %#v, want %#v", invocation.Args, tt.wantArgs)
			}
			if invocation.LaunchID != "launch-1" || invocation.NativeLogging.Coverage != NativeLogManaged || !invocation.NativeLogging.ManagedDirectory {
				t.Fatalf("native metadata = %+v launch=%q", invocation.NativeLogging, invocation.LaunchID)
			}
			if invocation.NativeLogging.Path != tt.wantPath || invocation.NativeLogging.Level != tt.wantLevel {
				t.Fatalf("native path/level = %q/%q", invocation.NativeLogging.Path, invocation.NativeLogging.Level)
			}
			for key, value := range tt.wantEnv {
				if invocation.Environment[key] != value {
					t.Errorf("Environment[%s] = %q, want %q", key, invocation.Environment[key], value)
				}
			}
		})
	}
}

func TestBuildInvocation_PreservesExplicitNativeLoggingOverrides(t *testing.T) {
	tests := []struct {
		name         string
		harness      string
		args         []string
		env          map[string]string
		wantPath     string
		wantLevel    string
		wantCoverage NativeLogCoverage
		wantOverride string
	}{
		{
			name: "claude external file", harness: Claude,
			args: []string{"--debug-file=/tmp/user-claude.log"}, wantPath: "/tmp/user-claude.log", wantLevel: "debug",
			wantCoverage: NativeLogExternal, wantOverride: "--debug-file",
		},
		{
			name: "codex external env and config", harness: Codex,
			args: []string{"-c", `log_dir="/tmp/user-codex"`}, env: map[string]string{"RUST_LOG": "trace"},
			wantPath: "/tmp/user-codex", wantLevel: "trace", wantCoverage: NativeLogExternal, wantOverride: "RUST_LOG,log_dir",
		},
		{
			name: "copilot non-debug override is degraded", harness: Copilot,
			args:     []string{"--log-dir", "/tmp/user-copilot", "--log-level=info"},
			wantPath: "/tmp/user-copilot", wantLevel: "info", wantCoverage: NativeLogDegraded, wantOverride: "--log-dir,--log-level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalArgs := append([]string(nil), tt.args...)
			invocation, err := BuildInvocation(tt.harness, InvocationFresh, InvocationInput{
				ProfileArgs: tt.args, Environment: tt.env,
				NativeLogging: NativeLoggingRequest{Enabled: true, LaunchID: "launch-2", Directory: "/state/launch-2"},
			})
			if err != nil {
				t.Fatalf("BuildInvocation: %v", err)
			}
			if !reflect.DeepEqual(invocation.Args, originalArgs) {
				t.Fatalf("explicit args changed: got %#v want %#v", invocation.Args, originalArgs)
			}
			got := invocation.NativeLogging
			if got.Path != tt.wantPath || got.Level != tt.wantLevel || got.Coverage != tt.wantCoverage || got.Override != tt.wantOverride || got.ManagedDirectory {
				t.Fatalf("native metadata = %+v", got)
			}
		})
	}
}

func TestBuildInvocation_DisabledNativeLoggingAddsNoControls(t *testing.T) {
	invocation, err := BuildInvocation(Codex, InvocationFresh, InvocationInput{ProfileArgs: []string{"--model", "gpt-5"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(invocation.Args, []string{"--model", "gpt-5"}) || len(invocation.Environment) != 0 {
		t.Fatalf("disabled invocation changed: args=%v env=%v", invocation.Args, invocation.Environment)
	}
	if invocation.NativeLogging.Coverage != NativeLogDisabled {
		t.Fatalf("coverage = %q", invocation.NativeLogging.Coverage)
	}
}
