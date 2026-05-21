package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// Status tests

func TestStatusMissingNoFile(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if results[0].Status != SyncMissing {
		t.Errorf("expected missing, got %q", results[0].Status)
	}
}

func TestStatusMissingNoamxHooks(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(accountDir, "settings.json"), map[string]any{
		"hooks": map[string]any{},
	})

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, _ := StatusAll(cfg, testBin)
	if results[0].Status != SyncMissing {
		t.Errorf("expected missing, got %q", results[0].Status)
	}
}

func TestStatusInSync(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	// Merge to install correct hooks.
	_, _ = MergeAll(cfg, "", testBin)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if results[0].Status != SyncInSync {
		t.Errorf("expected in_sync, got %q", results[0].Status)
	}
}

func TestStatusDrifted(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write settings.json with stale amx hook (old binary path).
	writeJSON(t, filepath.Join(accountDir, "settings.json"), map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/path/amx hook-indicator active",
							"async":   true,
						},
					},
				},
			},
		},
	})

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if results[0].Status != SyncDrifted {
		t.Errorf("expected drifted, got %q", results[0].Status)
	}
}

func TestOverallHookHealth_BucketsByStatus(t *testing.T) {
	dir := t.TempDir()

	inSyncDir := filepath.Join(dir, "in-sync")
	driftedDir := filepath.Join(dir, "drifted")
	missingDir := filepath.Join(dir, "missing")

	cfg := makeConfig([]config.Account{
		{Name: "in", ConfigDir: inSyncDir, Color: "green"},
		{Name: "drifted", ConfigDir: driftedDir, Color: "blue"},
		{Name: "missing", ConfigDir: missingDir, Color: "red"},
	}, nil)

	// in-sync: a successful merge installs the correct hooks.
	inSyncOnly := makeConfig([]config.Account{
		{Name: "in", ConfigDir: inSyncDir, Color: "green"},
	}, nil)
	if _, err := MergeAll(inSyncOnly, "", testBin); err != nil {
		t.Fatalf("seed in-sync: %v", err)
	}

	// drifted: write a settings.json with a stale binary path.
	if err := os.MkdirAll(driftedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(driftedDir, "settings.json"), map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/path/amx hook-indicator active",
							"async":   true,
						},
					},
				},
			},
		},
	})

	// missing: no settings.json at all (directory absent is fine).

	summary, err := OverallHookHealth(cfg, testBin)
	if err != nil {
		t.Fatalf("OverallHookHealth: %v", err)
	}
	if len(summary.InSync) != 1 || summary.InSync[0] != "in" {
		t.Errorf("InSync: got %v, want [in]", summary.InSync)
	}
	if len(summary.Drifted) != 1 || summary.Drifted[0] != "drifted" {
		t.Errorf("Drifted: got %v, want [drifted]", summary.Drifted)
	}
	if len(summary.Missing) != 1 || summary.Missing[0] != "missing" {
		t.Errorf("Missing: got %v, want [missing]", summary.Missing)
	}
	if summary.Healthy() {
		t.Error("Healthy: got true, want false (drifted+missing present)")
	}
}

func TestOverallHookHealth_AllInSyncIsHealthy(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	summary, err := OverallHookHealth(cfg, testBin)
	if err != nil {
		t.Fatalf("OverallHookHealth: %v", err)
	}
	if !summary.Healthy() {
		t.Errorf("Healthy: got false, want true (summary=%+v)", summary)
	}
}

// TestStatusAll_ContinuesPastPerAccountError verifies the post-session-5
// contract: when one account's checkStatus fails (here we plant a
// directory where settings.json should be a file, so os.ReadFile fails
// with a non-IsNotExist error), StatusAll still returns entries for the
// remaining accounts and reports the joined error. Previously the first
// failure short-circuited the loop, hiding accounts further down the
// list from the hooks-status command and the TUI health pip.
func TestStatusAll_ContinuesPastPerAccountError(t *testing.T) {
	dir := t.TempDir()

	brokenDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(filepath.Join(brokenDir, "settings.json"), 0o755); err != nil {
		t.Fatalf("seed broken settings dir: %v", err)
	}

	inSyncDir := filepath.Join(dir, "in-sync")
	missingDir := filepath.Join(dir, "missing")

	cfg := makeConfig([]config.Account{
		{Name: "broken", ConfigDir: brokenDir, Color: "red"},
		{Name: "in", ConfigDir: inSyncDir, Color: "green"},
		{Name: "missing", ConfigDir: missingDir, Color: "blue"},
	}, nil)

	inSyncOnly := makeConfig([]config.Account{
		{Name: "in", ConfigDir: inSyncDir, Color: "green"},
	}, nil)
	if _, err := MergeAll(inSyncOnly, "", testBin); err != nil {
		t.Fatalf("seed in-sync: %v", err)
	}

	results, err := StatusAll(cfg, testBin)
	if err == nil {
		t.Fatal("expected joined error when one account fails, got nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("expected joined error to mention failing account %q, got %v", "broken", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results (one per account), got %d", len(results))
	}

	byName := map[string]AccountStatus{}
	for _, s := range results {
		byName[s.Account] = s
	}
	if byName["broken"].Err == nil {
		t.Errorf("expected per-account Err on the failing account, got nil")
	}
	if byName["in"].Err != nil {
		t.Errorf("expected nil Err for in-sync account, got %v", byName["in"].Err)
	}
	if byName["in"].Status != SyncInSync {
		t.Errorf("expected in-sync account status=in_sync, got %q", byName["in"].Status)
	}
	if byName["missing"].Err != nil {
		t.Errorf("expected nil Err for missing account, got %v", byName["missing"].Err)
	}
	if byName["missing"].Status != SyncMissing {
		t.Errorf("expected missing account status=missing, got %q", byName["missing"].Status)
	}
}

// TestOverallHookHealth_ReturnsPartialBucketsOnError mirrors the StatusAll
// contract change: even when one account errors, the summary should still
// bucket the accounts that did report successfully and return the joined
// error so the TUI can degrade to a partial pip rather than blanking out.
func TestOverallHookHealth_ReturnsPartialBucketsOnError(t *testing.T) {
	dir := t.TempDir()

	brokenDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(filepath.Join(brokenDir, "settings.json"), 0o755); err != nil {
		t.Fatalf("seed broken settings dir: %v", err)
	}
	inSyncDir := filepath.Join(dir, "in-sync")

	cfg := makeConfig([]config.Account{
		{Name: "broken", ConfigDir: brokenDir, Color: "red"},
		{Name: "in", ConfigDir: inSyncDir, Color: "green"},
	}, nil)

	inSyncOnly := makeConfig([]config.Account{
		{Name: "in", ConfigDir: inSyncDir, Color: "green"},
	}, nil)
	if _, err := MergeAll(inSyncOnly, "", testBin); err != nil {
		t.Fatalf("seed in-sync: %v", err)
	}

	summary, err := OverallHookHealth(cfg, testBin)
	if err == nil {
		t.Fatal("expected non-nil error from OverallHookHealth when one account fails")
	}
	if len(summary.InSync) != 1 || summary.InSync[0] != "in" {
		t.Errorf("expected InSync=[in] in partial summary, got %v", summary.InSync)
	}
	for _, name := range summary.InSync {
		if name == "broken" {
			t.Error("failing account leaked into InSync bucket")
		}
	}
	for _, name := range summary.Drifted {
		if name == "broken" {
			t.Error("failing account leaked into Drifted bucket")
		}
	}
	for _, name := range summary.Missing {
		if name == "broken" {
			t.Error("failing account leaked into Missing bucket")
		}
	}
}
