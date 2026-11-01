package recap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeInvocationUsesStdinAndIsolatesToolsHooksAndSessionIdentity(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	t.Setenv("RECAP_TEST_CAPTURE", capture)
	t.Setenv("AMX_RUNTIME_SESSION_ID", "the-working-agent")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-auth-token")
	t.Setenv("CLAUDE_CONFIG_DIR", "/wrong-profile")
	t.Setenv("TMUX", "working-tmux")
	executable := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = --help ]; then
  echo '--safe-mode --tools --strict-mcp-config --no-session-persistence --json-schema --settings --setting-sources'
  exit 0
fi
printf '%s\n' "$@" > "$RECAP_TEST_CAPTURE.args"
printf '%s\n' "$CLAUDE_CONFIG_DIR" "${AMX_RUNTIME_SESSION_ID-unset}" "${CLAUDECODE-unset}" "${TMUX-unset}" "$PWD" "$CLAUDE_CODE_OAUTH_TOKEN" > "$RECAP_TEST_CAPTURE.env"
cat > "$RECAP_TEST_CAPTURE.input"
printf '%s' '{"is_error":false,"structured_output":{"goal":{"text":"Goal","messages":[0]},"latest":{"text":"Agent reports tests passed","messages":[1]},"outstanding":{"text":"","messages":[]}}}'
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	in := Input{Messages: []Message{{Index: 0, Role: "user", Text: "private goal $(touch no)"}, {Index: 1, Role: "assistant", Text: "done"}}}
	result, err := (Claude{Executable: executable, Home: "/selected/profile", Model: "small-model"}).Generate(t.Context(), in)
	if err != nil || result.Goal.Text != "Goal" {
		t.Fatalf("result %+v, %v", result, err)
	}
	args, _ := os.ReadFile(capture + ".args")
	for _, flag := range []string{"--safe-mode\n", "--tools\n\n", "--strict-mcp-config\n", "--no-session-persistence\n", `{"disableAllHooks":true}`, "--setting-sources\nuser\n", "--model\nsmall-model\n"} {
		if !strings.Contains(string(args), flag) {
			t.Fatalf("missing control %q in %s", flag, args)
		}
	}
	if strings.Contains(string(args), "private goal") || strings.Contains(string(args), "--resume") {
		t.Fatal("source text or resume identity leaked into argv")
	}
	env, _ := os.ReadFile(capture + ".env")
	if !strings.HasPrefix(string(env), "/selected/profile\nunset\nunset\nunset\n") {
		t.Fatalf("session environment not scrubbed: %s", env)
	}
	if !strings.HasSuffix(string(env), "fixture-auth-token\n") {
		t.Fatal("removed authentication along with session identity")
	}
	workingDir := strings.TrimSpace(strings.Split(string(env), "\n")[4])
	if _, err := os.Stat(workingDir); !os.IsNotExist(err) {
		t.Fatalf("temporary working directory was not removed: %v", err)
	}
	data, _ := os.ReadFile(capture + ".input")
	var actual Input
	if err := json.Unmarshal(data, &actual); err != nil || actual.Messages[0].Text != in.Messages[0].Text {
		t.Fatalf("stdin input %s, %v", data, err)
	}
}

func TestClaudeRefusesCLIWithoutIsolationControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho old-cli\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (Claude{Executable: path}).Generate(t.Context(), Input{})
	if err == nil || !strings.Contains(err.Error(), "update Claude Code") {
		t.Fatalf("err = %v", err)
	}
}

// Opt-in smoke test uses only synthetic messages and the operator's existing
// Claude authentication; ordinary tests never make model requests.
func TestClaudeLiveSynthetic(t *testing.T) {
	if os.Getenv("AMX_RECAP_LIVE_TEST") != "1" {
		t.Skip("set AMX_RECAP_LIVE_TEST=1 for a synthetic provider request")
	}
	in := Input{Messages: []Message{{Index: 0, Role: "user", Text: "Add a red button. Verify keyboard navigation later."},
		{Index: 1, Role: "assistant", Text: "The red button is implemented. I ran the unit tests and they passed. Keyboard navigation has not been checked."}}}
	result, err := (Claude{Home: os.Getenv("AMX_RECAP_TEST_PROFILE_HOME"), Model: "haiku"}).Generate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(result, in); err != nil {
		t.Fatal(err)
	}
	if result.Goal.Text == "" || result.Latest.Text == "" || result.Outstanding.Text == "" {
		t.Fatalf("incomplete synthetic recap %+v", result)
	}
}
