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

	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func resetProjectCommandsTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := projectCommandsLoadConfig
	oldOpenStore := projectCommandsOpenStore
	oldPlanIndex := projectCommandsLoadPlanIndex
	oldSessionIndex := projectCommandsLoadSessionIndex
	oldPlanPath := projectCommandsPlanIndexPath
	oldSessionPath := projectCommandsSessionIndexPath
	oldReaders := projectCommandsSessionReaders
	oldNewKitty := projectCommandsNewKitty
	oldDaemon := projectCommandsDaemon
	oldNewHandoff := projectCommandsNewHandoff
	oldNewWorktree := projectCommandsNewWorktree
	oldConfigPath := projectCommandsConfigPath
	oldReadFile := projectCommandsReadFile
	oldWorkspaceListOut := workspaceListCmd.OutOrStdout()
	oldWorkspaceShowOut := workspaceShowCmd.OutOrStdout()
	oldWorkspaceDiscoverOut := workspaceDiscoverCmd.OutOrStdout()
	oldWorkspaceAdoptOut := workspaceAdoptCmd.OutOrStdout()
	oldSessionListOut := sessionListCmd.OutOrStdout()
	oldSessionLaunchOut := sessionLaunchCmd.OutOrStdout()
	oldSessionFocusOut := sessionFocusCmd.OutOrStdout()
	oldSessionContinueOut := sessionContinueCmd.OutOrStdout()
	oldSessionForkOut := sessionForkCmd.OutOrStdout()
	oldSessionForkWorktreeOut := sessionForkWorktreeCmd.OutOrStdout()
	oldSessionHandoffOut := sessionHandoffCmd.OutOrStdout()
	oldPlanListOut := planListCmd.OutOrStdout()
	oldPlanLinkOut := planLinkCmd.OutOrStdout()
	oldPlanUnlinkOut := planUnlinkCmd.OutOrStdout()
	testDBPath := filepath.Join(t.TempDir(), "project-commands.db")
	projectCommandsOpenStore = func(ctx context.Context) (*datastore.Store, error) {
		return datastore.OpenPath(ctx, testDBPath)
	}

	t.Cleanup(func() {
		projectCommandsLoadConfig = oldLoadConfig
		projectCommandsOpenStore = oldOpenStore
		projectCommandsLoadPlanIndex = oldPlanIndex
		projectCommandsLoadSessionIndex = oldSessionIndex
		projectCommandsPlanIndexPath = oldPlanPath
		projectCommandsSessionIndexPath = oldSessionPath
		projectCommandsSessionReaders = oldReaders
		projectCommandsNewKitty = oldNewKitty
		projectCommandsDaemon = oldDaemon
		projectCommandsNewHandoff = oldNewHandoff
		projectCommandsNewWorktree = oldNewWorktree
		projectCommandsConfigPath = oldConfigPath
		projectCommandsReadFile = oldReadFile
		workspaceListCmd.SetOut(oldWorkspaceListOut)
		workspaceShowCmd.SetOut(oldWorkspaceShowOut)
		workspaceDiscoverCmd.SetOut(oldWorkspaceDiscoverOut)
		workspaceAdoptCmd.SetOut(oldWorkspaceAdoptOut)
		sessionListCmd.SetOut(oldSessionListOut)
		sessionLaunchCmd.SetOut(oldSessionLaunchOut)
		sessionFocusCmd.SetOut(oldSessionFocusOut)
		sessionContinueCmd.SetOut(oldSessionContinueOut)
		sessionForkCmd.SetOut(oldSessionForkOut)
		sessionForkWorktreeCmd.SetOut(oldSessionForkWorktreeOut)
		sessionHandoffCmd.SetOut(oldSessionHandoffOut)
		planListCmd.SetOut(oldPlanListOut)
		planLinkCmd.SetOut(oldPlanLinkOut)
		planUnlinkCmd.SetOut(oldPlanUnlinkOut)
		_ = workspaceListCmd.Flags().Set("json", "false")
		_ = workspaceShowCmd.Flags().Set("json", "false")
		_ = workspaceDiscoverCmd.Flags().Set("json", "false")
		_ = workspaceAdoptCmd.Flags().Set("profile", "")
		_ = workspaceAdoptCmd.Flags().Set("title", "")
		_ = workspaceAdoptCmd.Flags().Set("yes", "false")
		_ = workspaceAdoptCmd.Flags().Set("json", "false")
		_ = sessionListCmd.Flags().Set("json", "false")
		_ = sessionLaunchCmd.Flags().Set("json", "false")
		_ = sessionFocusCmd.Flags().Set("json", "false")
		_ = sessionContinueCmd.Flags().Set("json", "false")
		_ = sessionForkCmd.Flags().Set("json", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("allow-dirty", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("yes", "false")
		_ = sessionForkWorktreeCmd.Flags().Set("json", "false")
		_ = sessionHandoffCmd.Flags().Set("profile", "")
		_ = sessionHandoffCmd.Flags().Set("edit", "false")
		_ = sessionHandoffCmd.Flags().Set("no-launch", "false")
		_ = sessionHandoffCmd.Flags().Set("json", "false")
		_ = planListCmd.Flags().Set("json", "false")
		_ = planLinkCmd.Flags().Set("json", "false")
		_ = planUnlinkCmd.Flags().Set("json", "false")
	})
}

func TestWorkspaceAdoptPreviewsExactBlockThenRequiresExplicitYes(t *testing.T) {
	resetProjectCommandsTestState(t)
	ctx := context.Background()
	root := t.TempDir()
	projectDir := filepath.Join(root, "api")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	original := []byte(`[[profiles]]
name = "work"
harness = "claude"
home_dir = "` + root + `"
color = "blue"

[harness_emojis]
claude = "🦊"

# retained comment
`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "agent-mux.db")
	store, err := datastore.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	items, err := workspace.New(store).DiscoverCandidates(ctx, workspace.DiscoveryOptions{
		Config: &config.Config{}, Conversations: []transcript.SessionMeta{{ID: "s1", Harness: "codex", CWD: projectDir}},
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("seed candidate = %+v, %v", items, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	projectCommandsLoadConfig = func() (*config.Config, error) {
		cfg, err := config.Load(configPath)
		if err == nil {
			err = config.Validate(cfg)
		}
		return cfg, err
	}
	projectCommandsOpenStore = func(context.Context) (*datastore.Store, error) { return datastore.OpenPath(ctx, dbPath) }
	projectCommandsConfigPath = func() string { return configPath }
	projectCommandsLoadSessionIndex = func(context.Context, string, *datastore.Store, map[string]transcript.Reader, compatimport.SyncMode) (*transcript.Index, error) {
		return nil, errors.New("not available")
	}
	projectCommandsLoadPlanIndex = func(context.Context, string, *datastore.Store, compatimport.SyncMode) (*plans.Index, error) {
		return nil, errors.New("not available")
	}
	projectCommandsNewKitty = func() kitty.KittyClient {
		return kitty.NewClientWithSocket(kitty.NewMockRunner(nil), "")
	}
	if err := workspaceAdoptCmd.Flags().Set("profile", "work"); err != nil {
		t.Fatal(err)
	}
	var preview bytes.Buffer
	workspaceAdoptCmd.SetOut(&preview)
	if err := workspaceAdoptCmd.RunE(workspaceAdoptCmd, []string{items[0].ID}); err != nil {
		t.Fatalf("preview: %v", err)
	}
	wantBlock, _ := config.ProjectBlock(config.Session{Title: "api", Directory: items[0].CanonicalPath, Profile: "work"})
	if !strings.HasPrefix(preview.String(), wantBlock) || !strings.Contains(preview.String(), "--yes") {
		t.Fatalf("preview = %q, want exact block and confirmation guidance", preview.String())
	}
	if got, _ := os.ReadFile(configPath); !bytes.Equal(got, original) {
		t.Fatal("preview mutated config")
	}
	if err := workspaceAdoptCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	var adopted bytes.Buffer
	workspaceAdoptCmd.SetOut(&adopted)
	if err := workspaceAdoptCmd.RunE(workspaceAdoptCmd, []string{items[0].ID}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	got, _ := os.ReadFile(configPath)
	if !bytes.HasPrefix(got, original) || !bytes.Contains(got, []byte(wantBlock)) {
		t.Fatalf("adopted config =\n%s", got)
	}
}

func TestWorkspaceListJSONUsesSchemaV1(t *testing.T) {
	resetProjectCommandsTestState(t)
	root := t.TempDir()
	project := filepath.Join(root, "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	store := datastore.OpenTest(t)
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Projects: []config.Session{{ID: "api", Title: "API", Directory: project, Profile: "work"}}}, nil
	}
	projectCommandsOpenStore = func(context.Context) (*datastore.Store, error) { return store, nil }
	if err := workspaceListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	workspaceListCmd.SetOut(&out)

	if err := workspaceListCmd.RunE(workspaceListCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			Workspaces []workspaceView `json:"workspaces"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || len(payload.Data.Workspaces) != 1 || payload.Data.Workspaces[0].ID != "api" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestWorkspaceShowMissingSelectorReturnsStructuredGuidance(t *testing.T) {
	resetProjectCommandsTestState(t)
	if err := workspaceShowCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	workspaceShowCmd.SetOut(&out)
	err := workspaceShowCmd.RunE(workspaceShowCmd, nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("error = %v, want quiet structured ExitError", err)
	}
	var payload JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || payload.Error.Code != "missing_parameter" || len(payload.Error.Parameters) != 1 || payload.Error.Parameters[0].Name != "workspace" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestSessionAndPlanListJSONAreDeterministic(t *testing.T) {
	resetProjectCommandsTestState(t)
	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "sessions.json")
	planPath := filepath.Join(tmp, "plans.json")
	sessionBody := `{"entries":{"codex:s2":{"id":"s2","harness":"codex","profile":"work","project":"api","last_at":"2026-01-02T03:04:05Z"}}}`
	planBody := `{"entries":{"/plans/example":{"name":"example","dir_path":"/plans/example","files":["manifest.md"],"size":1,"mod_time":"2026-01-02T03:04:05Z","snippet":"hello"}}}`
	if err := os.WriteFile(sessionPath, []byte(sessionBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte(planBody), 0o600); err != nil {
		t.Fatal(err)
	}
	projectCommandsSessionIndexPath = func() string { return sessionPath }
	projectCommandsPlanIndexPath = func() string { return planPath }
	projectCommandsLoadSessionIndex = func(_ context.Context, path string, _ *datastore.Store, _ map[string]transcript.Reader, _ compatimport.SyncMode) (*transcript.Index, error) {
		return transcript.LoadIndexAt(path)
	}
	projectCommandsLoadPlanIndex = func(_ context.Context, path string, _ *datastore.Store, _ compatimport.SyncMode) (*plans.Index, error) {
		return plans.LoadIndexAt(path)
	}
	projectCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: transcript.NewCodexReader()}
	}
	if err := sessionListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := planListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}

	var sessionOut, planOut bytes.Buffer
	sessionListCmd.SetOut(&sessionOut)
	planListCmd.SetOut(&planOut)
	if err := sessionListCmd.RunE(sessionListCmd, nil); err != nil {
		t.Fatalf("session RunE: %v", err)
	}
	if err := planListCmd.RunE(planListCmd, nil); err != nil {
		t.Fatalf("plan RunE: %v", err)
	}

	var sessions JSONEnvelope
	var planPayload JSONEnvelope
	if err := json.Unmarshal(sessionOut.Bytes(), &sessions); err != nil {
		t.Fatalf("sessions JSON: %v", err)
	}
	if err := json.Unmarshal(planOut.Bytes(), &planPayload); err != nil {
		t.Fatalf("plans JSON: %v", err)
	}
	if sessions.SchemaVersion != 1 || planPayload.SchemaVersion != 1 {
		t.Fatalf("schema versions = %d, %d", sessions.SchemaVersion, planPayload.SchemaVersion)
	}
	if !bytes.Contains(sessionOut.Bytes(), []byte(`"id":"s2"`)) || !bytes.Contains(planOut.Bytes(), []byte(`"name":"example"`)) {
		t.Fatalf("unexpected outputs:\n%s\n%s", sessionOut.String(), planOut.String())
	}
}

func TestPlanLinkAndUnlinkUseExplicitSelectorsAndSchemaV1(t *testing.T) {
	resetProjectCommandsTestState(t)
	ctx := context.Background()
	root := t.TempDir()
	projectDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "agent-mux.db")
	planStore, err := datastore.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = planStore.Close() })
	planIndex, err := plans.LoadIndexAt(filepath.Join(root, "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	planIndex = plans.EnableSQLiteIndex(ctx, planIndex, planStore)
	repo, err := plans.NewRepository(planStore)
	if err != nil {
		t.Fatal(err)
	}
	plan := plans.PlanMeta{Name: "delivery", DirPath: filepath.Join(root, "plan"), Files: []string{"manifest.md"}}
	if err := repo.Upsert(ctx, plan, "delivery plan"); err != nil {
		t.Fatal(err)
	}
	projectCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Projects: []config.Session{{ID: "api", Title: "API", Directory: projectDir, Profile: "work"}}}, nil
	}
	projectCommandsOpenStore = func(context.Context) (*datastore.Store, error) { return datastore.OpenPath(ctx, dbPath) }
	projectCommandsLoadPlanIndex = func(context.Context, string, *datastore.Store, compatimport.SyncMode) (*plans.Index, error) {
		return planIndex, nil
	}

	if err := planLinkCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var linked bytes.Buffer
	planLinkCmd.SetOut(&linked)
	if err := planLinkCmd.RunE(planLinkCmd, []string{"delivery", "api"}); err != nil {
		t.Fatalf("link: %v", err)
	}
	var payload struct {
		SchemaVersion int                   `json:"schema_version"`
		Data          planWorkspaceLinkData `json:"data"`
	}
	if err := json.Unmarshal(linked.Bytes(), &payload); err != nil {
		t.Fatalf("link JSON: %v\n%s", err, linked.String())
	}
	if payload.SchemaVersion != JSONSchemaVersion || !payload.Data.Linked || payload.Data.Workspace.ID != "api" {
		t.Fatalf("link payload = %+v", payload)
	}
	if ids, err := repo.WorkspaceIDs(ctx, plan.DirPath); err != nil || len(ids) != 1 || ids[0] != "api" {
		t.Fatalf("linked workspace IDs = %v, %v", ids, err)
	}

	if err := planUnlinkCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var unlinked bytes.Buffer
	planUnlinkCmd.SetOut(&unlinked)
	if err := planUnlinkCmd.RunE(planUnlinkCmd, []string{plan.DirPath, projectDir}); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if ids, err := repo.WorkspaceIDs(ctx, plan.DirPath); err != nil || len(ids) != 0 {
		t.Fatalf("workspace IDs after unlink = %v, %v", ids, err)
	}
}

func TestPlanLinkAmbiguityReturnsCandidatesAndNeverGuesses(t *testing.T) {
	resetProjectCommandsTestState(t)
	idx, err := plans.LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := datastore.OpenTest(t)
	idx = plans.EnableSQLiteIndex(context.Background(), idx, store)
	repo, _ := plans.NewRepository(store)
	for _, path := range []string{"/plans/a", "/plans/b"} {
		if err := repo.Upsert(context.Background(), plans.PlanMeta{Name: "same", DirPath: path}, "body"); err != nil {
			t.Fatal(err)
		}
	}
	projectCommandsLoadPlanIndex = func(context.Context, string, *datastore.Store, compatimport.SyncMode) (*plans.Index, error) {
		return idx, nil
	}
	if err := planLinkCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	planLinkCmd.SetOut(&out)
	err = planLinkCmd.RunE(planLinkCmd, []string{"same", "unused"})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("error = %v, want structured ExitError", err)
	}
	var payload JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "ambiguous_selector" || len(payload.Error.Candidates) != 2 || payload.Error.Candidates[0].ID != "/plans/a" {
		t.Fatalf("payload = %+v", payload)
	}
}
