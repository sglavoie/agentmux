package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/launch"
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
			if picker.configPickerPurpose != launch.ConfigPickerHandoff || picker.configPickerReturnMode != ModeSessionView {
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
	t.Parallel()

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
	t.Parallel()

	footer := stripAnsi(renderFooter(Model{mode: ModeSessionView, width: 200}))
	for _, want := range []string{"c continue", "f fork", "w worktree", "H handoff", "E edit handoff", "i ID"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("session viewer footer = %q, want %q", footer, want)
		}
	}
}

func TestSessionSearchContinuityFlowsStillReturnToSearch(t *testing.T) {
	t.Parallel()

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
