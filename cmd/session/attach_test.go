package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// launchAgent starts one stand-in agent on a private tmux server and points
// the command's seams at that server and its registry.
func attachLaunchAgent(t *testing.T) (*tmuxtest.Fixture, session.LaunchResult) {
	t.Helper()
	f := tmuxtest.New(t)
	project := config.Session{Title: "api", Directory: t.TempDir(), Profile: "work"}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}}, Sessions: []config.Session{project}}
	launched, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatal(err)
	}
	previousPath, previousTmux, previousTerminal, previousRun := attachRegistryPath, attachNewTmux, attachIsTerminal, attachRunClient
	attachRegistryPath = f.Registry.Path
	attachNewTmux = func() (backend.TargetLauncher, error) { return f.Backend, nil }
	attachIsTerminal = func() bool { return true }
	t.Cleanup(func() {
		attachRegistryPath, attachNewTmux, attachIsTerminal, attachRunClient = previousPath, previousTmux, previousTerminal, previousRun
		attachJSON = false
	})
	return f, launched
}

func attachRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	attachJSON = false
	cmd := &cobra.Command{Use: "attach", RunE: runAttach, Args: cobra.MaximumNArgs(1), SilenceUsage: true, SilenceErrors: true}
	cmd.Flags().BoolVar(&attachJSON, "json", false, "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func attachAgentPID(t *testing.T, f *tmuxtest.Fixture, pane backend.Ref) int {
	t.Helper()
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := backend.FindPane(panes, pane)
	if !ok || len(p.Foreground) == 0 {
		t.Fatalf("pane %s not running", pane)
	}
	return p.Foreground[0].PID
}

func TestAttachTransfersOwnershipWithoutRestarting(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	id := launched.RuntimeSessionID
	before, _ := f.Load(t).Get(id)
	// A desktop view owns the session before this terminal attaches.
	if err := f.Registry.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.Attach(id, runtimesession.Attachment{ID: "desktop-1", Client: "desktop"}, before.UpdatedAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var argv []string
	attachRunClient = func(c *exec.Cmd) error {
		argv = c.Args
		during, _ := f.Load(t).Get(id)
		if during.Attachment == nil || !strings.HasPrefix(during.Attachment.ID, "amx-attach-") {
			t.Errorf("attachment while attached = %+v, want this terminal", during.Attachment)
		}
		return nil
	}
	if _, err := attachRun(t, string(id)); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !slices.Contains(argv, "attach-session") || !slices.Contains(argv, "-d") || argv[len(argv)-1] != launched.Pane.ID {
		t.Fatalf("client argv = %q, want attach-session -d -t %s", argv, launched.Pane.ID)
	}
	after, _ := f.Load(t).Get(id)
	if after.State != runtimesession.StateLive || after.Attachment != nil {
		t.Fatalf("after detach = %+v, want live and unowned", after)
	}
	if *after.Process != *before.Process || *after.Target != *before.Target {
		t.Fatalf("process/target changed across attach: %+v -> %+v", before, after)
	}
	if pid := attachAgentPID(t, f, launched.Pane); pid != before.Process.PID {
		t.Fatalf("agent pid = %d, want %d (never restarted)", pid, before.Process.PID)
	}
}

// TestAttachRealClientKeepsAgentRunning attaches a real tmux client through a
// pseudo-terminal, detaches it, and attaches again: the same agent process
// serves both attachments.
func TestAttachRealClientKeepsAgentRunning(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses BSD script(1) for a pseudo-terminal")
	}
	f, launched := attachLaunchAgent(t)
	record, _ := f.Load(t).Get(launched.RuntimeSessionID)
	attachRunClient = func(client *exec.Cmd) error {
		// The client attaches and immediately detaches itself again.
		args := append([]string{"-q", "/dev/null", client.Path}, client.Args[1:]...)
		args = append(args, ";", "detach-client")
		script := exec.Command("script", args...)
		script.Env = append(client.Env, "TERM=xterm-256color")
		out, err := script.CombinedOutput()
		if err != nil {
			return errors.New(string(out))
		}
		if !strings.Contains(string(out), "[detached (from session amx-") {
			t.Errorf("client output %q shows no attach and detach of an AgentMux session", out)
		}
		return nil
	}
	for range 2 {
		if _, err := attachRun(t, string(launched.RuntimeSessionID)); err != nil {
			t.Fatalf("attach: %v", err)
		}
		if pid := attachAgentPID(t, f, launched.Pane); pid != record.Process.PID {
			t.Fatalf("agent pid = %d after detach, want %d", pid, record.Process.PID)
		}
	}
	if after, _ := f.Load(t).Get(launched.RuntimeSessionID); after.State != runtimesession.StateLive || after.Attachment != nil {
		t.Fatalf("after detach = %+v, want live and unowned", after)
	}
}

func TestAttachJSONReportsCommandWithoutAttaching(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	attachRunClient = func(*exec.Cmd) error { t.Fatal("--json attached a client"); return nil }
	out, err := attachRun(t, string(launched.RuntimeSessionID), "--json")
	if err != nil {
		t.Fatalf("attach --json: %v\n%s", err, out)
	}
	var envelope struct {
		SchemaVersion int        `json:"schema_version"`
		Data          attachData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	record, _ := f.Load(t).Get(launched.RuntimeSessionID)
	d := envelope.Data
	if envelope.SchemaVersion != 1 || d.RuntimeSessionID != string(launched.RuntimeSessionID) || d.Backend != "tmux" || d.Pane != launched.Pane.ID || d.PID != record.Process.PID || !slices.Contains(d.Command, "attach-session") {
		t.Fatalf("payload = %+v, want the session and its attach command", envelope)
	}
	if record.Attachment != nil {
		t.Fatalf("--json took ownership: %+v", record.Attachment)
	}
}

func TestAttachRefusesWithStructuredErrors(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	attachRunClient = func(*exec.Cmd) error { t.Fatal("attached a refused session"); return nil }

	decode := func(out string) string {
		var envelope struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("decode %q: %v", out, err)
		}
		return envelope.Error.Code
	}
	if out, err := attachRun(t, "not-an-id", "--json"); err == nil || decode(out) != "invalid_arguments" {
		t.Fatalf("malformed id: %v %s", err, out)
	}
	if out, err := attachRun(t, "rs-00000000000000000000000000000000", "--json"); err == nil || decode(out) != "not_found" {
		t.Fatalf("unknown id: %v %s", err, out)
	}

	// The agent's tmux session goes away behind the registry's back: attach
	// must mark it stale instead of attaching to whatever reuses the pane.
	if err := f.Backend.Close(launched.Group); err != nil {
		t.Fatal(err)
	}
	if out, err := attachRun(t, string(launched.RuntimeSessionID), "--json"); err == nil || decode(out) != "runtime_session_stale" {
		t.Fatalf("closed target: %v %s", err, out)
	}
	if record, _ := f.Load(t).Get(launched.RuntimeSessionID); record.State != runtimesession.StateStale {
		t.Fatalf("record = %+v, want stale", record)
	}
	if out, err := attachRun(t, string(launched.RuntimeSessionID), "--json"); err == nil || decode(out) != "runtime_session_ended" {
		t.Fatalf("stale record: %v %s", err, out)
	}
}

func TestAttachNeedsATerminal(t *testing.T) {
	_, launched := attachLaunchAgent(t)
	attachIsTerminal = func() bool { return false }
	attachRunClient = func(*exec.Cmd) error { t.Fatal("attached without a terminal"); return nil }
	if _, err := attachRun(t, string(launched.RuntimeSessionID)); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("err = %v, want a terminal requirement", err)
	}
}
