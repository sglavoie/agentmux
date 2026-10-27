package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func TestPrunableSelectsOnlyCleanInactiveValidWorktrees(t *testing.T) {
	yes, no := true, false
	base := actions.WorktreeView{Entry: worktree.Entry{
		ID: "wt", Classification: worktree.ClassificationValid, Exists: true, Actionable: true,
		GitAvailable: true, StatusAvailable: true, ReachableFromSource: &yes,
	}}
	if !prunable(base, false) || !prunable(base, true) {
		t.Fatal("a clean, inactive, merged worktree must be prunable")
	}
	mutate := map[string]func(*actions.WorktreeView){
		"dirty":        func(v *actions.WorktreeView) { v.Dirty = true },
		"locked":       func(v *actions.WorktreeView) { v.Locked = true },
		"missing":      func(v *actions.WorktreeView) { v.Exists = false },
		"invalid":      func(v *actions.WorktreeView) { v.Classification = worktree.ClassificationMetadataInvalid },
		"no status":    func(v *actions.WorktreeView) { v.StatusAvailable = false },
		"live session": func(v *actions.WorktreeView) { v.LiveSessions = []actions.WorktreeLiveSession{{}} },
		"looping":      func(v *actions.WorktreeView) { v.LoopCount = 1 },
	}
	for name, change := range mutate {
		view := base
		change(&view)
		if prunable(view, false) {
			t.Errorf("%s worktree was selected for pruning", name)
		}
	}
	unmerged := base
	unmerged.ReachableFromSource = &no
	if !prunable(unmerged, false) || prunable(unmerged, true) {
		t.Error("--merged must exclude an unmerged branch, and only --merged")
	}
}

func TestWorktreePrunePreviewsThenRemoves(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) { return nil, nil }
	t.Cleanup(func() {
		worktreePruneCmd.SetOut(nil)
		for _, flag := range []string{"yes", "merged", "delete-branch", "json"} {
			_ = worktreePruneCmd.Flags().Set(flag, "false")
		}
	})

	var preview bytes.Buffer
	worktreePruneCmd.SetOut(&preview)
	if err := worktreePruneCmd.RunE(worktreePruneCmd, nil); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(preview.String(), "would remove wt-1") || !strings.Contains(preview.String(), "re-run with --yes") {
		t.Fatalf("preview = %q", preview.String())
	}
	if len(manager.removeOptions) != 0 {
		t.Fatal("a preview removed a worktree")
	}

	if err := worktreePruneCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if err := worktreePruneCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	worktreePruneCmd.SetOut(&out)
	if err := worktreePruneCmd.RunE(worktreePruneCmd, nil); err != nil {
		t.Fatalf("prune --yes: %v", err)
	}
	var payload struct {
		Data worktreePruneData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Data.DryRun || len(payload.Data.Removed) != 1 || payload.Data.Removed[0] != "wt-1" || len(manager.removeOptions) != 1 {
		t.Fatalf("payload = %+v, remove calls = %d", payload.Data, len(manager.removeOptions))
	}
}

func TestWorktreePruneLeavesAnActiveWorktreeAlone(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t) // the fixture's live session sits in wt-1
	t.Cleanup(func() { worktreePruneCmd.SetOut(nil); _ = worktreePruneCmd.Flags().Set("yes", "false") })
	if err := worktreePruneCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	worktreePruneCmd.SetOut(&out)
	if err := worktreePruneCmd.RunE(worktreePruneCmd, nil); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(manager.removeOptions) != 0 || !strings.Contains(out.String(), "Nothing to prune") {
		t.Fatalf("output = %q, remove calls = %d; want nothing pruned", out.String(), len(manager.removeOptions))
	}
}

// A removal whose checkout went but whose branch could not be deleted is a
// removal, reported as incomplete: "kept" would send the operator looking for
// a checkout that is gone. The exit stays nonzero, since not all was done.
func TestWorktreePruneReportsACheckoutRemovedWithoutItsBranchAsRemoved(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	manager.branchErr = errors.New("branch wt-branch is not fully merged")
	worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) { return nil, nil }
	t.Cleanup(func() {
		worktreePruneCmd.SetOut(nil)
		for _, flag := range []string{"yes", "delete-branch", "json"} {
			_ = worktreePruneCmd.Flags().Set(flag, "false")
		}
	})
	for _, flag := range []string{"yes", "delete-branch"} {
		if err := worktreePruneCmd.Flags().Set(flag, "true"); err != nil {
			t.Fatal(err)
		}
	}

	var text bytes.Buffer
	worktreePruneCmd.SetOut(&text)
	err := worktreePruneCmd.RunE(worktreePruneCmd, nil)
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("prune = %v, want exit 1 for the incomplete removal", err)
	}
	if strings.Contains(text.String(), "kept") || !strings.Contains(text.String(), "removed wt-1, but not completely") {
		t.Fatalf("text = %q", text.String())
	}

	if err := worktreePruneCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	worktreePruneCmd.SetOut(&out)
	if err := worktreePruneCmd.RunE(worktreePruneCmd, nil); !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("prune --json = %v, want a quiet nonzero exit", err)
	}
	var payload struct {
		Data worktreePruneData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	data := payload.Data
	if len(data.Removed) != 1 || data.Removed[0] != "wt-1" || len(data.Failed) != 0 ||
		len(data.Partial) != 1 || data.Partial[0].ID != "wt-1" || !strings.Contains(data.Partial[0].Message, "not fully merged") {
		t.Fatalf("payload = %+v", data)
	}
}
