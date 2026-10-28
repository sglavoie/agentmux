package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

func TestSessionForkCLIUsesPortableCopilotHandoff(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Copilot)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Copilot}}, Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work"}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Copilot: sessionCommandReader{harness: harness.Copilot}}
	}
	sessionCommandsNewKitty = func() kitty.KittyClient { return &sessionCommandKitty{} }
	runner := &sessionCommandHandoff{result: handoff.Result{
		Artifact:      handoff.Artifact{ID: "handoff-1", Path: "/tmp/handoff-1.md", Status: handoff.StatusFinal},
		TargetProfile: "work", TargetHarness: harness.Copilot, Launched: true, Label: "Portable fork",
	}}
	sessionCommandsNewHandoff = func() handoff.Runner { return runner }
	if err := sessionForkCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionForkCmd.SetOut(&out)
	if err := sessionForkCmd.RunE(sessionForkCmd, []string{harness.Copilot, meta.ID}); err != nil {
		t.Fatalf("session fork: %v", err)
	}
	var payload struct {
		SchemaVersion int               `json:"schema_version"`
		Data          sessionActionData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || payload.Data.Data["continuation"] != "portable_fork" || payload.Data.Data["fork_kind"] != "portable" {
		t.Fatalf("payload = %+v", payload)
	}
	if len(runner.requests) != 1 || runner.requests[0].TargetProfile != "work" {
		t.Fatalf("portable handoff requests = %+v", runner.requests)
	}
	if err := sessionForkCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionForkCmd.RunE(sessionForkCmd, []string{harness.Copilot, meta.ID}); err != nil {
		t.Fatalf("text session fork: %v", err)
	}
	if got := out.String(); got != "started portable fork from finalized handoff pack\n" {
		t.Fatalf("text output = %q", got)
	}
}

func TestSessionForkWorktreeCLIRequiresConfirmationAndPassesDirtyOverride(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}, Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work"}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex}}
	}
	client := &sessionCommandKitty{}
	sessionCommandsNewKitty = func() kitty.KittyClient { return client }
	managed := &sessionCommandWorktree{result: worktree.Result{ProjectLabel: "api", Path: t.TempDir(), Branch: "amx/api/source-1", BaseRef: "HEAD", SourceRoot: meta.CWD}}
	sessionCommandsNewWorktree = func() worktree.Runner { return managed }
	var out bytes.Buffer
	sessionForkWorktreeCmd.SetOut(&out)

	err := sessionForkWorktreeCmd.RunE(sessionForkWorktreeCmd, []string{harness.Codex, meta.ID})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) || actionErr.Code != "confirmation_required" {
		t.Fatalf("unconfirmed fork-worktree error = %v, want confirmation_required", err)
	}
	if err := sessionForkWorktreeCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := sessionForkWorktreeCmd.Flags().Set("allow-dirty", "true"); err != nil {
		t.Fatal(err)
	}
	if err := sessionForkWorktreeCmd.RunE(sessionForkWorktreeCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("confirmed fork-worktree: %v", err)
	}
	if len(managed.requests) != 1 || !managed.requests[0].AllowDirty {
		t.Fatalf("worktree requests = %+v, want confirmed dirty override", managed.requests)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != managed.result.Path {
		t.Fatalf("launches = %+v, want isolated directory %q", client.launched, managed.result.Path)
	}
	if got := out.String(); got != "forked indexed conversation in isolated worktree\n" {
		t.Fatalf("text output = %q", got)
	}
	if err := sessionForkWorktreeCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionForkWorktreeCmd.RunE(sessionForkWorktreeCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("JSON fork-worktree: %v", err)
	}
	var payload struct {
		SchemaVersion int               `json:"schema_version"`
		Data          sessionActionData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || payload.Data.Action != actions.ForkWorktree || payload.Data.Data["fork_kind"] != "isolated" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSessionHandoffCLICreateOnlyAndPartialJSON(t *testing.T) {
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
	if err := sessionHandoffCmd.Flags().Set("profile", "target"); err != nil {
		t.Fatal(err)
	}
	if err := sessionHandoffCmd.Flags().Set("no-launch", "true"); err != nil {
		t.Fatal(err)
	}
	if err := sessionHandoffCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionHandoffCmd.SetOut(&out)
	if err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{harness.Claude + ":" + meta.ID}); err != nil {
		t.Fatalf("session handoff: %v", err)
	}
	var payload struct {
		SchemaVersion int               `json:"schema_version"`
		Data          sessionActionData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Data.Action != actions.Handoff || payload.Data.Data["path"] != "/tmp/handoff-1.md" || payload.Data.Data["launched"] != false {
		t.Fatalf("payload = %+v", payload)
	}
	if len(runner.requests) != 1 || !runner.requests[0].NoLaunch || runner.requests[0].TargetProfile != "target" {
		t.Fatalf("handoff request = %+v", runner.requests)
	}
	if err := sessionHandoffCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{harness.Claude + ":" + meta.ID}); err != nil {
		t.Fatalf("text session handoff: %v", err)
	}
	if got := out.String(); got != "finalized handoff pack: /tmp/handoff-1.md\n" {
		t.Fatalf("text output = %q", got)
	}

	runner.err = &handoff.PartialLaunchError{Err: errors.New("Kitty unavailable")}
	runner.result.Launched = false
	if err := sessionHandoffCmd.Flags().Set("no-launch", "false"); err != nil {
		t.Fatal(err)
	}
	if err := sessionHandoffCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{harness.Claude + ":" + meta.ID})
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet || exitErr.Code != 1 {
		t.Fatalf("partial handoff error = %#v, want quiet nonzero cli.ExitError", err)
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("partial JSON = %v\n%s", err, out.String())
	}
	if payload.Data.Data["path"] != "/tmp/handoff-1.md" || payload.Data.Message != "handoff pack finalized at /tmp/handoff-1.md; target launch failed" {
		t.Fatalf("partial payload = %+v", payload)
	}
}

func TestSessionHandoffCLIRejectsEditJSON(t *testing.T) {
	resetSessionCommandsTestState(t)
	if err := sessionHandoffCmd.Flags().Set("profile", "work"); err != nil {
		t.Fatal(err)
	}
	if err := sessionHandoffCmd.Flags().Set("edit", "true"); err != nil {
		t.Fatal(err)
	}
	if err := sessionHandoffCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionHandoffCmd.SetOut(&out)
	err := sessionHandoffCmd.RunE(sessionHandoffCmd, []string{"claude:source-1"})
	if err == nil {
		t.Fatal("--edit --json succeeded")
	}
	var payload cli.JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Error.Code != "invalid_options" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSessionCLIRejectsHiddenMissingAndInvalidSelectors(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	meta.HiddenFromIndex = true
	writeSessionCommandIndex(t, indexPath, meta)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex}}
	}
	if err := sessionContinueCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionContinueCmd.SetOut(&out)

	assertSessionCommandJSONError(t, sessionContinueCmd, []string{harness.Codex, meta.ID}, &out, "not_found")
	assertSessionCommandJSONError(t, sessionContinueCmd, []string{harness.Codex, "missing"}, &out, "not_found")
	assertSessionCommandJSONError(t, sessionContinueCmd, []string{harness.Codex}, &out, "missing_parameter")
	assertSessionCommandJSONError(t, sessionContinueCmd, []string{harness.Codex, meta.ID, "extra"}, &out, "invalid_arguments")

	if err := sessionHandoffCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := sessionHandoffCmd.Flags().Set("profile", "work"); err != nil {
		t.Fatal(err)
	}
	sessionHandoffCmd.SetOut(&out)
	assertSessionCommandJSONError(t, sessionHandoffCmd, []string{"not-a-conversation"}, &out, "not_found")
	assertSessionCommandJSONError(t, sessionHandoffCmd, []string{"codex:"}, &out, "invalid_selector")
}

func assertSessionCommandJSONError(t *testing.T, command *cobra.Command, args []string, out *bytes.Buffer, wantCode string) {
	t.Helper()
	out.Reset()
	err := command.RunE(command, args)
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("error = %#v, want quiet structured cli.ExitError", err)
	}
	var payload cli.JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != cli.JSONSchemaVersion || payload.Error.Code != wantCode {
		t.Fatalf("payload = %+v, want code %q", payload, wantCode)
	}
}

func sessionCommandMeta(t *testing.T, harnessName string) (transcript.SessionMeta, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{ID: "session-1", Harness: harnessName, Profile: "work", Project: "api", CWD: dir, Path: source}
	indexPath := filepath.Join(dir, "transcripts.json")
	writeSessionCommandIndex(t, indexPath, meta)
	return meta, indexPath
}

func writeSessionCommandIndex(t *testing.T, indexPath string, metas ...transcript.SessionMeta) {
	t.Helper()
	entries := make(map[string]transcript.SessionMeta, len(metas))
	for _, meta := range metas {
		entries[transcript.IndexKey(meta.Harness, meta.ID)] = withMainClass(meta)
	}
	body, err := json.Marshal(struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A selector that cannot be a session ID is most likely a word remembered from
// the conversation, so the miss points at search with that word rather than at
// the whole list — unless it is a configured project's title, when the miss
// points at that project's conversations.
func TestSessionCLIPointsAWordSelectorAtSearch(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	writeSessionCommandIndex(t, indexPath, meta)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}},
			Projects: []config.Session{{Title: "🐱amx", Profile: "work"}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex}}
	}
	if err := sessionContinueCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionContinueCmd.SetOut(&out)

	for _, tc := range []struct {
		selector string
		want     string
	}{
		{"kitty", "was not found; to find a conversation by what was said in it, run `amx session search kitty`"},
		{"tab title", "`amx session search 'tab title'`"},
		{"deadbeef", "run `amx session list` to see the indexed conversations"},
		{"abc", "a session ID prefix needs at least 4 characters"},
		{"amx", "🐱amx is a configured project, not a conversation; `amx session list --project amx` lists its conversations by ID"},
		{"🐱amx", "`amx session list --project '🐱amx'`"},
	} {
		assertSessionCommandJSONError(t, sessionContinueCmd, []string{tc.selector}, &out, "not_found")
		var payload cli.JSONErrorEnvelope
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Error.Message, tc.want) {
			t.Errorf("selector %q: message = %q, want it to contain %q", tc.selector, payload.Error.Message, tc.want)
		}
	}
}
