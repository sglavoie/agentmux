package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

type continuityKitty struct {
	windows   []kitty.OSWindow
	listErr   error
	launchErr error
	focuses   []string
	launched  []kitty.LaunchOpts
}

func (c *continuityKitty) ListWindows() ([]kitty.OSWindow, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return c.windows, nil
}
func (c *continuityKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	return c.launchErr
}
func (c *continuityKitty) LaunchOverlay(kitty.LaunchOpts) error                     { return nil }
func (c *continuityKitty) CloseTab(string) error                                    { return nil }
func (c *continuityKitty) MoveTab(string, int) error                                { return nil }
func (c *continuityKitty) SetTabColor(string, string, string, string, string) error { return nil }
func (c *continuityKitty) SetTabTitle(string, string) error                         { return nil }
func (c *continuityKitty) SetUserVars(string, map[string]string) error              { return nil }
func (c *continuityKitty) FocusTab(match string) error {
	c.focuses = append(c.focuses, match)
	return nil
}
func (c *continuityKitty) FocusWindow(string) error               { return nil }
func (c *continuityKitty) GetText(string, string) (string, error) { return "", nil }
func (c *continuityKitty) LaunchOverlayPager(string) error        { return nil }

type continuityReader struct {
	harness string
	err     error
	reads   int
}

type continuityHandoff struct {
	requests []handoff.Request
	result   handoff.Result
	err      error
}

type continuityWorktree struct {
	requests  []worktree.Request
	created   worktree.Result
	createErr error
	removed   []worktree.Result
	removeErr error
}

func (w *continuityWorktree) Create(_ context.Context, request worktree.Request) (worktree.Result, error) {
	w.requests = append(w.requests, request)
	if w.createErr != nil {
		return worktree.Result{}, w.createErr
	}
	return w.created, nil
}

func (w *continuityWorktree) RemoveUnused(_ context.Context, result worktree.Result) error {
	w.removed = append(w.removed, result)
	return w.removeErr
}

func (h *continuityHandoff) Run(_ context.Context, request handoff.Request) (handoff.Result, error) {
	h.requests = append(h.requests, request)
	return h.result, h.err
}

func (r *continuityReader) Harness() string { return r.harness }
func (*continuityReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return nil, nil
}
func (r *continuityReader) ReadMessages(string) ([]transcript.Message, error) {
	r.reads++
	if r.err != nil {
		return nil, r.err
	}
	return nil, nil
}

func TestContinueFocusesOnlyAnExactLiveConversation(t *testing.T) {
	identity := conversation.Identity{Harness: harness.Claude, SessionID: "source-1"}
	client := &continuityKitty{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
		ID: 100,
		Windows: []kitty.Window{{
			ID: 10, IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude, Project: "api"},
		}},
	}}}}}
	reader := &continuityReader{harness: harness.Claude}
	meta := continuityMeta(t, harness.Claude, "source-1", "work")

	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Claude), Readers: map[string]transcript.Reader{harness.Claude: reader},
		Daemon: func(daemon.Request) (daemon.Response, error) {
			return daemon.Response{OK: true, States: map[int]daemon.WindowStateSnapshot{10: {Conversation: &identity}}}, nil
		},
	}, Continue, Request{Conversation: &meta})
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got := result.Data["continuation"]; got != "focused" {
		t.Fatalf("continuation = %v, want focused", got)
	}
	if got := client.focuses; len(got) != 1 || got[0] != "id:100" {
		t.Fatalf("focuses = %v, want [id:100]", got)
	}
	if len(client.launched) != 0 {
		t.Fatalf("launched = %+v, want no resume launch", client.launched)
	}
	if reader.reads != 0 {
		t.Fatalf("reader reads = %d, live focus must not validate source", reader.reads)
	}
}

func TestContinueRefusesAnAmbiguousLiveConversation(t *testing.T) {
	identity := conversation.Identity{Harness: harness.Codex, SessionID: "source-1"}
	client := &continuityKitty{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{
		{ID: 100, Windows: []kitty.Window{{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex}}}},
		{ID: 200, Windows: []kitty.Window{{ID: 20, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex}}}},
	}}}}
	reader := &continuityReader{harness: harness.Codex}
	meta := continuityMeta(t, harness.Codex, "source-1", "work")

	_, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Codex), Readers: map[string]transcript.Reader{harness.Codex: reader},
		Daemon: func(daemon.Request) (daemon.Response, error) {
			return daemon.Response{OK: true, States: map[int]daemon.WindowStateSnapshot{
				10: {Conversation: &identity}, 20: {Conversation: &identity},
			}}, nil
		},
	}, Continue, Request{Conversation: &meta})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_conversation" {
		t.Fatalf("error = %v, want ambiguous_conversation", err)
	}
	if len(client.focuses) != 0 || len(client.launched) != 0 || reader.reads != 0 {
		t.Fatalf("ambiguous continuation changed state: focuses=%v launches=%v reads=%d", client.focuses, client.launched, reader.reads)
	}
}

func TestContinueLaunchesNativeResumeForEachSupportedHarness(t *testing.T) {
	tests := []struct {
		harness string
		want    string
	}{
		{harness.Claude, "--resume source-1"},
		{harness.Codex, "resume source-1"},
		{harness.Copilot, "--resume=source-1"},
	}
	for _, tc := range tests {
		t.Run(tc.harness, func(t *testing.T) {
			client := &continuityKitty{}
			reader := &continuityReader{harness: tc.harness}
			meta := continuityMeta(t, tc.harness, "source-1", "work")
			result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
				Kitty: client, Config: continuityConfig(tc.harness), Readers: map[string]transcript.Reader{tc.harness: reader},
				Daemon: func(daemon.Request) (daemon.Response, error) { return daemon.Response{OK: true}, nil },
			}, Continue, Request{Conversation: &meta})
			if err != nil {
				t.Fatalf("Continue: %v", err)
			}
			if got := result.Data["continuation"]; got != "resumed" {
				t.Fatalf("continuation = %v, want resumed", got)
			}
			if reader.reads != 1 {
				t.Fatalf("reader reads = %d, want source validation", reader.reads)
			}
			if len(client.launched) != 1 {
				t.Fatalf("launches = %+v, want one", client.launched)
			}
			opts := client.launched[0]
			if opts.Directory != meta.CWD || opts.UserVars["amx_profile"] != "work" || opts.UserVars["amx_harness"] != tc.harness {
				t.Fatalf("launch options did not preserve source context: %+v", opts)
			}
			if got := strings.Join(opts.Cmdline, " "); !strings.Contains(got, tc.want) {
				t.Fatalf("command = %q, want native resume %q", got, tc.want)
			}
		})
	}
}

func TestForkUsesNativeSupportAndPreservesSource(t *testing.T) {
	for _, tc := range []struct {
		harness string
		want    string
	}{
		{harness.Claude, "--resume source-1 --fork-session"},
		{harness.Codex, "fork source-1"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			client := &continuityKitty{}
			reader := &continuityReader{harness: tc.harness}
			meta := continuityMeta(t, tc.harness, "source-1", "work")
			result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
				Kitty: client, Config: continuityConfig(tc.harness), Readers: map[string]transcript.Reader{tc.harness: reader},
			}, Fork, Request{Conversation: &meta})
			if err != nil {
				t.Fatalf("Fork: %v", err)
			}
			if got := result.Data["continuation"]; got != "forked" {
				t.Fatalf("continuation = %v, want forked", got)
			}
			if len(client.focuses) != 0 || len(client.launched) != 1 {
				t.Fatalf("fork changed source or failed to launch: focuses=%v launches=%+v", client.focuses, client.launched)
			}
			if got := strings.Join(client.launched[0].Cmdline, " "); !strings.Contains(got, tc.want) {
				t.Fatalf("command = %q, want native fork %q", got, tc.want)
			}
		})
	}
}

func TestForkUsesPortableHandoffForCopilot(t *testing.T) {
	client := &continuityKitty{}
	reader := &continuityReader{harness: harness.Copilot}
	meta := continuityMeta(t, harness.Copilot, "source-1", "work")
	runner := &continuityHandoff{result: handoff.Result{
		Artifact:      handoff.Artifact{ID: "handoff-1", Path: "/tmp/handoff-1.md", Status: handoff.StatusFinal},
		TargetProfile: "work", TargetHarness: harness.Copilot, Launched: true, Label: "Portable fork",
	}}
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Copilot), Readers: map[string]transcript.Reader{harness.Copilot: reader}, Handoff: runner,
	}, Fork, Request{Conversation: &meta})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if got := result.Data["continuation"]; got != "portable_fork" {
		t.Fatalf("continuation = %v, want portable_fork", got)
	}
	if got := result.Data["fork_kind"]; got != "portable" {
		t.Fatalf("fork_kind = %v, want portable", got)
	}
	if len(runner.requests) != 1 || runner.requests[0].TargetProfile != "work" || runner.requests[0].Label != "Portable fork" {
		t.Fatalf("portable handoff requests = %+v", runner.requests)
	}
	if len(client.launched) != 0 {
		t.Fatalf("portable fork bypassed injected handoff runner: %+v", client.launched)
	}
}

func TestForkWorktreeLaunchesNativeForkAndRetainsSuccessfulWorktree(t *testing.T) {
	client := &continuityKitty{}
	reader := &continuityReader{harness: harness.Codex}
	meta := continuityMeta(t, harness.Codex, "source-1", "work")
	managed := &continuityWorktree{created: worktree.Result{
		ProjectLabel: "api", Path: t.TempDir(), Branch: "amx/api/source-1", BaseRef: "HEAD", SourceRoot: t.TempDir(),
	}}
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Codex), Readers: map[string]transcript.Reader{harness.Codex: reader}, Worktree: managed,
	}, ForkWorktree, Request{Conversation: &meta, Confirmed: true})
	if err != nil {
		t.Fatalf("ForkWorktree: %v", err)
	}
	if len(managed.requests) != 1 || managed.requests[0].SourceCWD != meta.CWD || managed.requests[0].ProjectLabel != meta.Project || managed.requests[0].AllowDirty {
		t.Fatalf("worktree requests = %+v", managed.requests)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != managed.created.Path {
		t.Fatalf("launches = %+v, want isolated directory %q", client.launched, managed.created.Path)
	}
	if got := result.Data["fork_kind"]; got != "isolated" {
		t.Fatalf("fork_kind = %v, want isolated", got)
	}
	worktreeData, ok := result.Data["worktree"].(map[string]any)
	if !ok || worktreeData["project"] != "api" || worktreeData["path"] != managed.created.Path ||
		worktreeData["branch"] != managed.created.Branch || worktreeData["base_ref"] != "HEAD" ||
		worktreeData["source_root"] != managed.created.SourceRoot {
		t.Fatalf("worktree result data = %#v", result.Data["worktree"])
	}
	if _, exists := worktreeData["workspace_id"]; exists {
		t.Fatalf("worktree result retained workspace identity: %#v", worktreeData)
	}
	if len(managed.removed) != 0 {
		t.Fatalf("successful launch removed worktree: %+v", managed.removed)
	}
}

func TestForkWorktreeUsesPortableLaunchDirectoryAndCleansUpFailure(t *testing.T) {
	client := &continuityKitty{}
	reader := &continuityReader{harness: harness.Copilot}
	meta := continuityMeta(t, harness.Copilot, "source-1", "work")
	managed := &continuityWorktree{created: worktree.Result{ProjectLabel: "api", Path: t.TempDir(), Branch: "amx/api/source-1", BaseRef: "HEAD", SourceRoot: t.TempDir()}}
	runner := &continuityHandoff{result: handoff.Result{
		Artifact:      handoff.Artifact{ID: "handoff-1", Path: "/tmp/handoff-1.md", Status: handoff.StatusFinal},
		TargetProfile: "work", TargetHarness: harness.Copilot, Launched: true, Label: "Portable isolated fork",
	}}
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Copilot), Readers: map[string]transcript.Reader{harness.Copilot: reader}, Handoff: runner, Worktree: managed,
	}, ForkWorktree, Request{Conversation: &meta, Confirmed: true, Parameters: map[string]string{"allow_dirty": "true"}})
	if err != nil {
		t.Fatalf("ForkWorktree: %v", err)
	}
	if len(runner.requests) != 1 || runner.requests[0].LaunchDirectory != managed.created.Path {
		t.Fatalf("portable request = %+v, want launch directory %q", runner.requests, managed.created.Path)
	}
	if !managed.requests[0].AllowDirty || result.Data["fork_kind"] != "isolated" || len(managed.removed) != 0 {
		t.Fatalf("result=%+v requests=%+v cleanup=%+v", result, managed.requests, managed.removed)
	}

	runner.result.Launched = false
	runner.err = &handoff.PartialLaunchError{Err: errors.New("Kitty unavailable")}
	_, err = DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Copilot), Readers: map[string]transcript.Reader{harness.Copilot: reader}, Handoff: runner, Worktree: managed,
	}, ForkWorktree, Request{Conversation: &meta, Confirmed: true})
	if err == nil || len(managed.removed) != 1 {
		t.Fatalf("failed portable launch error=%v cleanup=%+v, want unused worktree cleanup", err, managed.removed)
	}
	if managed.removed[0] != managed.created {
		t.Fatalf("cleanup result = %+v, want returned worktree %+v", managed.removed[0], managed.created)
	}
}

func TestForkWorktreeCleansUpWhenNativeLaunchFails(t *testing.T) {
	client := &continuityKitty{launchErr: errors.New("Kitty unavailable")}
	reader := &continuityReader{harness: harness.Claude}
	meta := continuityMeta(t, harness.Claude, "source-1", "work")
	managed := &continuityWorktree{created: worktree.Result{ProjectLabel: "api", Path: t.TempDir(), Branch: "amx/api/source-1", BaseRef: "HEAD", SourceRoot: t.TempDir()}}
	_, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: continuityConfig(harness.Claude), Readers: map[string]transcript.Reader{harness.Claude: reader}, Worktree: managed,
	}, ForkWorktree, Request{Conversation: &meta, Confirmed: true})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "launch_failed" || len(managed.removed) != 1 {
		t.Fatalf("error=%v cleanup=%+v, want launch_failed and unused worktree cleanup", err, managed.removed)
	}
}

func TestForkWorktreeReportsGitCheckoutErrorsWithoutWorkspaceConcepts(t *testing.T) {
	reader := &continuityReader{harness: harness.Codex}
	meta := continuityMeta(t, harness.Codex, "source-1", "work")
	runner := &continuityWorktree{createErr: fmt.Errorf("probe failed: %w", worktree.ErrNotGitCheckout)}
	_, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: &continuityKitty{}, Config: continuityConfig(harness.Codex),
		Readers: map[string]transcript.Reader{harness.Codex: reader}, Worktree: runner,
	}, ForkWorktree, Request{Conversation: &meta, Confirmed: true})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "git_unavailable" {
		t.Fatalf("error = %v, want git_unavailable", err)
	}
}

func TestHandoffReportsFinalArtifactAfterLaunchFailure(t *testing.T) {
	client := &continuityKitty{}
	reader := &continuityReader{harness: harness.Claude}
	meta := continuityMeta(t, harness.Claude, "source-1", "work")
	createdAt := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	runner := &continuityHandoff{
		result: handoff.Result{Artifact: handoff.Artifact{
			ID: "handoff-1", Path: "/tmp/handoff-1.md", ContentHash: "sha256", Status: handoff.StatusFinal,
			CreatedAt: createdAt, UpdatedAt: updatedAt,
		}, TargetProfile: "target", TargetHarness: harness.Codex},
		err: &handoff.PartialLaunchError{Err: errors.New("Kitty unavailable")},
	}
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Config: &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		Readers: map[string]transcript.Reader{harness.Claude: reader}, Handoff: runner,
	}, Handoff, Request{Conversation: &meta, Parameters: map[string]string{"profile": "target"}})
	var partial *PartialResultError
	if !errors.As(err, &partial) || partial.ActionError.Code != "partial_launch_failed" {
		t.Fatalf("error = %#v, want typed partial launch failure", err)
	}
	if result.Action != Handoff || result.Data["path"] != "/tmp/handoff-1.md" || result.Data["status"] != handoff.StatusFinal ||
		result.Data["content_hash"] != "sha256" || result.Data["created_at"] != createdAt || result.Data["updated_at"] != updatedAt {
		t.Fatalf("partial result = %+v, want finalized artifact metadata", result)
	}
}

func continuityConfig(harnessName string) *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Args: []string{"--model", "test"}},
		Accounts: []config.Account{{Name: "work", Harness: harnessName, HomeDir: "/tmp/agentmux-profile"}},
		Projects: []config.Session{{Title: "api", Directory: "/tmp/agentmux-project", Profile: "work", Args: []string{"--project-flag"}}},
	}
}

func continuityMeta(t *testing.T, harnessName, id, profile string) transcript.SessionMeta {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return transcript.SessionMeta{ID: id, Harness: harnessName, Profile: profile, Project: "api", CWD: "/tmp/agentmux-project", Path: path}
}
