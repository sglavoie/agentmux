package planrun

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeASL answers by subcommand and records every argument list it was given.
type fakeASL struct {
	answers map[string]string
	errs    map[string]error
	calls   [][]string
}

func (f *fakeASL) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return []byte(f.answers[args[0]]), f.errs[args[0]]
}

func (f *fakeASL) call(subcommand string) []string {
	for _, call := range f.calls {
		if call[0] == subcommand {
			return call
		}
	}
	return nil
}

const (
	statusPending  = `{"ok":true,"counts":{"pending":3,"complete":2},"lock":{"active":false},"next_command":{"complete":false}}`
	statusRunning  = `{"ok":true,"counts":{"pending":3,"complete":2},"lock":{"active":true},"next_command":{"complete":false}}`
	statusComplete = `{"ok":true,"counts":{"pending":0,"complete":5},"lock":{"active":false},"next_command":{"complete":true}}`
	preflightGreen = `{"ok":true,"findings":[{"severity":"green","category":"env-amx","message":"hooks available"}]}`
	preflightRed   = `{"ok":false,"error":"1 red, 1 yellow finding(s)","findings":[
		{"severity":"green","category":"env-amx","message":"hooks available"},
		{"severity":"red","category":"plan-structure","message":"session-3.md is missing","path":"/plans/p/session-3.md","session_id":"3"},
		{"severity":"yellow","category":"manifest-readiness","message":"plan readiness state is 'reviewed'","detail":"run preflight to advance"}]}`
)

func TestCheckReportsWhatASLSays(t *testing.T) {
	for _, test := range []struct {
		name, status, preflight string
		preflightErr            error
		state                   State
		findings                int
		preflightAsked          bool
	}{
		{name: "ready", status: statusPending, preflight: preflightGreen, state: StateReady, preflightAsked: true},
		{name: "never run", status: `not json`, preflight: preflightGreen, state: StateReady, preflightAsked: true},
		{name: "not ready", status: statusPending, preflight: preflightRed, preflightErr: errors.New("exit status 1"), state: StateNotReady, findings: 2, preflightAsked: true},
		{name: "refused with nothing to show", status: statusPending, preflight: `{"ok":false,"error":"plan is locked"}`, state: StateNotReady, findings: 1, preflightAsked: true},
		{name: "running", status: statusRunning, state: StateRunning},
		{name: "complete", status: statusComplete, state: StateComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			asl := &fakeASL{answers: map[string]string{"status": test.status, "preflight": test.preflight}, errs: map[string]error{"preflight": test.preflightErr}}
			readiness, err := Check(context.Background(), asl, "/plans/p", "/work", Options{})
			if err != nil {
				t.Fatal(err)
			}
			if readiness.State != test.state || readiness.Ready() != (test.state == StateReady) {
				t.Fatalf("state = %q ready = %t, want %q", readiness.State, readiness.Ready(), test.state)
			}
			if len(readiness.Findings) != test.findings {
				t.Fatalf("findings = %+v, want %d", readiness.Findings, test.findings)
			}
			for _, finding := range readiness.Findings {
				if finding.Severity == "green" {
					t.Fatalf("a green finding was reported: %+v", finding)
				}
			}
			if asked := asl.call("preflight") != nil; asked != test.preflightAsked {
				t.Fatalf("preflight asked = %t, want %t", asked, test.preflightAsked)
			}
		})
	}
}

func TestCheckPassesTheRunOptionsToPreflight(t *testing.T) {
	asl := &fakeASL{answers: map[string]string{"status": statusPending, "preflight": preflightGreen}}
	if _, err := Check(context.Background(), asl, "/plans/p", "/work", Options{Harness: "codex", ReviewHarness: "claude", CommitMode: CommitAtEnd}); err != nil {
		t.Fatal(err)
	}
	want := []string{"preflight", "/plans/p", "--json", "--cwd", "/work", "--harness", "codex", "--commit-at-end"}
	if got := asl.call("preflight"); !slices.Equal(got, want) {
		t.Fatalf("preflight args = %q, want %q", got, want)
	}
}

func TestCheckFailsWhenPreflightGivesNoAnswer(t *testing.T) {
	for _, answer := range []string{"", "usage: asl", `{"findings":[]}`} {
		asl := &fakeASL{answers: map[string]string{"status": statusPending, "preflight": answer}, errs: map[string]error{"preflight": errors.New("exit status 2")}}
		if _, err := Check(context.Background(), asl, "/plans/p", "", Options{}); err == nil {
			t.Fatalf("answer %q read as a readiness", answer)
		}
	}
}

func TestPromptIsTheHarnessInvocationWithTheRunArguments(t *testing.T) {
	guide := func(harness, invocation string) string {
		return `{"skills":[{"skill":"asl","invocations":{"` + harness + `":"` + invocation + `"}}]}`
	}
	for _, test := range []struct {
		harness, invocation, planDir, want string
		opts                               Options
	}{
		{harness: "claude", invocation: "/asl asl run PLAN_DIR", planDir: "/plans/p", want: "/asl asl run /plans/p"},
		{harness: "codex", invocation: "$asl asl run PLAN_DIR", planDir: "/plans/p", opts: Options{Harness: "codex", ReviewHarness: "claude", CommitMode: CommitPerSession},
			want: "$asl asl run /plans/p --harness codex --review-harness claude --commit-per-session"},
		{harness: "claude", invocation: "/asl asl run PLAN_DIR", planDir: "/plans/it's here", want: `/asl asl run '/plans/it'\''s here'`},
	} {
		asl := &fakeASL{answers: map[string]string{"supervisor-guide": guide(test.harness, test.invocation)}}
		got, err := Prompt(context.Background(), asl, test.harness, test.planDir, test.opts)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("prompt = %q, want %q", got, test.want)
		}
		if call := asl.call("supervisor-guide"); !slices.Equal(call, []string{"supervisor-guide", "--harness", test.harness, "--json"}) {
			t.Fatalf("guide args = %q", call)
		}
	}
}

func TestPromptRefusesAGuideThatNamesNoInvocation(t *testing.T) {
	for _, answer := range []string{`{"skills":[]}`, `{"skills":[{"invocations":{"codex":"$asl asl run PLAN_DIR"}}]}`, `{"skills":[{"invocations":{"claude":"/asl supervise"}}]}`, `nope`} {
		asl := &fakeASL{answers: map[string]string{"supervisor-guide": answer}}
		if prompt, err := Prompt(context.Background(), asl, "claude", "/plans/p", Options{}); err == nil {
			t.Fatalf("guide %q produced prompt %q", answer, prompt)
		}
	}
}

func TestOptionsValidate(t *testing.T) {
	for _, test := range []struct {
		opts Options
		want string
	}{
		{opts: Options{}},
		{opts: Options{Harness: "codex", ReviewHarness: "claude", CommitMode: CommitAtEnd}},
		{opts: Options{ReviewHarness: "codex"}},
		{opts: Options{Harness: "gemini"}, want: "unknown harness"},
		{opts: Options{ReviewHarness: "gemini"}, want: "unknown review harness"},
		{opts: Options{Harness: "codex", ReviewHarness: "codex"}, want: "must differ"},
		{opts: Options{CommitMode: "sometimes"}, want: "unknown commit mode"},
	} {
		err := test.opts.Validate()
		if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
			t.Fatalf("Validate(%+v) = %v, want %q", test.opts, err, test.want)
		}
	}
}

func TestSummaryCountsFindingsBySeverity(t *testing.T) {
	readiness := Readiness{State: StateNotReady, Findings: []Finding{{Severity: "red"}, {Severity: "yellow"}, {Severity: "yellow"}}}
	if got := readiness.Summary(); !strings.Contains(got, "1 red and 2 yellow") {
		t.Fatalf("summary = %q", got)
	}
}
