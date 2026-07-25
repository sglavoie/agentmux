package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func viewerActionMeta() transcript.SessionMeta {
	return transcript.SessionMeta{
		ID:      "viewer-source",
		Harness: harness.Claude,
		Profile: "work",
		Project: "api",
		CWD:     "/tmp/api",
		Path:    "/tmp/api/session.jsonl",
	}
}

func viewerActionConfig() *config.Config {
	return &config.Config{Accounts: []config.Account{
		{Name: "work", Harness: harness.Claude},
		{Name: "target", Harness: harness.Codex},
	}}
}

func viewerActionModel() Model {
	meta := viewerActionMeta()
	return Model{
		mode:              ModeSessionView,
		width:             200,
		height:            30,
		cfg:               viewerActionConfig(),
		transcriptReaders: map[string]transcript.Reader{harness.Claude: transcript.NewClaudeReader()},
		viewerSession:     &meta,
		viewerMessages:    []transcript.Message{{Role: "user", Text: "hello"}},
	}
}

func TestSessionViewerDispatchesContinuityActions(t *testing.T) {
	base := viewerActionModel()
	for _, key := range []string{"c", "f"} {
		updated, cmd := base.Update(keyPress(key))
		got := updated.(Model)
		if got.mode != ModeSessionView || cmd == nil {
			t.Fatalf("%s = mode %v cmd %T, want continuity command without leaving the viewer", key, got.mode, cmd)
		}
		if got.viewerSession == nil || got.viewerSession.ID != "viewer-source" {
			t.Fatalf("%s changed the viewed transcript: %+v", key, got.viewerSession)
		}
	}
}

func TestSessionViewerCopyIDCopiesViewedSessionID(t *testing.T) {
	updated, cmd := viewerActionModel().Update(keyPress("i"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "viewer-source" {
		t.Fatalf("copied = %q, want viewed session ID", copied)
	}
}

func TestSessionViewerWorktreeForkReturnsToViewer(t *testing.T) {
	updated, cmd := viewerActionModel().Update(keyPress("w"))
	confirm := updated.(Model)
	if confirm.mode != ModeConfirm || cmd != nil {
		t.Fatalf("w = mode %v cmd %T, want isolated-fork confirmation", confirm.mode, cmd)
	}
	if confirm.confirmAction != "fork worktree" || confirm.confirmConsequence != worktreeForkConsequence {
		t.Fatalf("confirm action = %q consequence = %q, want the worktree-fork prompt", confirm.confirmAction, confirm.confirmConsequence)
	}
	if confirm.confirmReturnMode != ModeSessionView {
		t.Fatalf("confirmReturnMode = %v, want ModeSessionView", confirm.confirmReturnMode)
	}
	if confirm.isolatedForkConversation == nil || confirm.isolatedForkConversation.ID != "viewer-source" {
		t.Fatalf("fork conversation = %+v, want the viewed transcript", confirm.isolatedForkConversation)
	}
	if confirm.targetSession.Directory != "/tmp/api" || confirm.targetSession.Project != "api" {
		t.Fatalf("fork target = %+v, want the viewed transcript's project and directory", confirm.targetSession)
	}

	updated, cmd = confirm.Update(keyPress("n"))
	cancelled := updated.(Model)
	if cancelled.mode != ModeSessionView || cmd != nil || cancelled.isolatedForkConversation != nil {
		t.Fatalf("cancel = mode %v conversation %+v cmd %T, want the viewer with a cleared request", cancelled.mode, cancelled.isolatedForkConversation, cmd)
	}
	if cancelled.viewerSession == nil || cancelled.viewerSession.ID != "viewer-source" {
		t.Fatalf("cancel dropped the viewed transcript: %+v", cancelled.viewerSession)
	}

	updated, cmd = confirm.Update(keyPress("y"))
	accepted := updated.(Model)
	if accepted.mode != ModeSessionView || cmd == nil || accepted.isolatedForkConversation != nil {
		t.Fatalf("accept = mode %v conversation %+v cmd %T, want the viewer plus a fork command", accepted.mode, accepted.isolatedForkConversation, cmd)
	}
}

func TestSessionViewerHandoffPickerReturnsToViewer(t *testing.T) {
	for _, tc := range []struct {
		key      string
		wantEdit bool
	}{
		{key: "H"},
		{key: "E", wantEdit: true},
	} {
		t.Run(tc.key, func(t *testing.T) {
			updated, cmd := viewerActionModel().Update(keyPress(tc.key))
			picker := updated.(Model)
			if picker.mode != ModeConfigPicker || cmd != nil {
				t.Fatalf("%s = mode %v cmd %T, want profile picker without launch", tc.key, picker.mode, cmd)
			}
			if picker.configPickerPurpose != ConfigPickerHandoff || picker.configPickerReturnMode != ModeSessionView {
				t.Fatalf("%s picker purpose = %v return = %v, want handoff returning to the viewer", tc.key, picker.configPickerPurpose, picker.configPickerReturnMode)
			}
			if picker.handoffConversation == nil || picker.handoffConversation.ID != "viewer-source" || picker.handoffEdit != tc.wantEdit {
				t.Fatalf("%s handoff = %+v edit=%v, want the viewed transcript with edit=%v", tc.key, picker.handoffConversation, picker.handoffEdit, tc.wantEdit)
			}
			if picker.configPickerProjectIdx != -1 || len(picker.configPickerAccounts) != 2 {
				t.Fatalf("%s picker fields = projectIdx %d accounts %+v, want -1 and the configured profiles", tc.key, picker.configPickerProjectIdx, picker.configPickerAccounts)
			}

			updated, cmd = picker.Update(keyPress("esc"))
			cancelled := updated.(Model)
			if cancelled.mode != ModeSessionView || cmd != nil || cancelled.handoffConversation != nil || cancelled.handoffEdit {
				t.Fatalf("%s cancel = mode %v handoff %+v edit=%v cmd %T, want the viewer with a cleared request", tc.key, cancelled.mode, cancelled.handoffConversation, cancelled.handoffEdit, cmd)
			}
			if cancelled.viewerSession == nil || cancelled.viewerSession.ID != "viewer-source" {
				t.Fatalf("%s cancel dropped the viewed transcript: %+v", tc.key, cancelled.viewerSession)
			}

			updated, _ = picker.Update(keyPress("j"))
			updated, cmd = updated.(Model).Update(keyPress("enter"))
			accepted := updated.(Model)
			if accepted.mode != ModeSessionView || cmd == nil || accepted.handoffConversation != nil {
				t.Fatalf("%s accept = mode %v handoff %+v cmd %T, want the viewer plus a handoff command", tc.key, accepted.mode, accepted.handoffConversation, cmd)
			}
		})
	}
}

func TestSessionViewerContinuityActionsNoOpWithoutViewedTranscript(t *testing.T) {
	for _, key := range []string{"c", "f", "w", "H", "E", "i"} {
		t.Run(key, func(t *testing.T) {
			m := Model{mode: ModeSessionView, width: 200, height: 30, cfg: viewerActionConfig()}
			updated, cmd := m.Update(keyPress(key))
			got := updated.(Model)
			if got.mode != ModeSessionView || cmd != nil {
				t.Fatalf("%s = mode %v cmd %T, want a no-op in the viewer", key, got.mode, cmd)
			}
		})
	}
}

func TestSessionViewerFooterAdvertisesContinuityActions(t *testing.T) {
	footer := stripAnsi(renderFooter(Model{mode: ModeSessionView, width: 200}))
	for _, want := range []string{"c continue", "f fork", "w worktree", "H handoff", "E edit handoff", "i ID"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("session viewer footer = %q, want %q", footer, want)
		}
	}
}

type viewerActionHandoffRunner struct {
	requests []handoff.Request
	result   handoff.Result
}

func (r *viewerActionHandoffRunner) Run(_ context.Context, request handoff.Request) (handoff.Result, error) {
	r.requests = append(r.requests, request)
	return r.result, nil
}

type viewerActionWorktreeRunner struct {
	requests []worktree.Request
	created  worktree.Result
	removed  []worktree.Result
}

func (r *viewerActionWorktreeRunner) Create(_ context.Context, request worktree.Request) (worktree.Result, error) {
	r.requests = append(r.requests, request)
	return r.created, nil
}

func (r *viewerActionWorktreeRunner) RemoveUnused(_ context.Context, result worktree.Result) error {
	r.removed = append(r.removed, result)
	return nil
}

// viewerActionExecution is a session viewer whose continuity commands can run
// end to end: the transcript source exists on disk and Kitty, the daemon, and
// the handoff/worktree workflows are all recorders.
type viewerActionExecution struct {
	model    Model
	kitty    *fakeLaunchDispatchClient
	handoff  *viewerActionHandoffRunner
	worktree *viewerActionWorktreeRunner
	meta     transcript.SessionMeta
}

func newViewerActionExecution(t *testing.T) viewerActionExecution {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.jsonl")
	line := `{"type":"user","timestamp":"2026-01-02T14:04:05Z","message":{"role":"user","content":"viewer action fixture"}}`
	if err := os.WriteFile(source, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := viewerActionMeta()
	meta.CWD = t.TempDir()
	meta.Path = source

	exec := viewerActionExecution{
		kitty: &fakeLaunchDispatchClient{},
		handoff: &viewerActionHandoffRunner{result: handoff.Result{
			Artifact:      handoff.Artifact{ID: "handoff-1", Path: filepath.Join(t.TempDir(), "handoff-1.md"), Status: handoff.StatusFinal},
			TargetProfile: "target", TargetHarness: harness.Codex, Launched: true,
		}},
		worktree: &viewerActionWorktreeRunner{created: worktree.Result{
			ProjectLabel: "api", Path: t.TempDir(), Branch: "amx/api/viewer-source", BaseRef: "HEAD", SourceRoot: meta.CWD,
		}},
		meta: meta,
	}
	exec.model = viewerActionModel()
	exec.model.viewerSession = &meta
	exec.model.client = exec.kitty
	exec.model.handoffRunner = exec.handoff
	exec.model.worktreeRunner = exec.worktree
	exec.model.daemonCommand = func(daemon.Request) (daemon.Response, error) {
		return daemon.Response{OK: true}, nil
	}
	return exec
}

// runViewerActionCmd executes a viewer action command and fails on any error
// the shared registry reports, so a command that only looks non-nil cannot pass.
func runViewerActionCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("action command is nil")
	}
	msg := cmd()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("action command returned %T, want actionDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("action failed: %v", done.err)
	}
}

func assertViewerRetained(t *testing.T, updated tea.Model) {
	t.Helper()
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if got.viewerSession == nil || got.viewerSession.ID != "viewer-source" {
		t.Fatalf("viewed transcript = %+v, want the source conversation", got.viewerSession)
	}
}

func TestSessionViewerExecutesContinuityActionsAgainstFakeDependencies(t *testing.T) {
	t.Run("c focuses the exact live conversation", func(t *testing.T) {
		exec := newViewerActionExecution(t)
		identity := conversation.Identity{Harness: harness.Claude, SessionID: "viewer-source"}
		exec.kitty.windows = []kitty.OSWindow{{Tabs: []kitty.Tab{{
			ID:      100,
			Windows: []kitty.Window{{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude, Project: "api"}}},
		}}}}
		exec.model.daemonCommand = func(daemon.Request) (daemon.Response, error) {
			return daemon.Response{OK: true, States: map[int]daemon.WindowStateSnapshot{10: {Conversation: &identity}}}, nil
		}

		updated, cmd := exec.model.Update(keyPress("c"))
		runViewerActionCmd(t, cmd)
		assertViewerRetained(t, updated)
		if len(exec.kitty.focused) != 1 || exec.kitty.focused[0] != "id:100" {
			t.Fatalf("focused = %v, want the live conversation's tab", exec.kitty.focused)
		}
		if len(exec.kitty.launched) != 0 {
			t.Fatalf("launched = %+v, want no resume when a live owner exists", exec.kitty.launched)
		}
	})

	t.Run("f launches a native fork of the viewed conversation", func(t *testing.T) {
		exec := newViewerActionExecution(t)

		updated, cmd := exec.model.Update(keyPress("f"))
		runViewerActionCmd(t, cmd)
		assertViewerRetained(t, updated)
		if len(exec.kitty.focused) != 0 || len(exec.kitty.launched) != 1 {
			t.Fatalf("fork focused=%v launched=%+v, want exactly one new tab", exec.kitty.focused, exec.kitty.launched)
		}
		opts := exec.kitty.launched[0]
		if opts.Directory != exec.meta.CWD {
			t.Fatalf("launch directory = %q, want the source working directory %q", opts.Directory, exec.meta.CWD)
		}
		if got := strings.Join(opts.Cmdline, " "); !strings.Contains(got, "--resume viewer-source --fork-session") {
			t.Fatalf("command = %q, want a native fork of the viewed session", got)
		}
		if opts.UserVars["amx_profile"] != "work" || opts.UserVars["amx_harness"] != harness.Claude {
			t.Fatalf("launch user vars = %+v, want the viewed conversation's profile and harness", opts.UserVars)
		}
	})

	t.Run("w launches the fork inside the created worktree", func(t *testing.T) {
		exec := newViewerActionExecution(t)

		updated, cmd := exec.model.Update(keyPress("w"))
		confirm := updated.(Model)
		if confirm.mode != ModeConfirm || cmd != nil {
			t.Fatalf("w = mode %v cmd %T, want the isolated-fork confirmation", confirm.mode, cmd)
		}
		updated, cmd = confirm.Update(keyPress("y"))
		runViewerActionCmd(t, cmd)
		assertViewerRetained(t, updated)

		if len(exec.worktree.requests) != 1 {
			t.Fatalf("worktree requests = %+v, want exactly one", exec.worktree.requests)
		}
		request := exec.worktree.requests[0]
		if request.SourceCWD != exec.meta.CWD || request.SessionID != "viewer-source" || request.ProjectLabel != "api" || request.AllowDirty {
			t.Fatalf("worktree request = %+v, want the viewed conversation with a clean source", request)
		}
		if len(exec.kitty.launched) != 1 || exec.kitty.launched[0].Directory != exec.worktree.created.Path {
			t.Fatalf("launched = %+v, want one tab in the isolated checkout %q", exec.kitty.launched, exec.worktree.created.Path)
		}
		if got := strings.Join(exec.kitty.launched[0].Cmdline, " "); !strings.Contains(got, "--resume viewer-source --fork-session") {
			t.Fatalf("command = %q, want a native fork of the viewed session", got)
		}
		if len(exec.worktree.removed) != 0 {
			t.Fatalf("successful launch removed the worktree: %+v", exec.worktree.removed)
		}
	})

	for _, tc := range []struct {
		key      string
		wantEdit bool
	}{
		{key: "H"},
		{key: "E", wantEdit: true},
	} {
		t.Run(tc.key+" hands the viewed conversation to the target profile", func(t *testing.T) {
			exec := newViewerActionExecution(t)

			updated, cmd := exec.model.Update(keyPress(tc.key))
			picker := updated.(Model)
			if picker.mode != ModeConfigPicker || cmd != nil {
				t.Fatalf("%s = mode %v cmd %T, want the profile picker without launching", tc.key, picker.mode, cmd)
			}
			updated, _ = picker.Update(keyPress("j"))
			updated, cmd = updated.(Model).Update(keyPress("enter"))
			runViewerActionCmd(t, cmd)
			assertViewerRetained(t, updated)

			if len(exec.handoff.requests) != 1 {
				t.Fatalf("handoff requests = %+v, want exactly one", exec.handoff.requests)
			}
			request := exec.handoff.requests[0]
			if request.Source.ID != "viewer-source" || request.Source.CWD != exec.meta.CWD || request.Source.Path != exec.meta.Path {
				t.Fatalf("handoff source = %+v, want the viewed transcript", request.Source)
			}
			if request.TargetProfile != "target" || request.Edit != tc.wantEdit || request.NoLaunch {
				t.Fatalf("handoff target = %q edit = %v noLaunch = %v, want the picked profile with edit=%v and a launch", request.TargetProfile, request.Edit, request.NoLaunch, tc.wantEdit)
			}
			if request.Reader == nil || !strings.EqualFold(request.Reader.Harness(), harness.Claude) {
				t.Fatalf("handoff reader = %+v, want the source harness reader", request.Reader)
			}
			// The workflow owns the target launch, so the viewer's only launch
			// obligation is handing it the live Kitty client.
			if request.Kitty != exec.kitty {
				t.Fatalf("handoff Kitty client = %+v, want the injected client", request.Kitty)
			}
			if len(exec.kitty.launched) != 0 {
				t.Fatalf("launched = %+v, want the handoff workflow to own the target launch", exec.kitty.launched)
			}
		})
	}
}

func TestSessionSearchContinuityFlowsStillReturnToSearch(t *testing.T) {
	meta := viewerActionMeta()
	base := Model{
		mode:              ModeSessionSearch,
		width:             200,
		height:            30,
		cfg:               viewerActionConfig(),
		transcriptReaders: map[string]transcript.Reader{harness.Claude: transcript.NewClaudeReader()},
		sessionResults:    []transcript.Result{{Meta: meta}},
	}

	updated, _ := base.Update(keyPress("w"))
	confirm := updated.(Model)
	if confirm.mode != ModeConfirm || confirm.confirmReturnMode != ModeSessionSearch {
		t.Fatalf("w = mode %v return %v, want confirmation returning to search", confirm.mode, confirm.confirmReturnMode)
	}
	for _, key := range []string{"n", "y"} {
		updated, _ := confirm.Update(keyPress(key))
		if got := updated.(Model); got.mode != ModeSessionSearch {
			t.Fatalf("confirm %s = mode %v, want ModeSessionSearch", key, got.mode)
		}
	}

	for _, tc := range []struct {
		key      string
		wantEdit bool
	}{
		{key: "H"},
		{key: "E", wantEdit: true},
	} {
		updated, _ := base.Update(keyPress(tc.key))
		picker := updated.(Model)
		if picker.mode != ModeConfigPicker || picker.configPickerReturnMode != ModeSessionSearch || picker.handoffEdit != tc.wantEdit {
			t.Fatalf("%s = mode %v return %v edit=%v, want handoff picker returning to search", tc.key, picker.mode, picker.configPickerReturnMode, picker.handoffEdit)
		}
		updated, _ = picker.Update(keyPress("esc"))
		if got := updated.(Model); got.mode != ModeSessionSearch {
			t.Fatalf("%s cancel = mode %v, want ModeSessionSearch", tc.key, got.mode)
		}
		updated, cmd := picker.Update(keyPress("enter"))
		if got := updated.(Model); got.mode != ModeSessionSearch || cmd == nil {
			t.Fatalf("%s accept = mode %v cmd %T, want search plus a handoff command", tc.key, got.mode, cmd)
		}
	}
}
