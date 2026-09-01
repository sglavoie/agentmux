package aslbridge

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

// Loading the vendored corpus and decoding one fixture at a time. The tests
// that read these helpers live in protocol_fixtures_test.go and
// protocol_refusal_test.go.

func bridgeContract(t *testing.T) (aslcontract.ContractSet, aslcontract.Schema) {
	t.Helper()
	manifest, err := aslcontract.ReadManifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	set, err := manifest.Current()
	if err != nil {
		t.Fatalf("current contract set: %v", err)
	}
	schema, err := aslcontract.ReadSchema(aslcontract.FixturesBridgeProtocol)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return set, schema
}

func bridgeFixture(t *testing.T, validity, name string) aslcontract.Fixture {
	t.Helper()
	fixture, err := aslcontract.ReadFixture(aslcontract.FixturesBridgeProtocol, validity, name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return fixture
}

func bridgeFixtures(t *testing.T, validity string) []aslcontract.Fixture {
	t.Helper()
	fixtures, err := aslcontract.ReadFixtures(aslcontract.FixturesBridgeProtocol, validity)
	if err != nil {
		t.Fatalf("read %s fixtures: %v", validity, err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no %s bridge fixtures are vendored", validity)
	}
	return fixtures
}

// realized substitutes this build's contract-set ID for the placeholder a
// fixture carries. A fixture cannot embed the digest of its own bytes, so the
// corpus spells the field with a well-formed stand-in and a consumer that wants
// to compare a fixture against live output swaps it first.
func realized(doc json.RawMessage) []byte {
	return bytes.ReplaceAll(doc, []byte(aslcontract.PlaceholderContractSetID), []byte(ContractSetID))
}

type fixtureResponse struct {
	Type     string          `json:"type"`
	Protocol int             `json:"protocol"`
	ID       string          `json:"id"`
	OK       bool            `json:"ok"`
	Result   json.RawMessage `json:"result"`
	Error    *Error          `json:"error"`
}

type fixtureEvent struct {
	Type     string          `json:"type"`
	Protocol int             `json:"protocol"`
	Seq      uint64          `json:"seq"`
	Event    string          `json:"event"`
	Data     json.RawMessage `json:"data"`
}

type helloEnvelope struct {
	Type string `json:"type"`
	Hello
}

func decodeInto(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func decodeStrict(t *testing.T, name string, raw []byte, target any) {
	t.Helper()
	if err := decodeInto(raw, target); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

// decodeResponse loads one valid response fixture, checks the envelope, and
// decodes its result into target.
func decodeResponse(t *testing.T, name string, target any) fixtureResponse {
	t.Helper()
	fixture := bridgeFixture(t, "valid", name)
	var response fixtureResponse
	decodeStrict(t, fixture.Path, realized(fixture.Document), &response)
	if response.Type != "response" {
		t.Fatalf("%s: type = %q, want response", name, response.Type)
	}
	if response.Protocol != ProtocolVersion {
		t.Fatalf("%s: protocol = %d, want %d", name, response.Protocol, ProtocolVersion)
	}
	if response.ID == "" {
		t.Fatalf("%s: response carries no correlation id", name)
	}
	if target != nil {
		decodeStrict(t, name+" result", response.Result, target)
	}
	return response
}

func decodeEvent(t *testing.T, name string, target any) fixtureEvent {
	t.Helper()
	fixture := bridgeFixture(t, "valid", name)
	var frame fixtureEvent
	decodeStrict(t, fixture.Path, realized(fixture.Document), &frame)
	if frame.Type != "event" {
		t.Fatalf("%s: type = %q, want event", name, frame.Type)
	}
	if frame.Protocol != ProtocolVersion {
		t.Fatalf("%s: protocol = %d, want %d", name, frame.Protocol, ProtocolVersion)
	}
	if frame.Seq == 0 {
		t.Fatalf("%s: event seq is zero", name)
	}
	if target != nil {
		decodeStrict(t, name+" data", frame.Data, target)
	}
	return frame
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------
// Vocabulary
