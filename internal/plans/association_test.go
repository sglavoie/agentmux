package plans

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
)

func TestRefreshClassifiesPlanFromStateMetadataAndLegacyPreparation(t *testing.T) {
	root := t.TempDir()
	stateRoot := t.TempDir()
	runDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "run-repo"))
	metadataDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "metadata-repo"))
	legacyDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "legacy-repo"))
	unmatchedDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "unmatched-repo"))
	for _, dir := range []string{runDir, metadataDir, legacyDir, unmatchedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	statePlan := seedPlanDir(t, root, "state-plan", map[string]string{"manifest.md": "# state"})
	metadataPlan := seedPlanDir(t, root, "metadata-plan", map[string]string{"manifest.md": "# metadata"})
	legacyPlan := seedPlanDir(t, root, "legacy-plan", map[string]string{"manifest.md": "# legacy"})
	unmatchedPlan := seedPlanDir(t, root, "unmatched-plan", map[string]string{"manifest.md": "# unmatched"})
	unknownPlan := seedPlanDir(t, root, "unknown-plan", map[string]string{"manifest.md": "# unknown"})

	writePlanJSON(t, filepath.Join(statePlan, planMetadataFileName), map[string]any{"version": 1, "cwd": metadataDir})
	writePlanState(t, stateRoot, statePlan, runDir)
	writePlanJSON(t, filepath.Join(metadataPlan, planMetadataFileName), map[string]any{"version": 1, "cwd": metadataDir})
	writePlanJSON(t, filepath.Join(legacyPlan, prepareValidationFileName), map[string]any{"version": 1, "codebase_root": legacyDir, "sessions": []any{}})
	writePlanJSON(t, filepath.Join(unmatchedPlan, planMetadataFileName), map[string]any{"version": 1, "cwd": unmatchedDir})

	idx := newTestIndex(t)
	idx.stateRoot = stateRoot
	projects := []config.Session{
		{Title: "Run", Directory: runDir},
		{Title: "Metadata", Directory: metadataDir},
		{Title: "Metadata alias", Directory: metadataDir},
		{Title: "Legacy", Directory: legacyDir},
	}
	if err := idx.Refresh(root, projects...); err != nil {
		t.Fatal(err)
	}

	assertPlanProject(t, idx, statePlan, runDir, "Run", []string{"Run"})
	assertPlanProject(t, idx, metadataPlan, metadataDir, "Metadata", []string{"Metadata", "Metadata alias"})
	assertPlanProject(t, idx, legacyPlan, legacyDir, "Legacy", []string{"Legacy"})
	assertPlanProject(t, idx, unmatchedPlan, unmatchedDir, "", nil)
	assertPlanProject(t, idx, unknownPlan, "", "", nil)
}

func TestRefreshReclassifiesMetadataOnlyChangesAndPreservesLastGood(t *testing.T) {
	root := t.TempDir()
	firstDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "first"))
	secondDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "second"))
	for _, dir := range []string{firstDir, secondDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	planDir := seedPlanDir(t, root, "plan", map[string]string{"manifest.md": "# plan"})
	metadataPath := filepath.Join(planDir, planMetadataFileName)
	writePlanJSON(t, metadataPath, map[string]any{"version": 1, "cwd": firstDir})
	idx := newTestIndex(t)
	projects := []config.Session{{Title: "First", Directory: firstDir}, {Title: "Second", Directory: secondDir}}
	if err := idx.Refresh(root, projects...); err != nil {
		t.Fatal(err)
	}

	writePlanJSON(t, metadataPath, map[string]any{"version": 1, "cwd": secondDir})
	if err := idx.Refresh(root, projects...); err != nil {
		t.Fatal(err)
	}
	assertPlanProject(t, idx, planDir, secondDir, "Second", []string{"Second"})

	if err := os.WriteFile(metadataPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root, projects...); err == nil {
		t.Fatal("malformed metadata refresh returned nil warning")
	}
	assertPlanProject(t, idx, planDir, secondDir, "Second", []string{"Second"})
}

func writePlanState(t *testing.T, stateRoot, planDir, cwd string) {
	t.Helper()
	canonical, err := agentloop.CanonicalizePath(planDir)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(canonical))
	stateDir := filepath.Join(stateRoot, hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePlanJSON(t, filepath.Join(stateDir, "state.json"), map[string]any{"plan_dir": canonical, "cwd": cwd})
}

func writePlanJSON(t *testing.T, path string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func canonicalPlanTestPath(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := agentloop.CanonicalizePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func assertPlanProject(t *testing.T, idx *Index, planDir, cwd, project string, projects []string) {
	t.Helper()
	meta, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("missing plan %s", planDir)
	}
	if meta.CWD != cwd || meta.Project != project {
		t.Fatalf("plan %s association = cwd %q project %q", planDir, meta.CWD, meta.Project)
	}
	if got := meta.ProjectNames(); len(got) != len(projects) {
		t.Fatalf("plan %s projects = %#v, want %#v", planDir, got, projects)
	} else {
		for i := range got {
			if got[i] != projects[i] {
				t.Fatalf("plan %s projects = %#v, want %#v", planDir, got, projects)
			}
		}
	}
}
