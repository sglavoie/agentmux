package session_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestLaunchAddsStableNativeMetadataAndCausalJournalRecord(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	enabled := true
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", Diagnostics: config.DiagnosticsConfig{
			JournalEnabled: &enabled, JournalKeepDays: 14, JournalMaxMiB: 16,
			NativeLogging: &enabled, NativeKeepDays: 14, NativeMaxMiB: 32, NativeSessionMaxMiB: 8,
		}},
		HarnessEmojis: map[string]string{harness.Codex: "📟"},
	}
	acc := config.Account{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex"}
	sess := config.Session{Title: "api", Profile: "work", Directory: "/tmp/api", Env: map[string]string{"ASL_RUN_ID": "run-7"}}

	capture := &capturingLaunchClient{windows: nil}
	if err := session.LaunchContinuity(capture, sess, acc, cfg, harness.InvocationResume, "session-9", ""); err != nil {
		t.Fatal(err)
	}
	opts := capture.opts
	launchID := opts.UserVars["amx_launch_id"]
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(launchID) {
		t.Fatalf("launch ID = %q", launchID)
	}
	if opts.UserVars["amx_native_coverage"] != "managed" || opts.UserVars["amx_native_log_level"] != "debug" || opts.UserVars["amx_harness_session"] != "session-9" || opts.UserVars["amx_asl_run_id"] != "run-7" {
		t.Fatalf("native user vars = %+v", opts.UserVars)
	}
	wantDir := filepath.Join(stateHome, "agent-mux", "native", launchID)
	if opts.UserVars["amx_native_log_path"] != wantDir {
		t.Fatalf("native path = %q, want %q", opts.UserVars["amx_native_log_path"], wantDir)
	}
	info, err := os.Stat(wantDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("native directory mode = %v", info.Mode().Perm())
	}
	command := strings.Join(opts.Cmdline, " ")
	if !strings.Contains(command, "umask 077 && exec env CODEX_HOME=/tmp/codex RUST_LOG=debug codex -c log_dir="+wantDir+" resume session-9") {
		t.Fatalf("command = %q", command)
	}
	if opts.Env["RUST_LOG"] != "debug" {
		t.Fatalf("RUST_LOG = %q", opts.Env["RUST_LOG"])
	}

	journalPath := filepath.Join(stateHome, "agent-mux", "journal", "events.jsonl")
	file, err := os.Open(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	foundRecord := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var envelope diagnostics.TraceEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.LaunchID == launchID {
			foundRecord = envelope.Stage == "session.launch" && envelope.SessionID == "session-9" && envelope.RunID == "run-7" && envelope.Evidence["native_coverage"] == "managed"
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundRecord {
		t.Fatalf("journal has no correlated launch record for %s", launchID)
	}
}

type capturingLaunchClient struct {
	windows []kitty.OSWindow
	opts    kitty.LaunchOpts
}

func (c *capturingLaunchClient) ListWindows() ([]kitty.OSWindow, error)                 { return c.windows, nil }
func (c *capturingLaunchClient) LaunchTab(opts kitty.LaunchOpts) error                  { c.opts = opts; return nil }
func (*capturingLaunchClient) LaunchOverlay(kitty.LaunchOpts) error                     { return nil }
func (*capturingLaunchClient) CloseTab(string) error                                    { return nil }
func (*capturingLaunchClient) MoveTab(string, int) error                                { return nil }
func (*capturingLaunchClient) SetTabColor(string, string, string, string, string) error { return nil }
func (*capturingLaunchClient) SetTabTitle(string, string) error                         { return nil }
func (*capturingLaunchClient) SetUserVars(string, map[string]string) error              { return nil }
func (*capturingLaunchClient) FocusTab(string) error                                    { return nil }
func (*capturingLaunchClient) FocusWindow(string) error                                 { return nil }
func (*capturingLaunchClient) GetText(string, string) (string, error)                   { return "", nil }
func (*capturingLaunchClient) LaunchOverlayPager(string) error                          { return nil }
