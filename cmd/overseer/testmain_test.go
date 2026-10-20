package overseer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/testenv"
)

// TestMain points the whole package at a config path that does not exist, so a
// test that forgets to stage its own configuration fails deterministically
// instead of quietly authorizing itself against the operator's real projects.
func TestMain(m *testing.M) {
	testenv.ClearSessionEnv()
	root, err := os.MkdirTemp("", "agentmux-overseer-test-config-")
	if err != nil {
		panic(err)
	}
	previous, hadPrevious := os.LookupEnv("AMX_CONFIG")
	if err := os.Setenv("AMX_CONFIG", filepath.Join(root, "absent-config.toml")); err != nil {
		panic(err)
	}
	code := m.Run()
	if hadPrevious {
		_ = os.Setenv("AMX_CONFIG", previous)
	} else {
		_ = os.Unsetenv("AMX_CONFIG")
	}
	_ = os.RemoveAll(root)
	os.Exit(code)
}
