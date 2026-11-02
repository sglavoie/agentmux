package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestHandoffPreviewCLIWithoutTargetOrBackend(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Claude)
	sessionCommandsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Claude: sessionCommandReader{harness: harness.Claude}}
	}
	sessionCommandsNewKitty = func() kitty.KittyClient { t.Fatal("preview contacted Kitty"); return nil }
	runner := &sessionCommandHandoff{result: handoff.Result{Content: "Reviewed context\n", SourceHarness: meta.Harness, SourceSessionID: meta.ID, Directory: meta.CWD}}
	sessionCommandsNewHandoff = func() handoff.Runner { return runner }
	if err := sessionHandoffCmd.ParseFlags([]string{"--preview", "--json", "--", meta.Harness + ":" + meta.ID}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionHandoffCmd.SetOut(&out)
	if err := sessionHandoffCmd.RunE(sessionHandoffCmd, sessionHandoffCmd.Flags().Args()); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Data sessionActionData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Changed || payload.Data.Data["content"] != "Reviewed context\n" || payload.Data.Data["source_session_id"] != meta.ID || len(runner.requests) != 1 || !runner.requests[0].Preview {
		t.Fatalf("preview = %s, %+v", out.String(), runner.requests)
	}
	_ = sessionHandoffCmd.Flags().Set("json", "false")
	out.Reset()
	if err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{meta.ID}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Reviewed context\n" {
		t.Fatalf("text preview = %q", out.String())
	}
}

func TestHandoffReviewedFileAndInstructionReachWorkflow(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Claude)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "target", Harness: harness.Copilot}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Claude: sessionCommandReader{harness: harness.Claude}}
	}
	runner := &sessionCommandHandoff{}
	sessionCommandsNewHandoff = func() handoff.Runner { return runner }
	content := "My edited context; $(never executed)\nUnicode: café\n"
	path := filepath.Join(t.TempDir(), "context.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for flag, value := range map[string]string{"content-file": path, "instruction": "Review this for races", "profile": "target", "no-launch": "true", "json": "true"} {
		_ = sessionHandoffCmd.Flags().Set(flag, value)
	}
	var out bytes.Buffer
	sessionHandoffCmd.SetOut(&out)
	if err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{meta.ID}); err != nil {
		t.Fatal(err)
	}
	if len(runner.requests) != 1 || string(runner.requests[0].Content) != content || runner.requests[0].Instruction != "Review this for races" {
		t.Fatalf("requests = %+v", runner.requests)
	}
}

func TestReviewedFileRejectsInvalidInputsAndConflictingOptions(t *testing.T) {
	for _, content := range []string{"", " \n", string([]byte{0xff}), strings.Repeat("a", handoff.MaxReviewedBytes+1)} {
		path := filepath.Join(t.TempDir(), "context.md")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readReviewedHandoff(path); err == nil {
			t.Fatal("invalid content accepted")
		}
	}
	resetSessionCommandsTestState(t)
	_ = sessionHandoffCmd.Flags().Set("preview", "true")
	_ = sessionHandoffCmd.Flags().Set("instruction", "Review")
	_ = sessionHandoffCmd.Flags().Set("json", "true")
	var out bytes.Buffer
	sessionHandoffCmd.SetOut(&out)
	assertSessionCommandJSONError(t, sessionHandoffCmd, []string{"claude:one"}, &out, "invalid_options")
	var payload cli.JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
}
