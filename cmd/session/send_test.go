package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// sendWorld is one launched stand-in agent, a classifier that says what its
// screen is, and a daemon that answers with whatever slots the test gives it.
type sendWorld struct {
	f        *tmuxtest.Fixture
	launched session.LaunchResult
	screen   screenstate.State
	// noAdapter makes the session's harness one no adapter reads.
	noAdapter bool
	// daemon answers each state query in turn, repeating its last answer; a
	// nil daemon does not answer at all.
	daemon  []protocol.WindowStateSnapshot
	queries int
}

func newSendWorld(t *testing.T) *sendWorld {
	t.Helper()
	f, launched := attachLaunchAgent(t)
	w := &sendWorld{f: f, launched: launched, screen: screenstate.Terminal, daemon: []protocol.WindowStateSnapshot{{IconState: "stop"}}}
	previousDaemon, previousClassify, previousPause := sessionCommandsDaemon, sendClassify, sendPause
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) {
		if w.daemon == nil {
			return protocol.Response{}, errors.New("daemon down")
		}
		state := w.daemon[min(w.queries, len(w.daemon)-1)]
		w.queries++
		return protocol.Response{
			OK:           true,
			States:       map[int]protocol.WindowStateSnapshot{7: state},
			RuntimeSlots: map[int]protocol.RuntimeSlot{7: {RuntimeSessionID: string(launched.RuntimeSessionID), Backend: "tmux"}},
		}, nil
	}
	sendClassify = func(string, string) (screenstate.State, bool) { return w.screen, !w.noAdapter }
	sendPause = func(time.Duration) {}
	t.Cleanup(func() {
		sessionCommandsDaemon, sendClassify, sendPause = previousDaemon, previousClassify, previousPause
	})
	return w
}

func (w *sendWorld) id() string { return string(w.launched.RuntimeSessionID) }

func (w *sendWorld) run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	reset := func() {
		sendSearchContext = false
		sendJSON, sendStdin, sendKeys, sendNoEnter, sendAnyScreen, sendYes, sendOperationID, sendWait, sendLook = false, false, nil, false, false, false, "", 0, false
	}
	reset()
	t.Cleanup(reset)
	cmd := &cobra.Command{Use: "send", RunE: runSend, Args: cobra.MaximumNArgs(2), SilenceUsage: true, SilenceErrors: true}
	cmd.Flags().BoolVar(&sendJSON, "json", false, "")
	cmd.Flags().BoolVar(&sendStdin, "stdin", false, "")
	cmd.Flags().StringArrayVar(&sendKeys, "key", nil, "")
	cmd.Flags().BoolVar(&sendNoEnter, "no-enter", false, "")
	cmd.Flags().BoolVar(&sendAnyScreen, "any-screen", false, "")
	cmd.Flags().BoolVar(&sendYes, "yes", false, "")
	cmd.Flags().StringVar(&sendOperationID, "operation-id", "", "")
	cmd.Flags().DurationVar(&sendWait, "wait", 0, "")
	cmd.Flags().BoolVar(&sendLook, "look", false, "")
	cmd.Flags().BoolVar(&sendSearchContext, "search-context", false, "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func (w *sendWorld) sent(t *testing.T, args ...string) sendData {
	t.Helper()
	out, err := w.run(t, "", append(args, "--json")...)
	if err != nil {
		t.Fatalf("send %v: %v\n%s", args, err, out)
	}
	var envelope struct {
		SchemaVersion int      `json:"schema_version"`
		Data          sendData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.SchemaVersion != 1 {
		t.Fatalf("decode %q: %v", out, err)
	}
	return envelope.Data
}

func (w *sendWorld) refused(t *testing.T, want string, args ...string) {
	t.Helper()
	out, err := w.run(t, "", append(args, "--json")...)
	if err == nil {
		t.Fatalf("send %v succeeded, want %s\n%s", args, want, out)
	}
	if code := closeErrorCode(t, out); code != want {
		t.Fatalf("send %v refused as %q, want %q\n%s", args, code, want, out)
	}
}

// screenText is the agent's screen. The stand-in agent reads nothing, so what
// its terminal echoes is exactly what was typed to it.
func (w *sendWorld) screenText(t *testing.T) string {
	t.Helper()
	text, err := w.f.Backend.Capture(w.launched.Pane, backend.ExtentScreen)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func (w *sendWorld) waitTyped(t *testing.T, marker string, times int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := strings.Count(w.screenText(t), marker)
		if got == times {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen shows %q %d times, want %d:\n%s", marker, got, times, w.screenText(t))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *sendWorld) operation(t *testing.T) string {
	t.Helper()
	panes, err := w.f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	pane, _ := backend.FindPane(panes, w.launched.Pane)
	return pane.Vars[session.SendOperationVar]
}

func TestSendTypesAPromptOnceHoweverOftenItIsAsked(t *testing.T) {
	w := newSendWorld(t)
	data := w.sent(t, w.id(), "prompt-marker", "--operation-id", "op-1")
	want := sendData{RuntimeSessionID: w.id(), Backend: "tmux", Project: "api", OperationID: "op-1", Input: "text", Screen: "idle", Delivery: "accepted"}
	if data != want {
		t.Fatalf("payload = %+v, want %+v", data, want)
	}
	w.waitTyped(t, "prompt-marker", 1)
	if got := w.operation(t); got != "op-1:sent" {
		t.Fatalf("recorded operation = %q, want op-1:sent", got)
	}

	again := w.sent(t, w.id(), "prompt-marker", "--operation-id", "op-1")
	if again.Delivery != "replayed" {
		t.Fatalf("repeat = %+v, want a replay", again)
	}
	// A different operation is a different send.
	if next := w.sent(t, w.id(), "prompt-marker", "--operation-id", "op-2"); next.Delivery != "accepted" {
		t.Fatalf("second operation = %+v, want accepted", next)
	}
	w.waitTyped(t, "prompt-marker", 2)
	if older := w.sent(t, w.id(), "prompt-marker", "--operation-id", "op-1"); older.Delivery != "replayed" {
		t.Fatalf("older retry = %+v, want a replay", older)
	}
	w.waitTyped(t, "prompt-marker", 2)

	// Sending attached nothing and took the session from nobody.
	if record, _ := w.f.Load(t).Get(w.launched.RuntimeSessionID); record.Attachment != nil || record.State != runtimesession.StateLive {
		t.Fatalf("record = %+v, want live and unowned", record)
	}
	if out, err := w.run(t, "", w.id(), "plain-marker"); err != nil || out != "Sent to api.\n" {
		t.Fatalf("text output = %q, %v", out, err)
	}
}

func TestSendReadsTextFromStandardInput(t *testing.T) {
	w := newSendWorld(t)
	if out, err := w.run(t, "piped-marker\n", w.id(), "--stdin", "--json"); err != nil {
		t.Fatalf("send --stdin: %v\n%s", err, out)
	}
	w.waitTyped(t, "piped-marker", 1)
}

func TestSendHoldsInputToWhatTheScreenShows(t *testing.T) {
	w := newSendWorld(t)

	w.screen = screenstate.Blocked
	w.refused(t, "prompt_showing", w.id(), "typed-marker")
	if data := w.sent(t, w.id(), "--key", "2", "--key", "Enter"); data.Input != "keys" || data.Screen != "blocked" {
		t.Fatalf("keys at a prompt = %+v", data)
	}

	w.screen = screenstate.Working
	w.refused(t, "no_prompt_showing", w.id(), "--key", "y")
	if data := w.sent(t, w.id(), "queued-marker"); data.Screen != "working" {
		t.Fatalf("text while working = %+v", data)
	}

	// No prompt showing and nothing else to go on: a session before its first
	// turn. Text is what such a session is for; keys still need a prompt.
	w.screen = screenstate.Unknown
	w.refused(t, "no_prompt_showing", w.id(), "--key", "y")
	if data := w.sent(t, w.id(), "first-marker"); data.Screen != "unknown" {
		t.Fatalf("text before a first turn = %+v", data)
	}

	w.noAdapter = true
	w.refused(t, "screen_unreadable", w.id(), "typed-marker")
	w.refused(t, "screen_unreadable", w.id(), "--key", "y")
	if data := w.sent(t, w.id(), "forced-marker", "--any-screen"); data.Screen != "unchecked" {
		t.Fatalf("--any-screen = %+v", data)
	}

	w.waitTyped(t, "forced-marker", 1)
	if screen := w.screenText(t); strings.Contains(screen, "typed-marker") {
		t.Fatalf("a refused send was typed:\n%s", screen)
	}
}

func TestSendStaysOutOfAgentSessionLoopsLane(t *testing.T) {
	w := newSendWorld(t)

	w.daemon = []protocol.WindowStateSnapshot{{IconState: "bolt", ASLRunIDs: []string{"run-1"}}}
	w.refused(t, "confirmation_required", w.id(), "typed-marker")
	if data := w.sent(t, w.id(), "supervisor-marker", "--yes"); data.Delivery != "accepted" {
		t.Fatalf("confirmed send = %+v", data)
	}

	w.daemon = []protocol.WindowStateSnapshot{{IconState: "question", ASLRunIDs: []string{"run-1"}}}
	w.refused(t, "asl_question_open", w.id(), "typed-marker", "--yes")
	w.refused(t, "asl_question_open", w.id(), "--key", "enter", "--yes", "--any-screen")

	w.daemon = nil
	w.refused(t, "daemon_unavailable", w.id(), "typed-marker")
	w.sent(t, w.id(), "unchecked-marker", "--yes")

	w.waitTyped(t, "unchecked-marker", 1)
	if screen := w.screenText(t); strings.Contains(screen, "typed-marker") {
		t.Fatalf("a refused send was typed:\n%s", screen)
	}
}

func TestSendWaitsForTheAgentToTakeIt(t *testing.T) {
	w := newSendWorld(t)

	w.screen = screenstate.Blocked
	w.daemon = []protocol.WindowStateSnapshot{{IconState: "key"}, {IconState: "key"}, {IconState: "bolt"}}
	if data := w.sent(t, w.id(), "--key", "1", "--wait", "5s"); data.Delivery != "consumed" {
		t.Fatalf("answered prompt = %+v, want consumed", data)
	}

	// A session that never moves stays accepted.
	w.queries, w.daemon = 0, []protocol.WindowStateSnapshot{{IconState: "key"}}
	if data := w.sent(t, w.id(), "--key", "1", "--wait", "1s"); data.Delivery != "accepted" {
		t.Fatalf("unanswered prompt = %+v, want accepted", data)
	}

	// An agent already working moves by itself, so it is never watched.
	w.screen = screenstate.Working
	w.queries, w.daemon = 0, []protocol.WindowStateSnapshot{{IconState: "bolt"}, {IconState: "stop"}}
	if data := w.sent(t, w.id(), "queued-marker", "--wait", "5s"); data.Delivery != "accepted" || w.queries != 1 {
		t.Fatalf("working agent = %+v after %d queries, want accepted after one", data, w.queries)
	}
}

func TestSendRefusesAnOperationNobodySawFinish(t *testing.T) {
	w := newSendWorld(t)
	if err := w.f.Backend.SetMetadata(w.launched.Pane, map[string]string{session.SendOperationVar: "op-9:pending"}); err != nil {
		t.Fatal(err)
	}
	w.refused(t, "operation_outcome_unknown", w.id(), "typed-marker", "--operation-id", "op-9")
	if got := w.operation(t); got != "op-9:pending" {
		t.Fatalf("recorded operation = %q, want it left as it was", got)
	}
}

func TestSendRejectsWhatItCannotMean(t *testing.T) {
	w := newSendWorld(t)
	for name, args := range map[string][]string{
		"nothing to send":   {w.id()},
		"text and keys":     {w.id(), "text", "--key", "enter"},
		"text and stdin":    {w.id(), "text", "--stdin"},
		"unknown key":       {w.id(), "--key", "C-c"},
		"keys, no enter":    {w.id(), "--key", "y", "--no-enter"},
		"control character": {w.id(), "a\x1b[201~b"},
		"bad operation":     {w.id(), "text", "--operation-id", "a b"},
		"bad session":       {"nonsense", "text"},
	} {
		out, err := w.run(t, "", append(args, "--json")...)
		if err == nil || closeErrorCode(t, out) != "invalid_arguments" {
			t.Errorf("%s: err=%v out=%s, want invalid_arguments", name, err, out)
		}
	}
	w.refused(t, "not_found", "rs-00000000000000000000000000000000", "text")
	if w.queries != 0 {
		t.Fatalf("the daemon was asked %d times about sends that were never valid", w.queries)
	}
}

// Standard input longer than a send may carry is refused whole, even when it
// is cut at a newline that trimming would remove, and nothing is typed.
func TestSendRefusesStdinCutAtTheLimit(t *testing.T) {
	w := newSendWorld(t)
	for _, tail := range []string{"\nlost-marker\n", "\r\n\nlost-marker\n"} {
		long := strings.Repeat("a", backend.InputMaxBytes) + tail
		out, err := w.run(t, long, w.id(), "--stdin", "--json")
		if err == nil || closeErrorCode(t, out) != "invalid_arguments" {
			t.Fatalf("send of %d bytes ending %q: err=%v out=%s, want invalid_arguments", len(long), tail, err, out)
		}
	}
	if w.queries != 0 {
		t.Fatalf("the daemon was asked %d times about a send that was never valid", w.queries)
	}
	// The limit itself, with the line ending a pipe adds, still goes through.
	exact := strings.Repeat("a", backend.InputMaxBytes) + "\r\n"
	if out, err := w.run(t, exact, w.id(), "--stdin", "--no-enter", "--json"); err != nil {
		t.Fatalf("send of the limit: %v\n%s", err, out)
	}
}

func TestSendRefusesAnAgentThatStopped(t *testing.T) {
	w := newSendWorld(t)
	if out, err := closeRun(t, w.id(), "--yes", "--json"); err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	out, err := w.run(t, "", w.id(), "typed-marker", "--json")
	if err == nil || !strings.HasPrefix(closeErrorCode(t, out), "runtime_session_") {
		t.Fatalf("send to a closed session: err=%v out=%s", err, out)
	}
}

func TestSendThroughAServerThatRefusesLosesNothing(t *testing.T) {
	w := newSendWorld(t)
	w.f.Refuse(t)
	w.refused(t, "backend_unavailable", w.id(), "typed-marker")
	if record, _ := w.f.Load(t).Get(w.launched.RuntimeSessionID); record.State != runtimesession.StateLive {
		t.Fatalf("record = %s %q, want live", record.State, record.StaleReason)
	}
}

func TestSendLookShowsTheScreenAndWritesNothing(t *testing.T) {
	w := newSendWorld(t)
	w.sent(t, w.id(), "look-marker", "--no-enter")
	w.waitTyped(t, "look-marker", 1)
	before := w.operation(t)

	w.screen = screenstate.Blocked
	out, err := w.run(t, "", w.id(), "--look", "--json")
	if err != nil {
		t.Fatalf("send --look: %v\n%s", err, out)
	}
	var envelope struct {
		SchemaVersion int          `json:"schema_version"`
		Data          sendLookData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.SchemaVersion != 1 {
		t.Fatalf("decode %q: %v", out, err)
	}
	data := envelope.Data
	if data.RuntimeSessionID != w.id() || data.Backend != "tmux" || data.Project != "api" || data.Screen != "blocked" {
		t.Fatalf("payload = %+v", data)
	}
	if len(data.Lines) == 0 || !strings.Contains(strings.Join(data.Lines, "\n"), "look-marker") {
		t.Fatalf("lines = %q, want the screen's tail", data.Lines)
	}
	if last := data.Lines[len(data.Lines)-1]; strings.TrimSpace(last) == "" {
		t.Fatalf("the tail ends in a blank row: %q", data.Lines)
	}
	// Looking is not sending: nothing was typed, recorded, or asked of the daemon.
	queries := w.queries
	if text, err := w.run(t, "", w.id(), "--look"); err != nil || !strings.Contains(text, "look-marker") {
		t.Fatalf("text output = %q, %v", text, err)
	}
	if got := w.operation(t); got != before || w.queries != queries {
		t.Fatalf("a look recorded %q (was %q) and asked the daemon %d times", got, before, w.queries-queries)
	}
	w.waitTyped(t, "look-marker", 1)

	// A harness no adapter reads can still be looked at.
	w.noAdapter = true
	if out, err := w.run(t, "", w.id(), "--look", "--json"); err != nil || !strings.Contains(out, `"screen":"unknown"`) {
		t.Fatalf("look with no adapter = %q, %v", out, err)
	}
	for name, args := range map[string][]string{
		"with text":  {w.id(), "text", "--look"},
		"with a key": {w.id(), "--key", "y", "--look"},
		"with stdin": {w.id(), "--stdin", "--look"},
	} {
		out, err := w.run(t, "", append(args, "--json")...)
		if err == nil || closeErrorCode(t, out) != "invalid_arguments" {
			t.Errorf("%s: err=%v out=%s, want invalid_arguments", name, err, out)
		}
	}
}

func TestSendLookSearchContextIsReadOnlyAndCannotBeUsedWithInput(t *testing.T) {
	w := newSendWorld(t)
	before := w.operation(t)
	out, err := w.run(t, "", w.id(), "--look", "--search-context", "--json")
	if err != nil {
		t.Fatalf("look: %s %v", out, err)
	}
	var envelope struct {
		Data sendLookData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Search == nil || envelope.Data.Search.Scope != "terminal_history" {
		t.Fatalf("search = %+v", envelope.Data.Search)
	}
	if w.operation(t) != before {
		t.Fatal("reading search wrote an input receipt")
	}
	out, err = w.run(t, "", w.id(), "never send this", "--search-context", "--json")
	if err == nil || !strings.Contains(out, "invalid_arguments") {
		t.Fatalf("input accepted: %s %v", out, err)
	}
	if w.operation(t) != before {
		t.Fatal("refused input wrote a receipt")
	}
}
