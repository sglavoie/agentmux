//go:build tuitest

package tuitest

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/tui"
)

// sentinelHomeDir is a home directory holding the user files a harness run
// must never touch: launch overrides whose entries name no fixture project (so
// a run that loaded them would prune them), a transcript cache, and a config.
func sentinelHomeDir(t *testing.T) (string, map[string]string) {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		".local/share/agent-mux/launch_overrides.json": `{"version":2,"entries":[{"project_title":"SENTINEL","profile_name":"sentinel"}],"pinned_projects":[{"profile_name":"sentinel","project_title":"SENTINEL"}]}` + "\n",
		".local/share/agent-mux/transcripts.json":      "SENTINEL\n",
		".config/agent-mux/config.toml":                "# SENTINEL\n",
	}
	for name, content := range files {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, files
}

// sentinelUnsetKeys are the variables that could point a run somewhere other
// than its HOME; a sentinel run has none of them set.
var sentinelUnsetKeys = []string{overrides.PathEnvVar, "AMX_CONFIG", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME"}

// sentinelHome makes a sentinel home this process's HOME for the rest of the
// test.
func sentinelHome(t *testing.T) (string, map[string]string) {
	t.Helper()
	home, files := sentinelHomeDir(t)
	// The run must find the home by itself: nothing may point it elsewhere.
	t.Setenv("HOME", home)
	for _, key := range sentinelUnsetKeys {
		old, had := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, old)
			}
		})
	}
	return home, files
}

func requireUntouched(t *testing.T, home string, files map[string]string) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(home, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	if len(found) != len(files) {
		t.Errorf("the home holds %v; want only the %d files it started with", found, len(files))
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want it byte for byte as it was", name, got, err)
		}
	}
}

// A fixture run builds a real model, and building a model loads the launch
// overrides and prunes what its projects do not name. Run against the
// operator's own home that emptied the operator's overrides, so every run,
// fixture or config, is isolated first.
func TestFixtureRunsLeaveTheUsersHomeAlone(t *testing.T) {
	home, files := sentinelHome(t)

	scenario := writeScenario(t, tui.UITestScenario{
		Name:    "isolated-fixture",
		Fixture: "basic",
		Size:    tui.UITestSize{Width: 120, Height: 40},
		Assertions: []tui.UITestAssertion{
			{Type: tui.UITestAssertNoLayoutIssues},
		},
	})
	if _, stderr, err := runRootArgs(t, "tui-test", "run", scenario, "--json"); err != nil {
		t.Fatalf("run: %v (stderr=%q)", err, stderr)
	}
	requireUntouched(t, home, files)

	if _, stderr, err := runRootArgs(t, "tui-test", "fixture", "basic", "--width", "120", "--height", "40", "--json"); err != nil {
		t.Fatalf("fixture: %v (stderr=%q)", err, stderr)
	}
	requireUntouched(t, home, files)

	if got := os.Getenv("HOME"); got != home {
		t.Fatalf("HOME after the runs = %q; want it restored to %q", got, home)
	}
}
