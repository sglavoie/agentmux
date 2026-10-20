package aslbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/testutil"
)

// This file is the only place Agent Mux talks to a real agent-session-loop. It
// runs when AMX_ASL_INTEGRATION is set, which `just asl-integration` does, and
// is skipped otherwise so `go test ./...` stays hermetic and offline.
//
// When it is requested it must never skip. An ASL that is absent, too old, or
// speaking a different protocol is exactly the incompatibility this gate exists
// to catch, so those conditions fail rather than quietly reporting success.

// TestInstalledBridgeServesTheContractSet is the whole opt-in gate: one manager,
// one real child, and every shape the daemon depends on checked against it.
//
// It is also the live half of the vendoring check. internal/aslcontract proves
// offline that the vendored copy agrees with itself; only a running ASL can
// prove the copy is still the one ASL publishes, and it does so by naming the
// same contract-set ID the vendored bytes derive.
func TestInstalledBridgeServesTheContractSet(t *testing.T) {
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

	testutil.Eventually(t, "the installed bridge to become mutable", func() bool {
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
	assertInstalledBridgeNamesTheVendoredCorpus(t, manager, status)

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
	// The identity travels inside the projection, so the snapshot the daemon
	// acted on cannot have been built under a different set than the hello named.
	if snapshot.ContractSetID != ContractSetID {
		t.Errorf("snapshot contract_set_id = %q, want %q", snapshot.ContractSetID, ContractSetID)
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

// assertInstalledBridgeNamesTheVendoredCorpus is the cross-repository half of
// the byte-identity gate.
//
// Offline, internal/aslcontract re-derives the contract-set ID from the copy it
// embeds, which catches a vendored file edited on this side. That check cannot
// catch the opposite drift: a copy that is internally consistent but older than
// what ASL now publishes passes it. Here the installed bridge names its own ID,
// derived from its own corpus, so the two failures are separated — a locally
// edited fixture fails `just check`, and a stale copy fails here.
func assertInstalledBridgeNamesTheVendoredCorpus(t *testing.T, manager *Manager, status Status) {
	t.Helper()
	derived, err := aslcontract.DerivedContractSetID()
	if err != nil {
		t.Fatalf("derive the vendored contract set: %v", err)
	}
	if derived != ContractSetID {
		t.Fatalf("vendored corpus derives %q, this build pins %q", derived, ContractSetID)
	}
	if status.ContractSetID != derived {
		t.Fatalf("installed bridge serves %q, the vendored corpus derives %q",
			status.ContractSetID, derived)
	}
	assertTheDigestSpansEveryComponent(t)

	manifest, err := aslcontract.ReadManifest()
	if err != nil {
		t.Fatalf("read the vendored manifest: %v", err)
	}
	set, err := manifest.Current()
	if err != nil {
		t.Fatalf("current contract set: %v", err)
	}

	manager.mu.Lock()
	hello := manager.hello
	manager.mu.Unlock()

	// Protocol 2 publishes three closed lists rather than one mixed one, so
	// "can I call this?", "will I be sent this?", and "does this need a
	// handshake?" are separately answerable. Each is checked whole and in order.
	for _, check := range []struct {
		what   string
		served []string
		pinned []string
	}{
		{"methods", status.Capabilities, set.Capabilities.BridgeMethods},
		{"events", hello.Events, set.Capabilities.BridgeEvents},
		{"mutation methods", hello.MutationMethods, set.Capabilities.BridgeMutationMethods},
	} {
		if fmt.Sprint(check.served) != fmt.Sprint(check.pinned) {
			t.Errorf("installed %s = %v, the vendored corpus pins %v",
				check.what, check.served, check.pinned)
		}
	}
	if !hello.HandshakeRequired {
		t.Error("the installed bridge does not require a handshake")
	}
}

// assertTheDigestSpansEveryComponent is what lets one equality above stand for
// all three contracts.
//
// The bridge is the only component with a live connection to check, but the
// contract-set ID is a digest over the whole corpus — bridge frames, lifecycle
// payloads, and projection documents alike. So an installed ASL that names the
// vendored ID has thereby agreed to the lifecycle-hook fixtures byte for byte,
// even though no hook is piped anywhere in this test. That inference only holds
// while every component's fixtures are inside the digested material, which is
// what this checks.
func assertTheDigestSpansEveryComponent(t *testing.T) {
	t.Helper()
	files, err := aslcontract.CorpusFiles()
	if err != nil {
		t.Fatalf("list the vendored corpus: %v", err)
	}
	for _, component := range []string{
		aslcontract.FixturesBridgeProtocol,
		aslcontract.FixturesLifecycleHook,
		aslcontract.FixturesProjection,
	} {
		prefix := "fixtures/" + component + "/"
		found := false
		for _, file := range files {
			if strings.HasPrefix(file, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no %s fixture is inside the digested corpus; the served ID does not vouch for it",
				component)
		}
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
