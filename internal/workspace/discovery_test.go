package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestDiscoverCandidatesDeduplicatesEvidencePersistsIDAndMergesHistory(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	service := New(store)
	root := t.TempDir()
	project := filepath.Join(root, "useful-api")
	nested := filepath.Join(project, "cmd")
	planOnly := filepath.Join(root, "worker")
	for _, dir := range []string{nested, planOnly} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := requireGit(t)
	runGit(t, git, project, "init")
	now := time.Unix(1_700_000_000, 0)
	canonicalProject := projectresolve.AbsPath(project)
	canonicalPlanOnly := projectresolve.AbsPath(planOnly)
	opts := DiscoveryOptions{
		Config: &config.Config{}, Now: now,
		LiveSessions:  []session.RunningSession{{WindowID: 7, Directory: project}},
		Conversations: []transcript.SessionMeta{{ID: "abc", Harness: "codex", CWD: nested}},
		Plans:         []PlanEvidence{{ID: "plan-1", Name: "Useful API rollout", Content: fmt.Sprintf("work in `%s` and %s", project, planOnly)}},
	}
	items, err := service.DiscoverCandidates(ctx, opts)
	if err != nil {
		t.Fatalf("DiscoverCandidates: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("candidate count = %d, want 2: %+v", len(items), items)
	}
	var api ProjectCandidate
	for _, item := range items {
		if item.CanonicalPath == canonicalProject {
			api = item
		}
	}
	if api.ID == "" || len(api.Observations) != 4 {
		t.Fatalf("API candidate = %+v, want live/transcript/plan-path/title evidence", api)
	}

	opts.Now = now.Add(time.Hour)
	again, err := service.DiscoverCandidates(ctx, opts)
	if err != nil || len(again) != 2 {
		t.Fatalf("repeat discovery = %+v, %v", again, err)
	}
	var stable string
	for _, item := range again {
		if item.CanonicalPath == canonicalProject {
			stable = item.ID
		}
	}
	if stable != api.ID {
		t.Fatalf("candidate ID changed from %q to %q", api.ID, stable)
	}

	cfg := &config.Config{Projects: []config.Session{{ID: "api", Title: "Useful API", Directory: project, Profile: "work"}}}
	workspaces, err := service.Build(ctx, cfg)
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("Build adopted workspace: %+v, %v", workspaces, err)
	}
	remaining, err := service.ListCandidates(ctx)
	if err != nil || len(remaining) != 1 || remaining[0].CanonicalPath != canonicalPlanOnly {
		t.Fatalf("remaining candidates = %+v, %v", remaining, err)
	}
	var workspaceID string
	var observationCount int
	if err := store.DB().QueryRow(`SELECT workspace_id FROM workspace_candidates WHERE id = ?`, api.ID).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT count(*) FROM workspace_candidate_observations WHERE candidate_id = ?`, api.ID).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if workspaceID != workspaces[0].ID || observationCount != 4 {
		t.Fatalf("merged history workspace=%q observations=%d", workspaceID, observationCount)
	}
}
