package events

// The lifecycle-hook gate checked from the corpus direction: every canonical
// payload ASL publishes must pass, and the closed vocabulary the gate switches
// on must be the one the schema declares rather than a second copy of it
// maintained by hand. The daemon's behaviour when the gate closes is checked
// beside the ingestion path itself, in the daemon package.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// lifecycleFixture is one vendored payload with its event name lifted out. The
// name travels beside the payload on the wire — ASL passes it as the hook
// argument — so the fixture carries it inside the document and a consumer
// splits the two the way the daemon's request does.
type lifecycleFixture struct {
	name    string
	event   string
	payload hookevent.HookPayload
	raw     []byte
	// decodeErr is set when the document does not survive the decoder the daemon
	// actually uses. That is a refusal too — an earlier one than the gate — and a
	// fixture that hits it must be recorded as such rather than skipped.
	decodeErr error
}

func lifecycleFixtures(t *testing.T, validity string) []lifecycleFixture {
	t.Helper()
	fixtures, err := aslcontract.ReadFixtures(aslcontract.FixturesLifecycleHook, validity)
	if err != nil {
		t.Fatalf("read %s lifecycle fixtures: %v", validity, err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no %s lifecycle fixtures are vendored", validity)
	}
	out := make([]lifecycleFixture, 0, len(fixtures))
	for _, fixture := range fixtures {
		if fixture.Root != "hook_payload" {
			t.Fatalf("%s: root = %q, want hook_payload", fixture.Path, fixture.Root)
		}
		// A fixture cannot embed the digest of its own bytes, so the corpus spells
		// the identity with a placeholder and a consumer substitutes its own before
		// treating the document as live wire.
		raw := []byte(strings.ReplaceAll(string(fixture.Document),
			aslcontract.PlaceholderContractSetID, aslcontract.ContractSetID))
		var envelope struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("%s: %v", fixture.Path, err)
		}
		payload, decodeErr := hookevent.DecodeHookPayload(strings.NewReader(string(raw)))
		out = append(out, lifecycleFixture{
			name: fixture.Name, event: envelope.Event,
			payload: payload, raw: raw, decodeErr: decodeErr,
		})
	}
	return out
}

func lifecycleSchema(t *testing.T) aslcontract.Schema {
	t.Helper()
	schema, err := aslcontract.ReadSchema(aslcontract.FixturesLifecycleHook)
	if err != nil {
		t.Fatalf("read lifecycle schema: %v", err)
	}
	return schema
}

func TestTheLifecycleContractPinsWhatThisDaemonIngests(t *testing.T) {
	schema := lifecycleSchema(t)
	manifest, err := aslcontract.ReadManifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	set, err := manifest.Current()
	if err != nil {
		t.Fatalf("current contract set: %v", err)
	}

	if set.ContractSetID != aslcontract.ContractSetID {
		t.Errorf("current set = %q, this build ingests %q", set.ContractSetID, aslcontract.ContractSetID)
	}
	component, ok := set.Components[aslcontract.ComponentLifecycleHook]
	if !ok {
		t.Fatalf("contract set publishes no %s component", aslcontract.ComponentLifecycleHook)
	}
	if component.SchemaVersion != ASLHookSchemaVersion {
		t.Errorf("lifecycle schema version = %d, this daemon ingests %d",
			component.SchemaVersion, ASLHookSchemaVersion)
	}
	if schema.Version != ASLHookSchemaVersion {
		t.Errorf("schema version = %d, this daemon ingests %d", schema.Version, ASLHookSchemaVersion)
	}
	if schema.MaxDocumentBytes <= 0 {
		t.Errorf("schema declares no document ceiling")
	}
}

// TestTheGatedVocabularyIsTheDeclaredOne keeps the Go event list from drifting
// from ASL's. The gate is a membership test, so a boundary missing from this map
// is not refused — it is waved through ungated, which is the failure that would
// be hardest to notice in production.
func TestTheGatedVocabularyIsTheDeclaredOne(t *testing.T) {
	declared, err := lifecycleSchema(t).Enum("EventName")
	if err != nil {
		t.Fatalf("%v", err)
	}
	mine := make([]string, 0, len(aslLifecycleHookEvents))
	for event := range aslLifecycleHookEvents {
		mine = append(mine, event)
	}
	sort.Strings(mine)
	want := append([]string(nil), declared...)
	sort.Strings(want)

	if fmt.Sprint(mine) != fmt.Sprint(want) {
		t.Errorf("gated events = %v, schema declares %v", mine, want)
	}
	for _, event := range declared {
		if !IsAgentSessionLoopHookEvent(event) {
			t.Errorf("declared boundary %q is not gated", event)
		}
	}
	// The narrower list is the set of boundaries that carry the loop marker. It is
	// a subset on purpose: the mailbox boundaries are lifecycle payloads under the
	// same schema but are emitted by the CLI rather than by a running loop.
	for _, event := range []string{
		AgentSessionLoopMailboxQuestion,
		AgentSessionLoopMailboxReply,
		AgentSessionLoopMailboxResolution,
	} {
		if !IsAgentSessionLoopHookEvent(event) {
			t.Errorf("%q is not gated", event)
		}
		if IsAgentSessionLoopEvent(event) {
			t.Errorf("%q is claimed as a loop-marker boundary", event)
		}
	}
}

func TestEveryCanonicalLifecyclePayloadPassesTheGate(t *testing.T) {
	for _, fixture := range lifecycleFixtures(t, "valid") {
		if fixture.decodeErr != nil {
			t.Errorf("%s is canonical but the daemon's decoder refused it: %v",
				fixture.name, fixture.decodeErr)
			continue
		}
		if !IsAgentSessionLoopHookEvent(fixture.event) {
			t.Errorf("%s: %q is not a gated boundary", fixture.name, fixture.event)
			continue
		}
		if fixture.payload.Source != ASLHookSource {
			t.Errorf("%s: source = %q, want %q", fixture.name, fixture.payload.Source, ASLHookSource)
		}
		if refusal := ASLHookContractRefusal(fixture.event, fixture.payload); refusal != "" {
			t.Errorf("%s is canonical but the gate refused it: %s", fixture.name, refusal)
		}
	}
}

// TestEveryInvalidLifecycleFixtureIsRefusedAtItsOwnLayer runs the negative
// corpus and records which of the three refusals each case actually meets.
//
// The layers are ordered and only one of them is this gate's. The decoder runs
// first and rejects a document whose types are wrong; the gate runs next and
// answers exactly two questions, is this a schema I ingest and is this identity
// mine; the schema owns everything left — string bounds, path formats, the
// closed event vocabulary, prose where none belongs — and ASL's own suite
// enforces those over these same bytes.
//
// Assigning each fixture to a layer is the point. A case this build silently
// accepts at every layer is a real gap, and the closed switch at the end means a
// reason category ASL adds later has nowhere to land unnoticed.
func TestEveryInvalidLifecycleFixtureIsRefusedAtItsOwnLayer(t *testing.T) {
	// The gate's own cases, named rather than inferred.
	gated := map[string]string{
		"missing-contract-set-id": DecisionReasonHookContractMismatch,
	}
	// Cases the daemon accepts and the schema refuses. Go's decoder ignores
	// unknown fields and has no opinion on string length or path shape, so
	// claiming a Go-side refusal for these would be a test passing for the wrong
	// cause. They are listed so the set stays a decision rather than an accident.
	schemaOnly := map[string]bool{
		"invalid-event":              true,
		"oversized-question-preview": true,
		"relative-plan-dir":          true,
		"reply-text-leak":            true,
		"unknown-field":              true,
		"zulu-timestamp":             true,
	}

	seen := make(map[string]bool)
	for _, fixture := range lifecycleFixtures(t, "invalid") {
		refusal := ""
		if fixture.decodeErr == nil {
			refusal = ASLHookContractRefusal(fixture.event, fixture.payload)
		}
		switch {
		case gated[fixture.name] != "":
			if fixture.decodeErr != nil {
				t.Errorf("%s: the decoder refused it before the gate could: %v",
					fixture.name, fixture.decodeErr)
			}
			if refusal != gated[fixture.name] {
				t.Errorf("%s: gate said %q, want %q", fixture.name, refusal, gated[fixture.name])
			}
		case schemaOnly[fixture.name]:
			if refusal != "" {
				t.Errorf("%s: the gate refused a case it does not own, as %q", fixture.name, refusal)
			}
		case fixture.decodeErr != nil:
			// Refused by the decoder, which the daemon turns into invalid_payload.
		default:
			t.Errorf("%s: declared %s at %s, but no layer refuses it",
				fixture.name, seenReason(fixture), fixture.name)
		}
		seen[fixture.name] = true
	}

	for name := range gated {
		if !seen[name] {
			t.Errorf("no invalid fixture exercises %s", name)
		}
	}
	for name := range schemaOnly {
		if !seen[name] {
			t.Errorf("schema-only case %q is no longer in the corpus", name)
		}
	}

	// The event vocabulary is the schema's to enforce, not the gate's: an unknown
	// boundary is not a lifecycle event this build refuses, it is one it never
	// recognizes as a loop event at all.
	if IsAgentSessionLoopHookEvent("AgentSessionLoopTurnRetried") {
		t.Error("an undeclared boundary is gated")
	}
}

func seenReason(fixture lifecycleFixture) string {
	if fixture.decodeErr != nil {
		return fixture.decodeErr.Error()
	}
	return "no refusal"
}
