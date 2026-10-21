package session

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// handoff takes the same selector forms as continue, fork, and show,
// including the two-argument <harness> <session-id>.
func TestSessionHandoffAcceptsTheTwoArgumentSelector(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Claude)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Claude: sessionCommandReader{harness: harness.Claude}}
	}
	runner := &sessionCommandHandoff{result: handoff.Result{
		Artifact:      handoff.Artifact{ID: "handoff-1", Path: "/tmp/handoff-1.md", Status: handoff.StatusFinal},
		TargetProfile: "target", TargetHarness: harness.Codex,
	}}
	sessionCommandsNewHandoff = func() handoff.Runner { return runner }
	for flag, value := range map[string]string{"profile": "target", "no-launch": "true"} {
		if err := sessionHandoffCmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	sessionHandoffCmd.SetOut(&out)
	if err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{harness.Claude, meta.ID}); err != nil {
		t.Fatalf("session handoff claude <id>: %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("handoff requests = %+v", runner.requests)
	}
	if !strings.Contains(sessionHandoffCmd.Use, "<harness> <session-id>") {
		t.Fatalf("handoff usage does not name the two-argument form: %q", sessionHandoffCmd.Use)
	}
}

// A conversation that is not indexed is reported in the same words by every
// command that takes one.
func TestConversationNotFoundReadsTheSameEverywhere(t *testing.T) {
	resetSessionCommandsTestState(t)
	_, indexPath := sessionCommandMeta(t, harness.Claude)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Claude: sessionCommandReader{harness: harness.Claude}}
	}
	_ = sessionHandoffCmd.Flags().Set("profile", "work")
	want := "indexed conversation claude:0123456789abcdef was not found; run `amx session list` to see the indexed conversations"
	for _, command := range []*cobra.Command{sessionContinueCmd, sessionShowCmd, sessionForkCmd, sessionForkWorktreeCmd, sessionHandoffCmd} {
		command.SetOut(&bytes.Buffer{})
		err := command.RunE(command, []string{"claude", "0123456789abcdef"})
		if err == nil || err.Error() != want {
			t.Errorf("session %s: %v, want %q", command.Name(), err, want)
		}
	}
}
