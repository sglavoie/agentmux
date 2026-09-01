package aslbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// The stand-in plan corpus the live-bridge gate runs against, and the launcher
// that points a Manager at the installed ASL. The gate itself is in
// integration_test.go.

const integrationEnv = "AMX_ASL_INTEGRATION"

// aslCorpus is a throwaway plan/state root holding one paused run with an open
// question and one running run. Everything the bridge reads is inside it, and
// the environment overrides below keep the bridge from discovering the
// operator's real plans.
type aslCorpus struct {
	root      string
	plansRoot string
	stateRoot string
	paused    string
	running   string
}

func requireIntegration(t *testing.T) string {
	t.Helper()
	if os.Getenv(integrationEnv) == "" {
		t.Skipf("set %s=1 to run against the installed agent-session-loop", integrationEnv)
	}
	command := os.Getenv("AMX_ASL_COMMAND")
	if command == "" {
		command = "asl"
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		t.Fatalf("%s is set but %q is not installed: %v", integrationEnv, command, err)
	}
	return resolved
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	writeFile(t, path, string(encoded)+"\n")
}

// planStateDir mirrors agent_session_loop's own addressing: the per-plan state
// directory is the SHA-256 of the resolved plan path. Resolving symlinks first
// matters on macOS, where the temporary root is reached through /var but
// resolves to /private/var.
func planStateDir(t *testing.T, stateRoot, planDir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(planDir)
	if err != nil {
		t.Fatalf("resolve %s: %v", planDir, err)
	}
	sum := sha256.Sum256([]byte(resolved))
	return filepath.Join(stateRoot, hex.EncodeToString(sum[:]))
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

func writePlanFiles(t *testing.T, planDir, title string) {
	t.Helper()
	writeFile(t, filepath.Join(planDir, "context.md"), "# Context\n\nAgent Mux integration fixture.\n")
	writeFile(t, filepath.Join(planDir, "manifest.md"), "# Manifest\n\n- session-1\n")
	writeFile(t, filepath.Join(planDir, "session-1.md"), fmt.Sprintf(
		"# Session 1: %s\n\nFixture session.\n\n## Handoff\n\n- **Status**: pending\n- **Notes**:\n",
		title,
	))
}

func buildCorpus(t *testing.T) *aslCorpus {
	t.Helper()
	root := t.TempDir()
	corpus := &aslCorpus{
		root:      root,
		plansRoot: filepath.Join(root, "plans"),
		stateRoot: filepath.Join(root, "state"),
	}
	if err := os.MkdirAll(corpus.stateRoot, 0o755); err != nil {
		t.Fatalf("mkdir state root: %v", err)
	}

	pausedDir := filepath.Join(corpus.plansRoot, "plan-alpha")
	writePlanFiles(t, pausedDir, "Paused fixture")
	corpus.paused = resolvedPath(t, pausedDir)
	pausedState := planStateDir(t, corpus.stateRoot, pausedDir)
	lastMessage := filepath.Join(pausedState, "last-message.txt")
	writeFile(t, lastMessage, "Should we drop the v1 column before the backfill lands?\n")
	writeJSON(t, filepath.Join(pausedState, "state.json"), map[string]any{
		"plan_dir":       corpus.paused,
		"paused":         true,
		"pause_reason":   "agent-question",
		"run_id":         "20260619T140000Z-1",
		"harness":        "claude",
		"run_started_at": "2026-06-19T14:00:00+00:00",
		"turns": []map[string]any{{
			"session_id":        "1",
			"turn_index":        1,
			"started_at":        "2026-06-19T14:00:00+00:00",
			"last_message_path": lastMessage,
		}},
	})

	runningDir := filepath.Join(corpus.plansRoot, "plan-beta")
	writePlanFiles(t, runningDir, "Running fixture")
	corpus.running = resolvedPath(t, runningDir)
	runningState := planStateDir(t, corpus.stateRoot, runningDir)
	activeTurn := map[string]any{
		"session_id": "1",
		"turn_index": 1,
		"started_at": "2026-06-19T15:00:00+00:00",
	}
	writeJSON(t, filepath.Join(runningState, "state.json"), map[string]any{
		"plan_dir":       corpus.running,
		"paused":         false,
		"run_id":         "20260619T150000Z-1",
		"harness":        "codex",
		"run_started_at": "2026-06-19T15:00:00+00:00",
		"active_turn":    activeTurn,
		"turns":          []map[string]any{activeTurn},
	})
	// The lock names this test process, which is alive by definition, so the
	// projection classifies the run as running without anything being spawned.
	writeJSON(t, filepath.Join(runningState, "lock.json"), map[string]any{
		"created_at": "2026-06-19T15:00:00+00:00",
		"pid":        os.Getpid(),
		"plan_dir":   corpus.running,
	})
	return corpus
}

// launcher starts the installed bridge against the corpus only. The state and
// plan flags bound what the bridge reads; the environment overrides bound what
// it would otherwise discover through the operator's XDG directories.
func (c *aslCorpus) launcher(executable string, exitCode *atomic.Int32) Launcher {
	return func(ctx context.Context) (*Child, error) {
		args := append(append([]string(nil), BridgeArgs...),
			"--state-dir", c.stateRoot,
			"--plan-root", c.plansRoot,
		)
		cmd := exec.Command(executable, args...)
		cmd.Env = append(os.Environ(),
			"XDG_STATE_HOME="+filepath.Join(c.root, "xdg-state"),
			"XDG_DATA_HOME="+filepath.Join(c.root, "xdg-data"),
			"XDG_CONFIG_HOME="+filepath.Join(c.root, "xdg-config"),
			"AGENT_PLANS_DIR="+c.plansRoot,
			"TMPDIR="+filepath.Join(c.root, "tmp"),
		)
		for _, dir := range []string{"xdg-state", "xdg-data", "xdg-config", "tmp"} {
			if err := os.MkdirAll(filepath.Join(c.root, dir), 0o755); err != nil {
				return nil, err
			}
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &Child{
			Path:   executable,
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
			Wait: func() error {
				err := cmd.Wait()
				exitCode.Store(int32(cmd.ProcessState.ExitCode()))
				return err
			},
			Terminate: func() error { return cmd.Process.Kill() },
		}, nil
	}
}

func rowFor(t *testing.T, snapshot Snapshot, planDir string) Row {
	t.Helper()
	for _, row := range snapshot.Rows {
		if row.PlanDir == planDir {
			return row
		}
	}
	t.Fatalf("no row for %s in %d rows", planDir, len(snapshot.Rows))
	return Row{}
}
