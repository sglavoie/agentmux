package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func TestSessionContinueCLIReportsExistingTmuxConversation(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Codex)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{LaunchBackend: "kitty"}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex}}
	}
	client := &sessionCommandKitty{listErr: errors.New("Kitty unavailable")}
	sessionCommandsNewKitty = func() kitty.KittyClient { return client }
	identity := conversation.Identity{Harness: meta.Harness, SessionID: meta.ID}
	sessionCommandsDaemon = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{OK: true,
			States:       map[int]protocol.WindowStateSnapshot{123: {Conversation: &identity}},
			RuntimeSlots: map[int]protocol.RuntimeSlot{123: {RuntimeSessionID: "rs-live", Backend: "tmux"}},
		}, nil
	}
	if err := sessionContinueCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionContinueCmd.SetOut(&out)
	if err := sessionContinueCmd.RunE(sessionContinueCmd, []string{meta.Harness, meta.ID}); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Data sessionActionData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Changed || payload.Data.Data["continuation"] != actions.DataContinuationAttach || payload.Data.Data["runtime_session_id"] != "rs-live" || len(client.launched) != 0 {
		t.Fatalf("continue did not return the running conversation: %s", out.String())
	}
}

func TestSessionForkWorktreeCLIRetainsCleanupFailureInJSONAndText(t *testing.T) {
	resetSessionCommandsTestState(t)
	meta, indexPath := sessionCommandMeta(t, harness.Copilot)
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{LaunchBackend: "kitty"}, Accounts: []config.Account{{Name: "work", Harness: harness.Copilot}}}, nil
	}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Copilot: sessionCommandReader{harness: harness.Copilot}}
	}
	sessionCommandsNewKitty = func() kitty.KittyClient { return &sessionCommandKitty{} }
	managed := &sessionCommandWorktree{result: worktree.Result{ID: "retained", Path: t.TempDir(), Branch: "amx/api/source"}, removeErr: worktree.ErrWorktreeDirty}
	sessionCommandsNewWorktree = func() worktree.Runner { return managed }
	sessionCommandsNewHandoff = func() handoff.Runner {
		return &sessionCommandHandoff{err: &handoff.PartialLaunchError{Err: errors.New("launch failed")}}
	}
	if err := sessionForkWorktreeCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sessionForkWorktreeCmd.SetOut(&out)
	for _, format := range []string{"true", "false"} {
		if err := sessionForkWorktreeCmd.Flags().Set("json", format); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		err := sessionForkWorktreeCmd.RunE(sessionForkWorktreeCmd, []string{meta.Harness, meta.ID})
		if err == nil {
			t.Fatal("failed launch reported success")
		}
		if format == "true" {
			var exitErr *cli.ExitError
			if !errors.As(err, &exitErr) || !exitErr.Quiet || exitErr.Code != 1 {
				t.Fatalf("expected quiet nonzero JSON exit, got %v", err)
			}
			var payload struct {
				Data sessionActionData `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			retained, ok := payload.Data.Data["worktree"].(map[string]any)
			if !ok || retained["path"] != managed.result.Path || payload.Data.Data["worktree_cleanup_incomplete"] != true || !payload.Data.Changed {
				t.Fatalf("lost retained checkout from JSON: %s", out.String())
			}
		} else if !strings.Contains(out.String(), managed.result.Path) || !strings.Contains(out.String(), worktree.ErrWorktreeDirty.Error()) {
			t.Fatalf("lost retained checkout from text: %s", out.String())
		}
	}
}
