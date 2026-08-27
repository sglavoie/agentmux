package cmd

// What an acceptance fixture has to say to be an agent-session-loop producer.
//
// The daemon gates lifecycle payloads on a compatibility block — a schema
// version it ingests and the contract-set ID it was built against — and refuses
// anything else. So a fixture that omits it is not testing an ASL event at all;
// it is testing the refusal path, silently, while claiming to test the pipeline.
// These helpers keep every acceptance producer speaking the contract the
// vendored corpus pins.

import (
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

const aslAcceptanceSource = "agent_session_loop"

// aslAcceptanceSchemaVersion is read from the vendored contract set rather than
// written as a literal, so a corpus that moves the lifecycle schema forward
// moves these fixtures with it instead of leaving them pinned to a version the
// daemon has stopped ingesting.
func aslAcceptanceSchemaVersion(t *testing.T) int {
	t.Helper()
	manifest, err := aslcontract.ReadManifest()
	if err != nil {
		t.Fatalf("read the vendored manifest: %v", err)
	}
	set, err := manifest.Current()
	if err != nil {
		t.Fatalf("current contract set: %v", err)
	}
	component, ok := set.Components[aslcontract.ComponentLifecycleHook]
	if !ok {
		t.Fatalf("the contract set publishes no %s component", aslcontract.ComponentLifecycleHook)
	}
	if component.SchemaVersion == 0 {
		t.Fatal("the contract set pins no lifecycle schema version")
	}
	return component.SchemaVersion
}

// stampASLContract adds the compatibility block to a payload that declares
// itself an agent-session-loop producer, and leaves every other payload alone.
// Harness hooks carry no such block and are not gated on one.
func stampASLContract(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	if payload == nil || payload["source"] != aslAcceptanceSource {
		return payload
	}
	if _, ok := payload["schema_version"]; !ok {
		payload["schema_version"] = aslAcceptanceSchemaVersion(t)
	}
	if _, ok := payload["contract_set_id"]; !ok {
		payload["contract_set_id"] = aslcontract.ContractSetID
	}
	return payload
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return encoded
}
