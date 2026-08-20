package aslbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This file is the only place Agent Mux talks to a real agent-session-loop. It
// runs when AMX_ASL_INTEGRATION is set, which `just asl-integration` does, and
// is skipped otherwise so `go test ./...` stays hermetic and offline.
//
// When it is requested it must never skip. An ASL that is absent, too old, or
// speaking a different protocol is exactly the incompatibility this gate exists
// to catch, so those conditions fail rather than quietly reporting success.

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

// TestInstalledBridgeServesProtocolOne is the whole opt-in gate: one manager,
// one real child, and every shape the daemon depends on checked against it.
func TestInstalledBridgeServesProtocolOne(t *testing.T) {
	executable := requireIntegration(t)
	corpus := buildCorpus(t)

	var exitCode atomic.Int32
	exitCode.Store(-1)
	manager := New(Options{
		Mode:     ModeRequired,
		Command:  executable,
		Launcher: corpus.launcher(executable, &exitCode),
		Logger:   discardLogger(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	manager.Start(ctx)

	waitFor(t, "the installed bridge to become mutable", func() bool {
		return manager.Status().MutationsEnabled
	})

	status := manager.Status()
	if status.Protocol != ProtocolVersion {
		t.Fatalf("handshake protocol = %d, want %d", status.Protocol, ProtocolVersion)
	}
	if status.Version == "" {
		t.Error("status reports no ASL version")
	}
	if status.Executable != executable {
		t.Errorf("status executable = %q, want %q", status.Executable, executable)
	}
	for _, required := range []string{
		MethodRunsSnapshot, MethodQuestionGet, MethodQuestionReply, MethodQuestionResolve,
		MethodRunResume, MethodRunStopAfterCurrent, MethodRunAbort, MethodWorkbenchEnsure,
		EventRunsChanged, EventOperationUpdated,
	} {
		if !hasCapability(status.Capabilities, required) {
			t.Errorf("installed bridge does not advertise %s", required)
		}
	}
	// The checked-in wire fixtures are only worth decoding if they still
	// describe the ASL that is actually installed, so this gate is where a
	// fixture set that has fallen behind the bridge is caught.
	if fmt.Sprint(status.Capabilities) != fmt.Sprint(loadManifest(t).Capabilities) {
		t.Errorf("installed capabilities = %v, fixtures pin %v",
			status.Capabilities, loadManifest(t).Capabilities)
	}

	snapshot, meta, err := manager.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if meta.Stale {
		t.Error("a live handshake served a stale snapshot")
	}
	if snapshot.Protocol != ProtocolVersion || snapshot.Digest == "" {
		t.Fatalf("snapshot = protocol %d digest %q", snapshot.Protocol, snapshot.Digest)
	}
	if snapshot.RowCount != len(snapshot.Rows) {
		t.Errorf("row_count = %d, rows = %d", snapshot.RowCount, len(snapshot.Rows))
	}

	paused := rowFor(t, snapshot, corpus.paused)
	if paused.PrimaryState != "paused-for-reply" {
		t.Errorf("paused row state = %q", paused.PrimaryState)
	}
	if paused.Question == nil {
		t.Fatal("paused row carries no question projection")
	}
	if paused.Question.ContextKind == "" {
		t.Error("question projection carries no context kind")
	}
	if paused.ActionToken == "" {
		t.Error("paused row carries no action token")
	}

	running := rowFor(t, snapshot, corpus.running)
	if running.PrimaryState != "running" {
		t.Errorf("running row state = %q", running.PrimaryState)
	}
	if running.Question != nil {
		t.Error("a running row must not project a question")
	}

	assertSnapshotIsBodyFree(t, snapshot)
	body := assertQuestionBodyIsFetchedNotProjected(t, ctx, manager, corpus, paused)
	assertStaleTokenIsRefusedWithoutWriting(t, ctx, manager, corpus, running)
	assertAcceptedMutationYieldsABodyFreeReceipt(t, ctx, manager, corpus, running)
	assertWorkbenchLinkIsLoopbackAndTokenized(t, ctx, manager, corpus, body)

	manager.Close()
	if code := exitCode.Load(); code != 0 {
		t.Errorf("bridge exited with %d after stdin close, want a clean 0", code)
	}
}

// The snapshot is the only ASL data the daemon caches, so it is the one place a
// body leak would become durable.
func assertSnapshotIsBodyFree(t *testing.T, snapshot Snapshot) {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, forbidden := range []string{"drop the v1 column", `"body"`, `"text"`, `"token"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("snapshot carries %q", forbidden)
		}
	}
}

func assertQuestionBodyIsFetchedNotProjected(
	t *testing.T, ctx context.Context, manager *Manager, corpus *aslCorpus, paused Row,
) QuestionBody {
	t.Helper()
	body, err := manager.QuestionBody(ctx, QuestionRequest{
		PlanDir:    corpus.paused,
		QuestionID: paused.Question.QuestionID,
	})
	if err != nil {
		t.Fatalf("question.get: %v", err)
	}
	if !strings.Contains(body.Body.Reveal(), "drop the v1 column") {
		t.Errorf("fetched body does not contain the fixture question")
	}
	if got := fmt.Sprintf("%v", body); strings.Contains(got, "drop the v1 column") {
		t.Errorf("formatting a QuestionBody leaked the answer prompt: %s", got)
	}
	if body.RowID != paused.RowID {
		t.Errorf("body row_id = %q, want %q", body.RowID, paused.RowID)
	}
	if body.ActionToken == "" {
		t.Error("question.get returned no action token")
	}
	return body
}

func assertStaleTokenIsRefusedWithoutWriting(
	t *testing.T, ctx context.Context, manager *Manager, corpus *aslCorpus, running Row,
) {
	t.Helper()
	_, err := manager.Act(ctx, ActionRequest{
		Method:      MethodRunStopAfterCurrent,
		PlanDir:     corpus.running,
		RunID:       running.RunID,
		OperationID: "amx-integration-stale",
		ActionToken: "0000000000000000000000000000dead",
	})
	var refusal *Error
	if !errors.As(err, &refusal) {
		t.Fatalf("stale token error = %v, want a typed bridge refusal", err)
	}
	if refusal.Code != CodeStaleActionToken {
		t.Errorf("stale token code = %q, want %q", refusal.Code, CodeStaleActionToken)
	}
	if got := sanitizeReason(err); got != "bridge refused: "+CodeStaleActionToken {
		t.Errorf("sanitized reason = %q, want the bare code", got)
	}
}

// Reissuing one operation ID must stay idempotent: the daemon never retries a
// mutation itself, but an operator repeating an action must not queue it twice.
func assertAcceptedMutationYieldsABodyFreeReceipt(
	t *testing.T, ctx context.Context, manager *Manager, corpus *aslCorpus, running Row,
) {
	t.Helper()
	request := ActionRequest{
		Method:      MethodRunStopAfterCurrent,
		PlanDir:     corpus.running,
		RunID:       running.RunID,
		OperationID: "amx-integration-stop",
		ActionToken: running.ActionToken,
	}
	receipt, err := manager.Act(ctx, request)
	if err != nil {
		t.Fatalf("run.stop_after_current: %v", err)
	}
	if receipt.Status != OperationAccepted {
		t.Errorf("receipt status = %q, want %q", receipt.Status, OperationAccepted)
	}
	if receipt.OperationID != request.OperationID || receipt.Method != request.Method {
		t.Errorf("receipt = %+v, want it to name the submitted operation", receipt)
	}

	repeat, err := manager.Act(ctx, request)
	if err != nil {
		t.Fatalf("repeating one operation id: %v", err)
	}
	if repeat.Status != receipt.Status || repeat.OperationID != receipt.OperationID {
		t.Errorf("repeat receipt = %+v, want the first receipt %+v", repeat, receipt)
	}

	receipts := manager.Receipts()
	seen := 0
	for _, held := range receipts {
		if held.OperationID == request.OperationID {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("receipt window holds %d entries for one operation id, want 1", seen)
	}
	encoded, err := json.Marshal(receipts)
	if err != nil {
		t.Fatalf("marshal receipts: %v", err)
	}
	if strings.Contains(string(encoded), "drop the v1 column") {
		t.Error("the receipt window carries question prose")
	}
}

func assertWorkbenchLinkIsLoopbackAndTokenized(
	t *testing.T, ctx context.Context, manager *Manager, corpus *aslCorpus, body QuestionBody,
) {
	t.Helper()
	link, err := manager.Workbench(ctx, WorkbenchRequest{PlanDir: corpus.paused, Focus: FocusReply})
	if err != nil {
		t.Fatalf("workbench.ensure: %v", err)
	}
	if !strings.HasPrefix(link.URL, "http://127.0.0.1:") {
		t.Errorf("workbench url = %q, want a loopback origin", link.URL)
	}
	if !strings.Contains(link.URL, "token=") {
		t.Error("workbench url carries no token")
	}
	if link.Focus != FocusReply {
		t.Errorf("workbench focus = %q, want %q", link.Focus, FocusReply)
	}
	if strings.Contains(link.URL, body.Body.Reveal()) {
		t.Error("workbench url carries the question body")
	}

	if _, err := manager.Workbench(ctx, WorkbenchRequest{PlanDir: corpus.paused, Focus: "nowhere"}); err == nil {
		t.Error("an undeclared focus was accepted")
	} else if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("undeclared focus error = %v, want ErrInvalidRequest", err)
	}
}
