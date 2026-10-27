package session

import (
	"bytes"
	"encoding/json"
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

func closeRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	closeJSON, closeYes = false, false
	t.Cleanup(func() { closeJSON, closeYes = false, false })
	cmd := &cobra.Command{Use: "close", RunE: runClose, Args: cobra.MaximumNArgs(1), SilenceUsage: true, SilenceErrors: true}
	cmd.Flags().BoolVar(&closeJSON, "json", false, "")
	cmd.Flags().BoolVar(&closeYes, "yes", false, "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func closeDecode(t *testing.T, out string) closeData {
	t.Helper()
	var envelope struct {
		SchemaVersion int       `json:"schema_version"`
		Data          closeData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.SchemaVersion != 1 {
		t.Fatalf("decode %q: %v", out, err)
	}
	return envelope.Data
}

func closeErrorCode(t *testing.T, out string) string {
	t.Helper()
	var envelope struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return envelope.Error.Code
}

func TestCloseEndsTheAgentAndItsSession(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	other := closeLaunchAnother(t, f, "web")
	id := launched.RuntimeSessionID

	out, err := closeRun(t, string(id), "--yes", "--json")
	if err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	data := closeDecode(t, out)
	if data.RuntimeSessionID != string(id) || data.Backend != "tmux" || data.Project != "api" || !data.Closed || !data.Confirmed || data.State != "ended" {
		t.Fatalf("payload = %+v, want a confirmed close of the ended session", data)
	}
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if backend.GroupPresent(panes, launched.Group) {
		t.Fatalf("tmux session %s is still there", launched.Group.ID)
	}
	if !backend.GroupPresent(panes, other.Group) {
		t.Fatalf("closing one session ended another: %s is gone", other.Group.ID)
	}
	if record, _ := f.Load(t).Get(id); record.State != runtimesession.StateEnded {
		t.Fatalf("record = %+v, want ended", record)
	}
	if record, _ := f.Load(t).Get(other.RuntimeSessionID); record.State != runtimesession.StateLive {
		t.Fatalf("other record = %+v, want live", record)
	}

	// Closing it again finds nothing to kill and says so.
	out, err = closeRun(t, string(id), "--yes", "--json")
	if err != nil {
		t.Fatalf("second close: %v\n%s", err, out)
	}
	if again := closeDecode(t, out); again.Closed || again.State != "ended" {
		t.Fatalf("second close = %+v, want nothing closed", again)
	}
}

func TestCloseAsksForConfirmationAndRefusesWhatItCannotClose(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	id := string(launched.RuntimeSessionID)

	if out, err := closeRun(t, "--json"); err == nil {
		t.Fatalf("no id: succeeded: %s", out)
	}
	if out, err := closeRun(t, "not-an-id", "--yes", "--json"); err == nil || closeErrorCode(t, out) != "invalid_arguments" {
		t.Fatalf("malformed id: %v %s", err, out)
	}
	if out, err := closeRun(t, "rs-00000000000000000000000000000000", "--yes", "--json"); err == nil || closeErrorCode(t, out) != "not_found" {
		t.Fatalf("unknown id: %v %s", err, out)
	}
	if out, err := closeRun(t, id, "--json"); err == nil || closeErrorCode(t, out) != "confirmation_required" {
		t.Fatalf("no --yes: %v %s", err, out)
	}
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if !backend.GroupPresent(panes, launched.Group) {
		t.Fatal("a refused close ended the session")
	}
	if record, _ := f.Load(t).Get(launched.RuntimeSessionID); record.State != runtimesession.StateLive {
		t.Fatalf("record = %+v, want live", record)
	}
}

// TestCloseNeverKillsAPaneItDidNotRegister covers the pane ID a restarted
// server hands to somebody else: the registered generation is gone, so the
// pane that now answers to the same ID is left alone.
func TestCloseNeverKillsAPaneItDidNotRegister(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	if err := f.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	successor := closeLaunchAnother(t, f, "web")
	if successor.Pane != launched.Pane {
		t.Skipf("the restarted server did not reuse pane %s (got %s)", launched.Pane.ID, successor.Pane.ID)
	}

	out, err := closeRun(t, string(launched.RuntimeSessionID), "--yes", "--json")
	if err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	if data := closeDecode(t, out); data.Closed {
		t.Fatalf("payload = %+v, want nothing closed", data)
	}
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if !backend.GroupPresent(panes, successor.Group) {
		t.Fatalf("closing a session of the old server killed pane %s of the new one", successor.Pane.ID)
	}
	if record, _ := f.Load(t).Get(successor.RuntimeSessionID); record.State != runtimesession.StateLive {
		t.Fatalf("successor record = %+v, want live", record)
	}
}

// A server that turns a connection away while its socket still listens is
// unreadable, not gone: close says the backend is unavailable and ends
// nothing, so the agent is never left running under an ended session.
func TestCloseThroughAServerThatRefusesEndsNothing(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	f.Refuse(t)
	out, err := closeRun(t, string(launched.RuntimeSessionID), "--yes", "--json")
	if err == nil || closeErrorCode(t, out) != "backend_unavailable" || !strings.Contains(out, "still accepts connections") {
		t.Fatalf("close through a server that refused: %v %s", err, out)
	}
	if record, _ := f.Load(t).Get(launched.RuntimeSessionID); record.State != runtimesession.StateLive {
		t.Fatalf("record = %s %q, want live", record.State, record.StaleReason)
	}
}

// closeLaunchAnother starts one more stand-in agent on the fixture's server.
func closeLaunchAnother(t *testing.T, f *tmuxtest.Fixture, title string) session.LaunchResult {
	t.Helper()
	project := config.Session{Title: title, Directory: t.TempDir(), Profile: "work"}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}}, Sessions: []config.Session{project}}
	launched, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatal(err)
	}
	return launched
}
