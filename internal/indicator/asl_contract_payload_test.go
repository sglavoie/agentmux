package indicator

// The forwarding step's obligation to the lifecycle contract.
//
// This process stands between ASL's hook and the daemon that gates it, and the
// one thing it does to a payload on the way through is compact it when it is
// too large to forward whole. Compaction is lossy by design — it keeps the
// fields the reducer needs and drops the rest — so it is the single place where
// a size limit could turn into a compatibility failure the operator would read
// as a version mismatch.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// TestALifecyclePayloadIsTooSmallToEverBeCompacted is the primary guarantee:
// the contract caps a lifecycle payload far below the size that triggers
// compaction, so the lossy path is unreachable for this producer. Both bounds
// are constants, so this holds for every payload rather than for the ones a test
// happens to build.
func TestALifecyclePayloadIsTooSmallToEverBeCompacted(t *testing.T) {
	t.Parallel()

	schema, err := aslcontract.ReadSchema(aslcontract.FixturesLifecycleHook)
	if err != nil {
		t.Fatalf("read lifecycle schema: %v", err)
	}
	if schema.MaxDocumentBytes <= 0 {
		t.Fatal("the lifecycle schema declares no document ceiling")
	}
	if schema.MaxDocumentBytes >= maxRawHookPayloadBytes {
		t.Errorf("a lifecycle payload may reach %d bytes and compaction starts at %d",
			schema.MaxDocumentBytes, maxRawHookPayloadBytes)
	}
}

// TestCompactionCarriesTheCompatibilityBlock is the fallback for the day the
// bounds above stop holding. Unreachable today is not the same as safe, and a
// compacted payload that arrived without its contract identity would be
// quarantined by the daemon as incompatible — a diagnosis pointing at the
// version rather than at the size that actually caused it.
func TestCompactionCarriesTheCompatibilityBlock(t *testing.T) {
	fixture, err := aslcontract.ReadFixture(aslcontract.FixturesLifecycleHook, "valid", "turn-started")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	raw := strings.ReplaceAll(string(fixture.Document),
		aslcontract.PlaceholderContractSetID, aslcontract.ContractSetID)
	payload, err := hookevent.DecodeHookPayload(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if payload.SchemaVersion == 0 || payload.ContractSetID == "" {
		t.Fatalf("fixture carries no compatibility block: %+v", payload)
	}

	compact, err := hookevent.CompactHookPayload(payload)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	var got struct {
		SchemaVersion int    `json:"schema_version"`
		ContractSetID string `json:"contract_set_id"`
	}
	if err := json.Unmarshal(compact, &got); err != nil {
		t.Fatalf("decode compacted payload: %v", err)
	}
	if got.SchemaVersion != payload.SchemaVersion {
		t.Errorf("compacted schema_version = %d, want %d", got.SchemaVersion, payload.SchemaVersion)
	}
	if got.ContractSetID != payload.ContractSetID {
		t.Errorf("compacted contract_set_id = %q, want %q", got.ContractSetID, payload.ContractSetID)
	}
}
