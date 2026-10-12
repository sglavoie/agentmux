package dashboard

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func sendThrough(t *testing.T, answer string, runErr error) (func(session.RunningSession, commands.SendRequest) (commands.SendReport, error), *[]string, *string) {
	t.Helper()
	previousBinary, previousRun := attachBinary, sendRun
	attachBinary = func() (string, error) { return "/opt/amx", nil }
	var args []string
	var stdin string
	sendRun = func(bin string, got []string, in string) ([]byte, error) {
		args, stdin = append([]string{bin}, got...), in
		return []byte(answer), runErr
	}
	t.Cleanup(func() { attachBinary, sendRun = previousBinary, previousRun })
	return sendFor(stubTmux{}), &args, &stdin
}

func TestSendGoesThroughAmxSendWithTheTextOnStandardInput(t *testing.T) {
	send, args, stdin := sendThrough(t, `{"schema_version":1,"data":{"delivery":"accepted","screen":"idle"}}`, nil)
	rs := session.RunningSession{RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef"}

	report, err := send(rs, commands.SendRequest{Text: "secret prompt\nline two", Confirmed: true})
	if err != nil || report != (commands.SendReport{Delivery: "accepted", Screen: "idle"}) {
		t.Fatalf("send = %+v, %v", report, err)
	}
	want := []string{"/opt/amx", "send", "rs-0123456789abcdef0123456789abcdef", "--json", "--stdin", "--yes"}
	if !reflect.DeepEqual(*args, want) || *stdin != "secret prompt\nline two" {
		t.Fatalf("ran %q with stdin %q, want %q", *args, *stdin, want)
	}
	if strings.Contains(strings.Join(*args, " "), "secret") {
		t.Fatal("the text is in the argument list")
	}

	if _, err := send(rs, commands.SendRequest{Keys: []string{"down", "enter"}}); err != nil {
		t.Fatal(err)
	}
	want = []string{"/opt/amx", "send", "rs-0123456789abcdef0123456789abcdef", "--json", "--key", "down", "--key", "enter"}
	if !reflect.DeepEqual(*args, want) || *stdin != "" {
		t.Fatalf("ran %q with stdin %q, want %q", *args, *stdin, want)
	}

	if _, err := send(session.RunningSession{}, commands.SendRequest{Text: "x"}); err == nil {
		t.Fatal("a session with no runtime session was sent to")
	}
	if sendFor(nil) != nil {
		t.Fatal("a surface with no tmux can send")
	}
}

func TestSendAnswersAreReadByWhatTheyCarry(t *testing.T) {
	exit := errors.New("exit status 1")

	_, err := sendAnswer([]byte(`{"schema_version":1,"error":{"code":"prompt_showing","message":"the agent is asking something"}}`), exit)
	var refusal *commands.SendRefusal
	if !errors.As(err, &refusal) || refusal.Code != "prompt_showing" {
		t.Fatalf("an error envelope = %v, want a refusal by code", err)
	}
	// Written, though the record of it was not: still written.
	report, err := sendAnswer([]byte(`{"schema_version":1,"data":{"delivery":"accepted","screen":"blocked"}}`), exit)
	if err != nil || report.Delivery != "accepted" {
		t.Fatalf("a data envelope beside a nonzero exit = %+v, %v", report, err)
	}
	if _, err := sendAnswer(nil, exit); err == nil {
		t.Fatalf("no output = %v, want a failure", err)
	}
	if _, err := sendAnswer([]byte("not json"), nil); err == nil {
		t.Fatal("unreadable output was taken as a send")
	}
}
