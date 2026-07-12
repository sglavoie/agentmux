package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
)

func TestUITestDriverSeedsHermeticTranscriptPlanAndUnifiedServices(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "operator-sentinel-home"))
	fx, err := UITestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := NewUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	snapshot, ready := d.model.dataServices.Snapshot()
	if !ready {
		t.Fatal("fixture data services are not ready")
	}
	for label, path := range map[string]string{
		"datastore":  d.model.dataServices.store.Path(),
		"transcript": snapshot.transcriptIndex.Path(),
		"plans":      snapshot.planIndex.Path(),
	} {
		if !strings.HasPrefix(path, d.tempRoot+string(filepath.Separator)) {
			t.Fatalf("%s path %q escaped fixture root %q", label, path, d.tempRoot)
		}
	}

	if err := d.Key("S"); err != nil {
		t.Fatalf("enter session search: %v", err)
	}
	if got := d.Model().sessionResults; len(got) != 1 || got[0].Meta.ID != "fixture-conversation" {
		t.Fatalf("transcript fixture results = %+v", got)
	}
	if err := d.Key("q"); err != nil {
		t.Fatal(err)
	}
	if err := d.Key("P"); err != nil {
		t.Fatalf("enter plan search: %v", err)
	}
	if got := d.Model().planResults; len(got) != 1 || got[0].Meta.Name != "fixture plan" {
		t.Fatalf("plan fixture results = %+v", got)
	}
	if err := d.Key("enter"); err != nil {
		t.Fatalf("enter plan view: %v", err)
	}
	if got := strings.Join(d.Model().planViewerLines, "\n"); !strings.Contains(got, "Deterministic UI harness content") {
		t.Fatalf("plan view did not settle on fixture content: %q", got)
	}

	results, err := snapshot.unifiedSearch.Search(context.Background(), unifiedsearch.Query{Text: "deterministic"})
	if err != nil {
		t.Fatalf("unified fixture search: %v", err)
	}
	seen := map[unifiedsearch.EntityType]bool{}
	for _, result := range results {
		seen[result.EntityType] = true
	}
	if !seen[unifiedsearch.EntityConversation] || !seen[unifiedsearch.EntityPlan] {
		t.Fatalf("unified fixture search types = %+v", seen)
	}
}

func TestUITestDriverFailsFastInsteadOfOpeningDefaultDataServices(t *testing.T) {
	d := NewUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)
	services := d.model.dataServices
	services.stateMu.Lock()
	services.ready = false
	services.stateMu.Unlock()
	_, err := services.Initialize(context.Background(), d.model.cfg)
	if !errors.Is(err, errUITestLiveDataAccess) {
		t.Fatalf("Initialize error = %v, want hermetic live-access failure", err)
	}
}

func TestUITestDriverTickUsesDeterministicDaemonAndDiscoveryDependencies(t *testing.T) {
	d := NewUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)
	d.model.mode = ModeDetail
	d.model.detailWindowID = 42
	d.Tick(1)
	if err := d.Err(); err != nil {
		t.Fatalf("deterministic tick: %v", err)
	}
	if d.model.detailErr == nil || !strings.Contains(d.model.detailErr.Error(), "live daemon access is disabled") {
		t.Fatalf("detail daemon guard = %v", d.model.detailErr)
	}
}

func TestUITestDriverActionDaemonFailsFast(t *testing.T) {
	d := NewUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)
	if err := d.drain(d.model.clearStatusCmd(makeSessions()[0])); err != nil {
		t.Fatalf("clear status command: %v", err)
	}
	if err := d.Err(); err == nil || !strings.Contains(err.Error(), "live daemon access is disabled") {
		t.Fatalf("clear-status daemon guard = %v", err)
	}
}

func TestUITestScenariosRepeatWithoutSQLiteCleanupFaults(t *testing.T) {
	for i := 0; i < 12; i++ {
		for _, actions := range [][]UITestAction{
			{{Type: UITestActionKey, Key: "P"}, {Type: UITestActionKey, Key: "enter"}},
			{{Type: UITestActionKey, Key: "S"}},
		} {
			observations, err := RunUITestFixture("basic", UITestOptions{}, actions)
			if err != nil {
				t.Fatalf("iteration %d: %v", i, err)
			}
			if len(observations) != len(actions)+1 {
				t.Fatalf("iteration %d: observations=%d", i, len(observations))
			}
		}
	}
}
