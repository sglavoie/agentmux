package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

type sessionCommandKitty struct {
	windows  []kitty.OSWindow
	launched []kitty.LaunchOpts
	focuses  []string
}

func resetSessionCommandsTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := sessionCommandsLoadConfig
	oldLoadIndex := sessionCommandsLoadSessionIndex
	oldIndexPath := sessionCommandsSessionIndexPath
	oldReaders := sessionCommandsSessionReaders
	oldNewKitty := sessionCommandsNewKitty
	oldDaemon := sessionCommandsDaemon
	oldNewHandoff := sessionCommandsNewHandoff
	oldNewWorktree := sessionCommandsNewWorktree
	oldListOut := sessionListCmd.OutOrStdout()
	oldContinueOut := sessionContinueCmd.OutOrStdout()
	oldForkOut := sessionForkCmd.OutOrStdout()
	oldForkWorktreeOut := sessionForkWorktreeCmd.OutOrStdout()
	oldHandoffOut := sessionHandoffCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionCommandsLoadConfig = oldLoadConfig
		sessionCommandsLoadSessionIndex = oldLoadIndex
		sessionCommandsSessionIndexPath = oldIndexPath
		sessionCommandsSessionReaders = oldReaders
		sessionCommandsNewKitty = oldNewKitty
		sessionCommandsDaemon = oldDaemon
		sessionCommandsNewHandoff = oldNewHandoff
		sessionCommandsNewWorktree = oldNewWorktree
		sessionListCmd.SetOut(oldListOut)
		sessionContinueCmd.SetOut(oldContinueOut)
		sessionForkCmd.SetOut(oldForkOut)
		sessionForkWorktreeCmd.SetOut(oldForkWorktreeOut)
		sessionHandoffCmd.SetOut(oldHandoffOut)
		_ = sessionListCmd.Flags().Set("json", "false")
		_ = sessionContinueCmd.Flags().Set("json", "false")
		_ = sessionForkCmd.Flags().Set("json", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("allow-dirty", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("yes", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("json", "false")
		_ = sessionHandoffCmd.Flags().Set("profile", "")
		_ = sessionHandoffCmd.Flags().Set("edit", "false")
		_ = sessionHandoffCmd.Flags().Set("no-launch", "false")
		_ = sessionHandoffCmd.Flags().Set("json", "false")
	})
}

func (c *sessionCommandKitty) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }
func (c *sessionCommandKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	return nil
}
func (*sessionCommandKitty) LaunchOverlay(kitty.LaunchOpts) error                     { return nil }
func (*sessionCommandKitty) CloseTab(string) error                                    { return nil }
func (*sessionCommandKitty) MoveTab(string, int) error                                { return nil }
func (*sessionCommandKitty) SetTabColor(string, string, string, string, string) error { return nil }
func (*sessionCommandKitty) SetTabTitle(string, string) error                         { return nil }
func (*sessionCommandKitty) SetUserVars(string, map[string]string) error              { return nil }
func (c *sessionCommandKitty) FocusTab(match string) error {
	c.focuses = append(c.focuses, match)
	return nil
}
func (*sessionCommandKitty) FocusWindow(string) error               { return nil }
func (*sessionCommandKitty) GetText(string, string) (string, error) { return "", nil }
func (*sessionCommandKitty) LaunchOverlayPager(string) error        { return nil }

type sessionCommandReader struct {
	harness     string
	discovered  []transcript.SessionMeta
	discoverErr error
	calls       *int
}

func (r sessionCommandReader) Harness() string { return r.harness }
func (r sessionCommandReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	if r.calls != nil {
		(*r.calls)++
	}
	return r.discovered, r.discoverErr
}
func (sessionCommandReader) ReadMessages(string) ([]transcript.Message, error) { return nil, nil }

type sessionCommandHandoff struct {
	requests []handoff.Request
	result   handoff.Result
	err      error
}

type sessionCommandWorktree struct {
	requests []worktree.Request
	result   worktree.Result
}

func (w *sessionCommandWorktree) Create(_ context.Context, request worktree.Request) (worktree.Result, error) {
	w.requests = append(w.requests, request)
	return w.result, nil
}

func (*sessionCommandWorktree) RemoveUnused(context.Context, worktree.Result) error { return nil }

func (h *sessionCommandHandoff) Run(_ context.Context, request handoff.Request) (handoff.Result, error) {
	h.requests = append(h.requests, request)
	return h.result, h.err
}

func TestSessionListRefreshesFilesystemIndexAndEmitsTextAndJSON(t *testing.T) {
	resetSessionCommandsTestState(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	source := filepath.Join(dir, "fresh.jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{
		ID: "fresh", Harness: harness.Codex, CWD: dir, Path: source,
		FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
	}
	indexPath := filepath.Join(dir, "transcripts.json")
	discoverCalls := 0
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: dir}}}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{
			harness: harness.Codex, discovered: []transcript.SessionMeta{meta}, calls: &discoverCalls,
		}}
	}

	if err := sessionListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionListCmd.SetOut(&out)
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("JSON list: %v", err)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          sessionListData
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || len(payload.Data.Sessions) != 1 || payload.Data.Sessions[0].ID != meta.ID {
		t.Fatalf("payload = %+v", payload)
	}
	persisted, err := transcript.LoadIndexAt(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := persisted.Get(transcript.IndexKey(meta.Harness, meta.ID)); !found || discoverCalls != 1 {
		t.Fatalf("filesystem refresh found=%t calls=%d", found, discoverCalls)
	}

	if err := sessionListCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("text list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "codex\twork\t") || !strings.Contains(got, "\tfresh\n") {
		t.Fatalf("text list = %q", got)
	}
	assertNoRetiredDatabaseState(t, home, "session list")
}

func TestSessionContinueCLIUsesExactNativeInvocation(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	client := &sessionCommandKitty{}
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Defaults: config.Defaults{Args: []string{"--model", "test"}},
			Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Profiles: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work", Args: []string{"--project-flag"}}},
		}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	info, err := os.Stat(meta.Path)
	if err != nil {
		t.Fatal(err)
	}
	meta.FileSize, meta.FileModTime = info.Size(), info.ModTime()
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	discoverCalls := 0
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{
			harness: harness.Codex, discovered: []transcript.SessionMeta{meta}, calls: &discoverCalls,
		}}
	}
	sessionCommandsNewKitty = func() kitty.KittyClient { return client }
	sessionCommandsDaemon = func(daemon.Request) (daemon.Response, error) { return daemon.Response{OK: true}, nil }
	if err := sessionContinueCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionContinueCmd.SetOut(&out)

	if err := sessionContinueCmd.RunE(sessionContinueCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("session continue: %v", err)
	}
	if len(client.launched) != 1 {
		t.Fatalf("launches = %+v, want one", client.launched)
	}
	opts := client.launched[0]
	if opts.Directory != meta.CWD || opts.UserVars["amx_harness"] != harness.Codex || opts.UserVars["amx_profile"] != "work" {
		t.Fatalf("launch context = %+v, want indexed source context", opts)
	}
	if got := strings.Join(opts.Cmdline, " "); !strings.Contains(got, "codex --project-flag resume session-1") {
		t.Fatalf("command = %q, want Codex resume argv", got)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          sessionActionData
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || payload.Data.Action != "session.continue" || payload.Data.Data["continuation"] != "resumed" {
		t.Fatalf("payload = %+v", payload)
	}
	if discoverCalls != 1 {
		t.Fatalf("conversation resolution refresh calls = %d, want 1", discoverCalls)
	}
	if err := sessionContinueCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := sessionContinueCmd.RunE(sessionContinueCmd, []string{harness.Codex, meta.ID}); err != nil {
		t.Fatalf("text session continue: %v", err)
	}
	if got := out.String(); got != "resumed indexed conversation\n" {
		t.Fatalf("text output = %q", got)
	}
}

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
	if payload.SchemaVersion != JSONSchemaVersion || payload.Data.Data["continuation"] != "portable_fork" || payload.Data.Data["fork_kind"] != "portable" {
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
	if payload.SchemaVersion != JSONSchemaVersion || payload.Data.Action != actions.ForkWorktree || payload.Data.Data["fork_kind"] != "isolated" {
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
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet || exitErr.Code != 1 {
		t.Fatalf("partial handoff error = %#v, want quiet nonzero ExitError", err)
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
	var payload JSONErrorEnvelope
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
	assertSessionCommandJSONError(t, sessionHandoffCmd, []string{"not-a-conversation"}, &out, "invalid_selector")
}

func assertSessionCommandJSONError(t *testing.T, command *cobra.Command, args []string, out *bytes.Buffer, wantCode string) {
	t.Helper()
	out.Reset()
	err := command.RunE(command, args)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("error = %#v, want quiet structured ExitError", err)
	}
	var payload JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || payload.Error.Code != wantCode {
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
		entries[transcript.IndexKey(meta.Harness, meta.ID)] = meta
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
