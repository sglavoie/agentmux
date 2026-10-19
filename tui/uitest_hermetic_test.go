package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
)

func TestUITestDriverSeedsHermeticFilesystemIndexes(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "operator-sentinel-home"))
	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)

	for label, path := range map[string]string{
		"transcript": d.model.transcriptIndex.Path(),
		"plans":      d.model.plansIndex.Path(),
	} {
		if !strings.HasPrefix(path, d.tempRoot+string(filepath.Separator)) {
			t.Fatalf("%s path %q escaped fixture root %q", label, path, d.tempRoot)
		}
	}
	databasePath := filepath.Join(d.tempRoot, "data", "agent-mux.db")
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("unexpected fixture database %s: %v", databasePath, err)
	}

	if err := d.Command(commands.SearchAllSessions); err != nil {
		t.Fatalf("enter session search: %v", err)
	}
	if got := d.Model().sessionResults; len(got) != 1 || got[0].Meta.ID != "fixture-conversation" {
		t.Fatalf("transcript fixture results = %+v", got)
	}
	if err := d.Key("q"); err != nil {
		t.Fatal(err)
	}
	if err := d.Command(commands.SearchPlans); err != nil {
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

}

func TestUITestDriverDoesNotCreateDefaultDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := newUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)
	for _, path := range []string{
		filepath.Join(home, ".local", "share", "agent-mux", "agent-mux.db"),
		filepath.Join(d.tempRoot, "data", "agent-mux.db"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected database %s: %v", path, err)
		}
	}
}

func TestUITestDriverTickUsesDeterministicDaemonAndDiscoveryDependencies(t *testing.T) {
	d := newUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
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
	d := newUITestDriverModel(testModelWithSessions(), UITestSize{Width: 80, Height: 24})
	cleanupUITestDriver(t, d)
	t.Setenv("KITTY_LISTEN_ON", "unix:/kitty-test")
	row := makeSessions()[0]
	row.WindowID = 42
	if err := d.drain(d.model.clearStatusCmd(row)); err != nil {
		t.Fatalf("clear status command: %v", err)
	}
	if err := d.Err(); err == nil || !strings.Contains(err.Error(), "live daemon access is disabled") {
		t.Fatalf("clear-status daemon guard = %v", err)
	}
}

func TestUITestScenariosRepeatWithoutFilesystemCleanupFaults(t *testing.T) {
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
