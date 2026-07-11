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
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

type sessionCommandKitty struct {
	windows  []kitty.OSWindow
	launched []kitty.LaunchOpts
	focuses  []string
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

type sessionCommandReader struct{ harness string }

func (r sessionCommandReader) Harness() string { return r.harness }
func (sessionCommandReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return nil, nil
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

func TestSessionContinueCLIUsesExactNativeInvocation(t *testing.T) {
	resetProjectCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	client := &sessionCommandKitty{}
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Defaults: config.Defaults{Args: []string{"--model", "test"}},
			Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: "/tmp/codex-home"}},
			Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work", Args: []string{"--project-flag"}}},
		}, nil
	}
	projectCommandsSessionIndexPath = func() string { return indexPath }
	projectCommandsLoadSessionIndex = func(path string, _ map[string]transcript.Reader) (*transcript.Index, error) {
		return transcript.LoadIndexAt(path)
	}
	projectCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex}}
	}
	projectCommandsNewKitty = func() kitty.KittyClient { return client }
	projectCommandsDaemon = func(daemon.Request) (daemon.Response, error) { return daemon.Response{OK: true}, nil }
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
}

func TestSessionLaunchAndFocusCLIUseWorkspaceAndLiveTabSelectors(t *testing.T) {
	resetProjectCommandsTestState(t)
	projectDir := t.TempDir()
	client := &sessionCommandKitty{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
		ID: 73,
		Windows: []kitty.Window{{
			ID: 8, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude, Project: "api"},
		}},
	}}}}}
	store := datastore.OpenTest(t)
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{
			Accounts: []config.Account{{Name: "work", Harness: harness.Claude}},
			Projects: []config.Session{{ID: "api", Title: "api", Directory: projectDir, Profile: "work"}},
		}, nil
	}
	projectCommandsOpenStore = func(context.Context) (*datastore.Store, error) { return store, nil }
	projectCommandsNewKitty = func() kitty.KittyClient { return client }

	if err := sessionLaunchCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var launchOut bytes.Buffer
	sessionLaunchCmd.SetOut(&launchOut)
	if err := sessionLaunchCmd.RunE(sessionLaunchCmd, []string{"api"}); err != nil {
		t.Fatalf("session launch: %v", err)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != projectDir {
		t.Fatalf("launches = %+v, want workspace default at %q", client.launched, projectDir)
	}
	client.focuses = nil
	if err := sessionFocusCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var focusOut bytes.Buffer
	sessionFocusCmd.SetOut(&focusOut)
	if err := sessionFocusCmd.RunE(sessionFocusCmd, []string{"73"}); err != nil {
		t.Fatalf("session focus: %v", err)
	}
	if got := client.focuses; len(got) != 1 || got[0] != "id:73" {
		t.Fatalf("focuses = %v, want [id:73]", got)
	}
}

func TestSessionForkCLIUsesPortableCopilotHandoff(t *testing.T) {
	resetProjectCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Copilot)
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Copilot}}, Projects: []config.Session{{Title: "api", Directory: meta.CWD, Profile: "work"}}}, nil
	}
	projectCommandsSessionIndexPath = func() string { return indexPath }
	projectCommandsLoadSessionIndex = func(path string, _ map[string]transcript.Reader) (*transcript.Index, error) {
		return transcript.LoadIndexAt(path)
	}
	projectCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Copilot: sessionCommandReader{harness: harness.Copilot}}
	}
	projectCommandsNewKitty = func() kitty.KittyClient { return &sessionCommandKitty{} }
	runner := &sessionCommandHandoff{result: handoff.Result{
		Artifact:      handoff.Artifact{Record: handoff.Record{ID: "handoff-1", Path: "/tmp/handoff-1.md", Status: handoff.StatusFinal}},
		TargetProfile: "work", TargetHarness: harness.Copilot, Launched: true, Label: "Portable fork",
	}}
	projectCommandsNewHandoff = func() handoff.Runner { return runner }
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
}

func TestSessionForkWorktreeCLIRequiresConfirmationAndPassesDirtyOverride(t *testing.T) {
	resetProjectCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}, Projects: []config.Session{{ID: "api", Title: "api", Directory: meta.CWD, Profile: "work"}}}, nil
	}
	projectCommandsSessionIndexPath = func() string { return indexPath }
	projectCommandsLoadSessionIndex = func(path string, _ map[string]transcript.Reader) (*transcript.Index, error) {
		return transcript.LoadIndexAt(path)
	}
	projectCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex}}
	}
	client := &sessionCommandKitty{}
	projectCommandsNewKitty = func() kitty.KittyClient { return client }
	managed := &sessionCommandWorktree{result: worktree.Result{ID: "mwt-1", WorkspaceID: "api", Path: t.TempDir(), Branch: "amx/api/source-1", SourceRoot: meta.CWD}}
	projectCommandsNewWorktree = func() worktree.Runner { return managed }

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
}

func TestSessionHandoffCLICreateOnlyAndPartialJSON(t *testing.T) {
	resetProjectCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Claude)
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}}, nil
	}
	projectCommandsSessionIndexPath = func() string { return indexPath }
	projectCommandsLoadSessionIndex = func(path string, _ map[string]transcript.Reader) (*transcript.Index, error) {
		return transcript.LoadIndexAt(path)
	}
	projectCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Claude: sessionCommandReader{harness: harness.Claude}}
	}
	runner := &sessionCommandHandoff{result: handoff.Result{
		Artifact:      handoff.Artifact{Record: handoff.Record{ID: "handoff-1", Path: "/tmp/handoff-1.md", Status: handoff.StatusFinal}},
		TargetProfile: "target", TargetHarness: harness.Codex,
	}}
	projectCommandsNewHandoff = func() handoff.Runner { return runner }
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

	runner.err = &handoff.PartialLaunchError{Err: errors.New("Kitty unavailable")}
	runner.result.Launched = false
	if err := sessionHandoffCmd.Flags().Set("no-launch", "false"); err != nil {
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
	resetProjectCommandsTestState(t)
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

func sessionCommandMeta(t *testing.T, harnessName string) (transcript.SessionMeta, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{ID: "session-1", Harness: harnessName, Profile: "work", Project: "api", CWD: dir, Path: source}
	indexPath := filepath.Join(dir, "transcripts.json")
	body, err := json.Marshal(struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}{Entries: map[string]transcript.SessionMeta{transcript.IndexKey(harnessName, meta.ID): meta}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return meta, indexPath
}
