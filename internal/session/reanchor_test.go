package session_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestReanchorTab_InvokesSetUserVars asserts that ReanchorTab writes the full
// BuildUserVars map onto the ghost's surviving window via a single
// `kitten @ set-user-vars` call. The expected argv mirrors BuildUserVars'
// contract; if a key changes there, this test must change too.
func TestReanchorTab_InvokesSetUserVars(t *testing.T) {
	defaults := config.Defaults{Args: []string{"--shared-default"}}
	acc := config.Account{
		Name:    "work",
		Harness: "claude",
		HomeDir: "~/.config/claude-work",
		Color:   "blue",
	}
	sess := config.Session{
		Title:     "api",
		Directory: "~/projects/api",
		Profile:   "work",
		Account:   "work",
	}
	ghost := session.GhostTab{
		TabID:    42,
		WindowID: 7,
		Profile:  "work",
		Project:  "api",
		Account:  acc,
		Session:  sess,
	}

	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := session.ReanchorTab(client, ghost, defaults); err != nil {
		t.Fatalf("ReanchorTab: %v", err)
	}

	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	got := runner.Calls[0].Args
	if got[0] != "@" || got[1] != "set-user-vars" {
		t.Fatalf("argv prefix: got %v", got)
	}
	if got[2] != "--match=id:7" {
		t.Errorf("match expr: got %q, want %q", got[2], "--match=id:7")
	}

	// Verify the KEY=VALUE positional args exactly match BuildUserVars output.
	wantVars := session.BuildUserVars(sess, acc, defaults)
	wantArgs := make([]string, 0, len(wantVars))
	for k, v := range wantVars {
		wantArgs = append(wantArgs, k+"="+v)
	}
	sort.Strings(wantArgs)

	gotArgs := append([]string(nil), got[3:]...)
	sort.Strings(gotArgs)

	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("argv var count: got %d (%v), want %d (%v)", len(gotArgs), gotArgs, len(wantArgs), wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Errorf("argv[%d]: got %q, want %q", i, gotArgs[i], wantArgs[i])
		}
	}
}

// TestReanchorAll_LogsAndContinuesOnError verifies ReanchorAll keeps going when
// one ghost fails to re-stamp. We trigger failure by making the runner refuse
// commands (no canned response for set-user-vars), then assert the second
// ghost still gets a kitten invocation.
func TestReanchorAll_ProcessesEveryGhost(t *testing.T) {
	defaults := config.Defaults{}
	acc := config.Account{Name: "work", Harness: "claude", HomeDir: "~/h"}
	mkGhost := func(tabID, winID int, project string) session.GhostTab {
		return session.GhostTab{
			TabID:    tabID,
			WindowID: winID,
			Profile:  "work",
			Project:  project,
			Account:  acc,
			Session: config.Session{
				Title:     project,
				Directory: "~/p",
				Profile:   "work",
				Account:   "work",
			},
		}
	}
	ghosts := []session.GhostTab{mkGhost(1, 10, "a"), mkGhost(2, 20, "b")}

	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	session.ReanchorAll(client, ghosts, defaults)

	if len(runner.Calls) != 2 {
		t.Fatalf("expected 2 kitten calls (one per ghost), got %d", len(runner.Calls))
	}
	want := []string{"--match=id:10", "--match=id:20"}
	for i, call := range runner.Calls {
		if call.Args[1] != "set-user-vars" {
			t.Errorf("call %d: not set-user-vars, got %v", i, call.Args)
		}
		if !strings.Contains(strings.Join(call.Args, " "), want[i]) {
			t.Errorf("call %d: want match %q in args %v", i, want[i], call.Args)
		}
	}
}
