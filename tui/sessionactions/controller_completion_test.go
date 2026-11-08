package sessionactions

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestCompleteCorrelatesWithItsDispatch is the correlation contract: a reported
// completion names the dispatch it answers, and the record it clears is that
// dispatch's alone.
func TestCompleteCorrelatesWithItsDispatch(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21), row(12, 22)}}
	c := testController(hostEffects(commands.CloseSession, commands.RestartSession))

	h, first, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})
	h, second, _ := c.Begin(h, commands.RestartSession, Capture(row(12, 22)), Origin{})
	if first.Request == second.Request {
		t.Fatalf("both dispatches minted request %d", first.Request)
	}
	if got := h.state.InFlight(); len(got) != 2 {
		t.Fatalf("in flight = %+v, want two records", got)
	}

	h, result, ok := c.Complete(h, second.Request, Completion{})
	if !ok {
		t.Fatal("completion was not correlated")
	}
	if result.Action != commands.RestartSession || result.Request != second.Request {
		t.Fatalf("result = %+v, want the restart dispatch %d", result, second.Request)
	}
	if !result.Succeeded() {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeSucceeded)
	}
	remaining := h.state.InFlight()
	if len(remaining) != 1 || remaining[0].Request != first.Request {
		t.Fatalf("in flight = %+v, want only the close dispatch %d", remaining, first.Request)
	}

	failure := errors.New("tmux refused")
	h, result, ok = c.Complete(h, first.Request, Completion{Err: failure})
	if !ok {
		t.Fatal("failing completion was not correlated")
	}
	if result.Outcome != OutcomeFailed || !errors.Is(result.Err, failure) || result.Detail != failure.Error() {
		t.Fatalf("result = %+v, want a failure carrying %v", result, failure)
	}
	if got := h.state.InFlight(); len(got) != 0 {
		t.Fatalf("in flight = %+v, want empty", got)
	}
}

// TestUnconfirmedCompletionIsNeitherSuccessNorFailure is the third answer: the
// action was accepted, nothing failed, and its effect was never observed. A
// surface that offered close recovery on this would be offering to undo a close
// that may not have happened.
func TestUnconfirmedCompletionIsNeitherSuccessNorFailure(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))
	h, dispatched, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})

	_, result, ok := c.Complete(h, dispatched.Request, Completion{Unconfirmed: true})
	if !ok {
		t.Fatal("unconfirmed completion was not correlated")
	}
	if result.Outcome != OutcomeUnconfirmed {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeUnconfirmed)
	}
	if result.Succeeded() {
		t.Fatal("an unconfirmed action reported success")
	}
	if result.Err != nil || result.Detail != "" {
		t.Fatalf("result = %+v, want no failure to report", result)
	}

	// A failure is the stronger answer and wins.
	failure := errors.New("tmux refused")
	if got := (Completion{Err: failure, Unconfirmed: true}).Outcome(); got != OutcomeFailed {
		t.Fatalf("outcome = %q, want %q", got, OutcomeFailed)
	}
}

// TestCompleteIgnoresForeignCompletions is what lets the host keep its own
// handling for everything else the same message carries: an undo, a direct key,
// a reply already reported. None of them are this controller's.
func TestCompleteIgnoresForeignCompletions(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))
	h, dispatched, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})

	for _, request := range []RequestID{0, dispatched.Request + 1} {
		out, result, ok := c.Complete(h, request, Completion{})
		if ok {
			t.Fatalf("request %d was correlated: %+v", request, result)
		}
		if got := out.state.InFlight(); len(got) != 1 {
			t.Fatalf("request %d disturbed the record: %+v", request, got)
		}
	}

	// The same completion twice is a foreign one the second time, which is what
	// keeps a retried reply from being counted as a second action.
	h, _, ok := c.Complete(h, dispatched.Request, Completion{})
	if !ok {
		t.Fatal("first completion was not correlated")
	}
	if _, _, ok := c.Complete(h, dispatched.Request, Completion{}); ok {
		t.Fatal("the same completion was correlated twice")
	}
}

// TestInFlightRecordIsBounded is the leak guard: an effect that never reports
// back must not grow the state without limit.
func TestInFlightRecordIsBounded(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.FocusSession))

	for i := 0; i < maxInFlight+5; i++ {
		h, _, _ = c.Begin(h, commands.FocusSession, Capture(row(11, 21)), Origin{})
	}

	got := h.state.InFlight()
	if len(got) != maxInFlight {
		t.Fatalf("in flight = %d records, want the bound %d", len(got), maxInFlight)
	}
	// The bound drops the oldest, so the newest dispatch is always correlatable.
	if got[len(got)-1].Request != RequestID(maxInFlight+5) {
		t.Fatalf("newest record = %d, want %d", got[len(got)-1].Request, maxInFlight+5)
	}
}

// TestInFlightIsCopied guards the value-copied host model: two copies must not
// write through one backing array.
func TestInFlightIsCopied(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.FocusSession))
	h, _, _ = c.Begin(h, commands.FocusSession, Capture(row(11, 21)), Origin{})

	snapshot := h.state.InFlight()
	snapshot[0].Action = "tampered"
	if again := h.state.InFlight(); again[0].Action != commands.FocusSession {
		t.Fatalf("state was written through its own read projection: %+v", again)
	}
}

// TestHostHostsOnlyItsOwnTab pins the hosting-tab identity. It is asked of the
// captured target's tab and answers false whenever either side is unknown, so an
// unresolved host cannot claim every row.
func TestHostHostsOnlyItsOwnTab(t *testing.T) {
	target := Capture(row(11, 21))
	cases := []struct {
		name string
		host Host
		want bool
	}{
		{name: "same tab", host: Host{TabID: 21, WindowID: 99}, want: true},
		{name: "other tab", host: Host{TabID: 22, WindowID: 99}, want: false},
		{name: "unresolved host", host: Host{}, want: false},
		{name: "window only", host: Host{WindowID: 11}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.host.Hosts(target); got != tc.want {
				t.Fatalf("Hosts = %v, want %v", got, tc.want)
			}
		})
	}
	if (Host{TabID: 21}).Hosts(Target{}) {
		t.Fatal("a target with no session was claimed by the host tab")
	}
}

// TestConsequencesAreHarnessNeutral is the copy regression. The sentences used
// to name Claude, which was wrong on a Codex or Copilot row and had been since
// the second harness landed.
func TestConsequencesAreHarnessNeutral(t *testing.T) {
	for _, id := range []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile} {
		prompt, ok := ConfirmPromptFor(id, "personal")
		if !ok {
			t.Fatalf("%s declares no confirmation prompt", id)
		}
		if prompt.Consequence == "" {
			t.Fatalf("%s confirms with no consequence", id)
		}
		for _, harness := range []string{"Claude", "claude", "Codex", "Copilot"} {
			if strings.Contains(prompt.Consequence, harness) {
				t.Fatalf("%s consequence %q names the harness %q", id, prompt.Consequence, harness)
			}
		}
	}
	if _, ok := ConfirmPromptFor(commands.FocusSession, ""); ok {
		t.Fatal("a non-destructive action declares a confirmation prompt")
	}
}

// TestDestructiveAndNeedsProfileAgree documents the two independent questions
// the picker and the prompt answer, and in particular that launching another
// session needs a profile without being destructive.
func TestDestructiveAndNeedsProfileAgree(t *testing.T) {
	cases := []struct {
		id          commands.ID
		destructive bool
		needsProf   bool
	}{
		{id: commands.CloseSession, destructive: true},
		{id: commands.RestartSession, destructive: true},
		{id: commands.OverrideProfile, destructive: true, needsProf: true},
		{id: commands.LaunchAnother, needsProf: true},
		{id: commands.FocusSession},
		{id: commands.ToggleLockID},
	}
	for _, tc := range cases {
		if got := Destructive(tc.id); got != tc.destructive {
			t.Errorf("Destructive(%s) = %v, want %v", tc.id, got, tc.destructive)
		}
		if got := NeedsProfile(tc.id); got != tc.needsProf {
			t.Errorf("NeedsProfile(%s) = %v, want %v", tc.id, got, tc.needsProf)
		}
	}
}

// TestSameSessionIsConservativeInBothDirections pins the revalidation
// predicate. Identity evidence is used where it exists and ignored where it does
// not, because a row the daemon has not yet attributed carries none and refusing
// every such row would make the dashboard unusable during its first seconds.
func TestSameSessionIsConservativeInBothDirections(t *testing.T) {
	base := row(11, 21)
	cases := []struct {
		name string
		with func(session.RunningSession) session.RunningSession
		want bool
	}{
		{name: "identical", with: func(s session.RunningSession) session.RunningSession { return s }, want: true},
		{name: "different tab", with: func(s session.RunningSession) session.RunningSession { s.TabID = 99; return s }, want: false},
		{name: "different window", with: func(s session.RunningSession) session.RunningSession { s.WindowID = 99; return s }, want: false},
		{name: "unattributed window", with: func(s session.RunningSession) session.RunningSession { s.WindowID = 0; return s }, want: true},
		{name: "different launch", with: func(s session.RunningSession) session.RunningSession { s.LaunchID = "other"; return s }, want: false},
		{name: "unknown launch", with: func(s session.RunningSession) session.RunningSession { s.LaunchID = ""; return s }, want: true},
		{name: "different harness session", with: func(s session.RunningSession) session.RunningSession { s.HarnessSessionID = "other"; return s }, want: false},
		{name: "unknown harness session", with: func(s session.RunningSession) session.RunningSession { s.HarnessSessionID = ""; return s }, want: true},
		{name: "renamed", with: func(s session.RunningSession) session.RunningSession { s.Base = "other"; return s }, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameSession(base, tc.with(base)); got != tc.want {
				t.Fatalf("SameSession = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNilSeamsAreTolerated covers the host that has no surface to leave — the
// overview, or a test driving the controller alone. Every navigation seam is
// optional; the effect table is not.
func TestNilSeamsAreTolerated(t *testing.T) {
	effects := Effects[host]{
		State:    func(h host) State { return h.state },
		SetState: func(h host, state State) host { h.state = state; return h },
		Run: map[commands.ID]Effect[host]{
			commands.FocusSession: func(h host, d Dispatch) (host, tea.Cmd) {
				h.ran = append(append([]Dispatch(nil), h.ran...), d)
				return h, nil
			},
		},
	}
	c := testController(effects)

	out, step, cmd := c.Begin(host{}, commands.FocusSession, Capture(row(11, 21)), Origin{})
	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
	}
	if cmd != nil {
		t.Fatal("a nil Stamp seam invented a command")
	}
	if len(out.ran) != 1 {
		t.Fatalf("ran = %+v, want one dispatch", out.ran)
	}
}
