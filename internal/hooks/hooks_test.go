package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	testRoot, err := os.MkdirTemp("", "agentmux-hooks-test-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_STATE_HOME", filepath.Join(testRoot, "state")); err != nil {
		_ = os.RemoveAll(testRoot)
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(testRoot)
	os.Exit(code)
}

// currentUmask reads (and restores) the process umask so a fresh-write
// permissions test can predict the actual mode that os.OpenFile applied.
// Tests should call this from a single goroutine since umask is a
// process-wide setting.
func currentUmask() os.FileMode {
	u := syscall.Umask(0)
	syscall.Umask(u)
	return os.FileMode(u)
}

const testBin = "/usr/local/bin/amx"

func makeConfig(accounts []config.Account, overrides map[string]config.AccountHookOverride) *config.Config {
	cfg := &config.Config{Accounts: accounts}
	if overrides != nil {
		cfg.Hooks.Overrides = overrides
	}
	return cfg
}

// helpers

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshaling JSON: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func checkHookCommand(t *testing.T, hooks map[string]any, event, wantCmd string) {
	t.Helper()
	groups, ok := hooks[event].([]any)
	if !ok || len(groups) == 0 {
		t.Errorf("no hook groups for event %q", event)
		return
	}
	for _, g := range groups {
		group := g.(map[string]any)
		entries := group["hooks"].([]any)
		for _, e := range entries {
			entry := e.(map[string]any)
			cmd, _ := entry["command"].(string)
			if strings.Contains(cmd, "hook-indicator") {
				if cmd != wantCmd {
					t.Errorf("event %q: got command %q, want %q", event, cmd, wantCmd)
				}
				return
			}
		}
	}
	t.Errorf("no amx hook found for event %q", event)
}
