package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sendform"
)

// tmuxTestRow is a session on the private tmux server, as the dashboard lists
// one: a process-local ID standing in for its tab and window.
func tmuxTestRow(state string) session.RunningSession {
	return session.RunningSession{
		TabID: commands.PortableIDBase, WindowID: commands.PortableIDBase,
		Base: "api", Project: "api", Profile: "work", Account: "work", Directory: "/home/user/api", State: state,
		Backend: runtimesession.BackendTmux, Pane: backend.PaneID("%1"), Group: backend.GroupID("$1"),
		RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef",
	}
}

// sendRecorder stands in for `amx send`: it keeps what the form asked for and
// answers with whatever the test queued.
type sendRecorder struct {
	requests []commands.SendRequest
	sessions []runtimesession.ID
	answers  []error
}

func installSendRecorder(t *testing.T) *sendRecorder {
	t.Helper()
	recorder := &sendRecorder{}
	previous := commands.Portable
	commands.Portable.Send = func(rs session.RunningSession, req commands.SendRequest) (commands.SendReport, error) {
		recorder.requests = append(recorder.requests, req)
		recorder.sessions = append(recorder.sessions, rs.RuntimeSessionID)
		var err error
		if len(recorder.answers) > 0 {
			err, recorder.answers = recorder.answers[0], recorder.answers[1:]
		}
		return commands.SendReport{Delivery: "accepted", Screen: "idle"}, err
	}
	t.Cleanup(func() { commands.Portable = previous })
	return recorder
}

var ctrlS = tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl})

// sendFormOn opens the form on one tmux row and answers its first screen read.
func sendFormOn(t *testing.T, row session.RunningSession) Model {
	t.Helper()
	m := pressKey(withListRows(testModelWithSessions(), row), keySend)
	if m.mode != ModeSend || !m.send.Active {
		t.Fatalf("%q on a tmux row left mode %v, want the send form", keySend, m.mode)
	}
	updated, _ := m.Update(commands.SendScreenMsg{Seq: m.send.Seq, Text: "❯ 1. Yes\n  2. No"})
	return updated.(Model)
}

// step feeds msg and then every message the resulting command produces that is
// the form's own, so a send and the screen read after it both land.
func sendStep(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	updated, cmd := m.Update(msg)
	m = updated.(Model)
	if cmd == nil {
		return m
	}
	if done, ok := cmd().(commands.SendDoneMsg); ok {
		updated, _ = m.Update(done)
		m = updated.(Model)
	}
	return m
}

func TestSendIsOfferedOnlyWhereThereIsAPaneToWriteTo(t *testing.T) {
	asking := tmuxTestRow("question")
	asking.ASLPhases = []string{"implement"}
	supervising := tmuxTestRow("bolt")
	supervising.ASLPhases = []string{"implement"}
	for name, tc := range map[string]struct {
		row  session.RunningSession
		want string
	}{
		"tmux session":           {tmuxTestRow("stop"), ""},
		"tmux session, asking":   {tmuxTestRow("key"), ""},
		"Kitty tab":              {session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Account: "work", State: "stop"}, commands.ReasonSendNeedsTmux},
		"run with no question":   {supervising, ""},
		"run's question is open": {asking, commands.ReasonSendASLQuestion},
	} {
		got := commands.SendAvailability(commands.Context{Target: tc.row, HasTarget: true})
		if got.Reason != tc.want || got.Available != (tc.want == "") {
			t.Errorf("%s: availability = %+v, want reason %q", name, got, tc.want)
		}
	}
	if got := commands.SendAvailability(commands.Context{}); got.Reason != commands.ReasonNoSession {
		t.Errorf("no row: availability = %+v", got)
	}

	// The key is dead on a row that cannot be sent to.
	kitty := withListRows(testModelWithSessions(), session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Account: "work", State: "stop"})
	if m := pressKey(kitty, keySend); m.mode != ModeList || m.send.Active {
		t.Fatalf("%q on a Kitty row opened the form (mode %v)", keySend, m.mode)
	}
}

func TestSendFormOpensOnWhatTheRowIsWaitingFor(t *testing.T) {
	if m := sendFormOn(t, tmuxTestRow("key")); !m.send.FocusKeys {
		t.Error("a row that is asking opened on the text half")
	}
	if m := sendFormOn(t, tmuxTestRow("stop")); m.send.FocusKeys {
		t.Error("a row at rest opened on the keys half")
	}
}

func TestSendFormSendsTextOnPurposeAndThenCloses(t *testing.T) {
	recorder := installSendRecorder(t)
	m := sendFormOn(t, tmuxTestRow("stop"))
	m = typeString(m, "run the tests")
	m = pressKey(m, "enter")
	m = typeString(m, "then stop")
	if len(recorder.requests) != 0 {
		t.Fatalf("typing sent %d requests; only ctrl+s sends text", len(recorder.requests))
	}
	m = sendStep(t, m, ctrlS)
	if len(recorder.requests) != 1 || recorder.requests[0].Text != "run the tests\nthen stop" || len(recorder.requests[0].Keys) != 0 {
		t.Fatalf("requests = %+v, want the two-line draft as text", recorder.requests)
	}
	if recorder.sessions[0] != tmuxTestRow("").RuntimeSessionID {
		t.Fatalf("sent to %s", recorder.sessions[0])
	}
	if m.mode != ModeList || m.send.Active || m.send.Draft != "" {
		t.Fatalf("after a sent prompt: mode %v, form %v", m.mode, m.send)
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[0].Text, "sent to") {
		t.Fatalf("toasts = %+v, want one saying it was sent", m.toasts)
	}
}

func TestSendFormRefusesAnEmptyDraft(t *testing.T) {
	recorder := installSendRecorder(t)
	m := sendStep(t, sendFormOn(t, tmuxTestRow("stop")), ctrlS)
	if len(recorder.requests) != 0 || !m.send.Warn || m.mode != ModeSend {
		t.Fatalf("an empty draft: %d requests, form %v", len(recorder.requests), m.send)
	}
}

func TestSendFormForwardsEachKeyAtOnceAndStaysOpen(t *testing.T) {
	recorder := installSendRecorder(t)
	m := sendFormOn(t, tmuxTestRow("key"))
	for _, key := range []string{"2", "y", "enter", " ", "left"} {
		m = sendStep(t, m, keyPress(key))
	}
	var got []string
	for _, req := range recorder.requests {
		if req.Text != "" || len(req.Keys) != 1 {
			t.Fatalf("request = %+v, want one key and no text", req)
		}
		got = append(got, req.Keys[0])
	}
	if want := []string{"2", "y", "enter", "space", "left"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("keys sent = %v, want %v", got, want)
	}
	if m.mode != ModeSend || m.send.Notice != "Sent." || m.send.Draft != "" {
		t.Fatalf("after keys: mode %v, form %v, draft %q", m.mode, m.send, m.send.Draft)
	}
	// A key the form does not forward is not typed into the draft from the
	// keys half, and tab moves to the text, where the same key is a letter.
	m = sendStep(t, m, keyPress("x"))
	m = pressKey(m, "tab")
	m = pressKey(m, "y")
	if len(recorder.requests) != 5 || m.send.Draft != "y" || m.send.FocusKeys {
		t.Fatalf("%d requests, draft %q, keys half %v", len(recorder.requests), m.send.Draft, m.send.FocusKeys)
	}
}

func TestSendFormAsksBeforeSendingIntoARun(t *testing.T) {
	recorder := installSendRecorder(t)
	recorder.answers = []error{&commands.SendRefusal{Code: commands.SendRefusedConfirmation, Message: "runs a run"}}
	m := typeString(sendFormOn(t, tmuxTestRow("stop")), "hello")
	m = sendStep(t, m, ctrlS)
	if m.mode != ModeSend || !m.send.Confirmed || m.send.Draft != "hello" || !strings.Contains(m.send.Notice, "Send again to confirm") {
		t.Fatalf("after the refusal: mode %v, form %v, notice %q", m.mode, m.send, m.send.Notice)
	}
	m = sendStep(t, m, ctrlS)
	if len(recorder.requests) != 2 || recorder.requests[0].Confirmed || !recorder.requests[1].Confirmed {
		t.Fatalf("requests = %+v, want the second one confirmed", recorder.requests)
	}
	if m.mode != ModeList {
		t.Fatalf("the confirmed send left mode %v", m.mode)
	}
}

func TestSendFormKeepsTheDraftWhenASendIsRefused(t *testing.T) {
	recorder := installSendRecorder(t)
	recorder.answers = []error{&commands.SendRefusal{Code: "prompt_showing", Message: "the agent is asking something"}}
	m := typeString(sendFormOn(t, tmuxTestRow("stop")), "hello")
	m = sendStep(t, m, ctrlS)
	if m.mode != ModeSend || m.send.Draft != "hello" || !m.send.Warn || !strings.Contains(m.send.Notice, "keys half") || m.send.InFlight {
		t.Fatalf("after a refusal: mode %v, form %v, notice %q", m.mode, m.send, m.send.Notice)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", m.send, m.send, m.send), "hello") {
		t.Fatal("formatting the form's state printed the draft")
	}
}

func TestSendFormDropsAnswersMeantForAnEarlierForm(t *testing.T) {
	installSendRecorder(t)
	m := typeString(sendFormOn(t, tmuxTestRow("stop")), "hello")
	updated, cmd := m.Update(ctrlS)
	m = updated.(Model)
	late := cmd().(commands.SendDoneMsg)
	lateScreen := commands.SendScreenMsg{Seq: late.Seq, Text: "stale"}

	m = pressKey(m, "esc")
	if m.mode != ModeList || m.send.Active {
		t.Fatalf("esc left mode %v", m.mode)
	}
	m = pressKey(m, keySend)
	before := m.send
	for _, msg := range []tea.Msg{late, lateScreen, commands.SendDoneMsg{Seq: late.Seq, Err: errors.New("boom")}} {
		updated, _ = m.Update(msg)
		m = updated.(Model)
	}
	if !reflect.DeepEqual(m.send, before) || m.mode != ModeSend || len(m.toasts) != 0 {
		t.Fatalf("a late answer changed the new form: %v -> %v", before, m.send)
	}
}

func TestSendFormIgnoresKeysWhileASendIsInFlight(t *testing.T) {
	recorder := installSendRecorder(t)
	m := sendFormOn(t, tmuxTestRow("key"))
	updated, _ := m.Update(keyPress("1"))
	m = updated.(Model)
	if !m.send.InFlight {
		t.Fatal("a sent key did not mark the form in flight")
	}
	m = pressKey(pressKey(m, "2"), "tab")
	if !m.send.FocusKeys || !m.send.InFlight {
		t.Fatalf("keys pressed during a send moved the form: %v", m.send)
	}
	_ = recorder
}

func TestSendFormFitsNarrowAndWideTerminals(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		m := sendFormOn(t, tmuxTestRow("key"))
		m.width, m.height = width, 30
		m.send.Screen = strings.Repeat("w", 200) + "\n❯ 1. Yes"
		m.send.Draft = strings.Repeat("d", 200)
		m.send.Notice, m.send.Warn = strings.Repeat("n ", 120), true
		box := renderSendBox(m)
		for _, line := range strings.Split(box, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width %d: a form line is %d wide: %q", width, got, ansi.Strip(line))
			}
		}
		if !strings.Contains(ansi.Strip(box), "Send to") || !strings.Contains(ansi.Strip(box), "❯ 1. Yes") {
			t.Fatalf("width %d: the form lost its title or the agent's screen:\n%s", width, ansi.Strip(box))
		}
		if view := m.View(); !strings.Contains(ansi.Strip(view.Content), "Send to") {
			t.Fatalf("width %d: the dashboard does not draw the form", width)
		}
	}
	if _, ok := sendform.KeyFor("tab"); ok {
		t.Fatal("tab is forwarded; it has to stay the form's own")
	}
}
