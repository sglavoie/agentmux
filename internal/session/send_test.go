package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// failingTypist is a real backend whose writes fail, or whose record of a
// write fails once the write has landed.
type failingTypist struct {
	SendTerminal
	failSend    bool
	sendErr     error
	failRecord  bool
	wroteRecord int
}

func (f *failingTypist) Send(ref backend.Ref, generation string, input backend.Input) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.failSend {
		return errors.New("tmux went away")
	}
	return f.SendTerminal.Send(ref, generation, input)
}

func (f *failingTypist) SetMetadata(ref backend.Ref, vars map[string]string) error {
	f.wroteRecord++
	if f.failRecord && f.wroteRecord > 1 {
		return errors.New("metadata lock lost")
	}
	return f.SendTerminal.SetMetadata(ref, vars)
}

func sendFixture(t *testing.T) (*tmuxtest.Fixture, SendRequest) {
	t.Helper()
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	launched, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.Load(t).Get(launched.RuntimeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return f, SendRequest{
		Record: record, Input: backend.Input{Text: "sent-marker", Submit: true}, OperationID: "op-1",
		LockPath: filepath.Join(t.TempDir(), "send"), Now: time.Now(),
		Classify: func(string, string) (screenstate.State, bool) { return screenstate.Terminal, true },
	}
}

func sendRecorded(t *testing.T, f *tmuxtest.Fixture, record runtimesession.Record) string {
	t.Helper()
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	pane, _ := backend.FindPane(panes, backend.PaneID(record.Target.Ref))
	return pane.Vars[SendOperationVar]
}

func TestSendWritesOnlyToThePaneThatStillCarriesTheSession(t *testing.T) {
	f, req := sendFixture(t)
	ref := backend.PaneID(req.Record.Target.Ref)
	// The pane was given to something else since the record was read.
	if err := f.Backend.SetMetadata(ref, map[string]string{RuntimeSessionVar: "rs-ffffffffffffffffffffffffffffffff"}); err != nil {
		t.Fatal(err)
	}
	outcome, err := Send(f.Backend, req)
	var refusal *SendRefusal
	if !errors.As(err, &refusal) || refusal.Code != SendRefusedGone || outcome.Written {
		t.Fatalf("Send = %+v, %v; want a not_found refusal", outcome, err)
	}
	if got := sendRecorded(t, f, req.Record); got != "" {
		t.Fatalf("a refused send recorded %q on a pane that is not its own", got)
	}
	if text, _ := f.Backend.Capture(ref, backend.ExtentScreen); strings.Contains(text, "sent-marker") {
		t.Fatalf("a refused send was typed:\n%s", text)
	}
}

func TestSendThatFailsLeavesItsOperationUnfinished(t *testing.T) {
	f, req := sendFixture(t)
	outcome, err := Send(&failingTypist{SendTerminal: f.Backend, failSend: true}, req)
	if err == nil || outcome.Written {
		t.Fatalf("Send = %+v, %v; want a failure that wrote nothing", outcome, err)
	}
	if got := sendRecorded(t, f, req.Record); got != "op-1:pending" {
		t.Fatalf("recorded %q, want op-1:pending", got)
	}
	// Nobody can say what the failed write did, so the same operation is not
	// tried again; a new one is.
	var refusal *SendRefusal
	if _, err := Send(f.Backend, req); !errors.As(err, &refusal) || refusal.Code != SendRefusedOutcomeUnknown {
		t.Fatalf("repeat = %v, want operation_outcome_unknown", err)
	}
	req.OperationID = "op-2"
	if outcome, err := Send(f.Backend, req); err != nil || !outcome.Written || outcome.Screen != SendScreenIdle {
		t.Fatalf("new operation = %+v, %v", outcome, err)
	}
	req.OperationID = "op-1"
	if _, err := Send(f.Backend, req); !errors.As(err, &refusal) || refusal.Code != SendRefusedOutcomeUnknown {
		t.Fatalf("older unfinished operation = %v, want operation_outcome_unknown", err)
	}
}

func TestSendReplaysOlderOperationAfterAnotherSend(t *testing.T) {
	f, req := sendFixture(t)
	if outcome, err := Send(f.Backend, req); err != nil || !outcome.Written {
		t.Fatalf("first send = %+v, %v", outcome, err)
	}
	req.OperationID = "op-2"
	if outcome, err := Send(f.Backend, req); err != nil || !outcome.Written {
		t.Fatalf("second send = %+v, %v", outcome, err)
	}
	req.OperationID = "op-1"
	if outcome, err := Send(f.Backend, req); err != nil || !outcome.Replayed || outcome.Written {
		t.Fatalf("older retry = %+v, %v; want replay without a write", outcome, err)
	}
	if got := sendRecorded(t, f, req.Record); got != "op-2:sent" {
		t.Fatalf("older retry changed the pane marker to %q", got)
	}
}

func TestSendRetainsLegacyPaneMarkerWhenAnotherOperationStarts(t *testing.T) {
	f, req := sendFixture(t)
	ref := backend.PaneID(req.Record.Target.Ref)
	if err := f.Backend.SetMetadata(ref, map[string]string{SendOperationVar: "op-old:sent"}); err != nil {
		t.Fatal(err)
	}
	if outcome, err := Send(f.Backend, req); err != nil || !outcome.Written {
		t.Fatalf("new send = %+v, %v", outcome, err)
	}
	req.OperationID = "op-old"
	if outcome, err := Send(f.Backend, req); err != nil || !outcome.Replayed || outcome.Written {
		t.Fatalf("legacy retry = %+v, %v; want replay", outcome, err)
	}
}

func TestSendRefusesCorruptHistoryBeforeWriting(t *testing.T) {
	f, req := sendFixture(t)
	path := sendLedgerPath(req.LockPath, req.Record.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if outcome, err := Send(f.Backend, req); err == nil || outcome.Written {
		t.Fatalf("send with corrupt history = %+v, %v; want refusal before writing", outcome, err)
	}
}

func TestSendThatCertainlyWroteNothingCanBeRepeated(t *testing.T) {
	for name, cause := range map[string]error{
		"no client slot": &tmux.CommandError{Err: tmux.ErrClientQueueTimeout},
		"another server": backend.ErrStaleGeneration,
	} {
		t.Run(name, func(t *testing.T) {
			f, req := sendFixture(t)
			outcome, err := Send(&failingTypist{SendTerminal: f.Backend, sendErr: cause}, req)
			if err == nil || outcome.Written {
				t.Fatalf("Send = %+v, %v; want a failure that wrote nothing", outcome, err)
			}
			if got := sendRecorded(t, f, req.Record); got == "op-1:pending" {
				t.Fatal("a write that never started left its operation pending, so a repeat would be refused forever")
			}
			if outcome, err := Send(f.Backend, req); err != nil || !outcome.Written {
				t.Fatalf("repeat = %+v, %v; want it written", outcome, err)
			}
		})
	}
}

func TestSendReportsInputThatLandedWhenItsRecordDidNot(t *testing.T) {
	f, req := sendFixture(t)
	outcome, err := Send(&failingTypist{SendTerminal: f.Backend, failRecord: true}, req)
	if err == nil || !outcome.Written {
		t.Fatalf("Send = %+v, %v; want the write reported beside the failure", outcome, err)
	}
	if strings.Contains(err.Error(), "sent-marker") {
		t.Fatalf("the error carries the text: %v", err)
	}
	if replay, err := Send(f.Backend, req); err != nil || !replay.Replayed || replay.Written {
		t.Fatalf("retry after pane marker failure = %+v, %v; want replay", replay, err)
	}
}

func TestSendRejectsAnOperationIDItCouldNotRecord(t *testing.T) {
	f, req := sendFixture(t)
	for _, id := range []string{"", "a b", "a:pending", strings.Repeat("a", 65)} {
		req.OperationID = id
		if _, err := Send(f.Backend, req); !errors.Is(err, backend.ErrInvalidInput) {
			t.Errorf("operation %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}
