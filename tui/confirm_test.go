package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// TestRenderConfirmClose locks in the two-line layout for the close prompt:
// line 1 carries the question, line 2 carries the explicit consequence.
func TestRenderConfirmClose(t *testing.T) {
	t.Parallel()

	m := Model{
		mode:               ModeConfirm,
		confirmAction:      "close",
		confirmConsequence: wantCloseConsequence,
		targetSession:      session.RunningSession{Base: "api"},
	}
	out := stripAnsi(renderConfirm(m))
	want1 := `End session "api"?`
	want2 := wantCloseConsequence
	if !strings.Contains(out, want1) {
		t.Errorf("expected line 1 %q in output; got %q", want1, out)
	}
	if !strings.Contains(out, want2) {
		t.Errorf("expected consequence %q in output; got %q", want2, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines; got %d (%q)", len(lines), out)
	}
}

// TestRenderConfirmRestart mirrors the close test for the restart variant, with
// the difference that its consequence is long enough to wrap. The whole sentence
// must survive: a warning cut in half is a different warning, and the operator
// answers y or n to whichever one they read.
func TestRenderConfirmRestart(t *testing.T) {
	t.Parallel()

	m := Model{
		mode:               ModeConfirm,
		confirmAction:      "restart",
		confirmConsequence: wantRestartConsequence,
		targetSession:      session.RunningSession{Base: "dotfiles"},
	}
	out := stripAnsi(renderConfirm(m))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want1 := `Restart "dotfiles"?`
	if lines[0] != want1 {
		t.Errorf("expected line 1 %q in output; got %q", want1, out)
	}
	if len(lines) < 3 {
		t.Fatalf("expected the consequence to wrap onto more than one line; got %q", out)
	}
	if got := strings.Join(lines[1:], " "); got != wantRestartConsequence {
		t.Errorf("wrapped consequence reassembles to %q, want %q", got, wantRestartConsequence)
	}
	if strings.Contains(out, "…") {
		t.Errorf("the consequence was truncated rather than wrapped; got %q", out)
	}
}

// TestRenderConfirmNoConsequence verifies that an unset consequence collapses
// the layout to a single line (defensive: keeps callers free to omit the
// consequence rather than passing a placeholder).
func TestRenderConfirmNoConsequence(t *testing.T) {
	t.Parallel()

	m := Model{
		mode:          ModeConfirm,
		confirmAction: "close",
		targetSession: session.RunningSession{Base: "x"},
	}
	out := stripAnsi(renderConfirm(m))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line when consequence is empty; got %d (%q)", len(lines), out)
	}
}
