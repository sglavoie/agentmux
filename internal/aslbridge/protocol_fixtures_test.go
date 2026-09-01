package aslbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

// The fixtures behind these tests are ASL's, vendored byte-for-byte at
// internal/aslcontract. Running them here is the Go half of the cross-language
// contract: every canonical frame must decode into this package's types with
// DisallowUnknownFields, and every closed vocabulary this package spells as Go
// constants must be the one ASL's schema declares. A field the bridge starts
// sending that this package has no home for fails here rather than being
// silently dropped, and a constant list that drifts from the schema fails here
// rather than at an operator's screen.
//
// What these tests deliberately do not do is re-validate the corpus against
// itself. Whether the vendored bytes are the ones ASL published is
// internal/aslcontract's question, answered offline by digest and live by the
// hello that just asl-integration checks.

func TestTheContractSetPinsTheProtocolThisPackageSpeaks(t *testing.T) {
	set, schema := bridgeContract(t)

	if set.ContractSetID != ContractSetID {
		t.Errorf("current set = %q, this build implements %q", set.ContractSetID, ContractSetID)
	}
	component, ok := set.Components[aslcontract.ComponentBridgeProtocol]
	if !ok {
		t.Fatalf("contract set publishes no %s component", aslcontract.ComponentBridgeProtocol)
	}
	if component.SchemaVersion != ProtocolVersion {
		t.Errorf("bridge schema version = %d, this package speaks %d",
			component.SchemaVersion, ProtocolVersion)
	}
	if schema.Version != ProtocolVersion {
		t.Errorf("schema version = %d, this package speaks %d", schema.Version, ProtocolVersion)
	}
	// The frame ceiling is a constant on both ends rather than a negotiated
	// value, because a ceiling only helps if it is known before parsing.
	if component.MaxDocumentBytes != MaxFrameBytes {
		t.Errorf("max_document_bytes = %d, want the frame ceiling %d",
			component.MaxDocumentBytes, MaxFrameBytes)
	}
	if schema.MaxDocumentBytes != MaxFrameBytes {
		t.Errorf("schema max_document_bytes = %d, want %d", schema.MaxDocumentBytes, MaxFrameBytes)
	}
}

func TestTheGoVocabulariesAreTheDeclaredOnes(t *testing.T) {
	set, schema := bridgeContract(t)

	goMethods := []string{
		MethodSessionHandshake,
		MethodRunsSnapshot,
		MethodQuestionGet,
		MethodQuestionReply,
		MethodQuestionResolve,
		MethodRunResume,
		MethodRunStopAfterCurrent,
		MethodRunAbort,
		MethodWorkbenchEnsure,
	}
	goEvents := []string{EventRunsChanged, EventOperationUpdated}

	// Protocol 2 publishes methods, events, and the mutating subset as three
	// closed lists. Order is part of the declaration: every surface that maps a
	// value to operator-facing text is audited against these, so a value added by
	// a newer bridge cannot fall through to a generic sentence unnoticed.
	for _, check := range []struct {
		what     string
		declared []string
		manifest []string
		mine     []string
	}{
		{"methods", mustEnum(t, schema, "Method"), set.Capabilities.BridgeMethods, goMethods},
		{"events", mustEnum(t, schema, "EventName"), set.Capabilities.BridgeEvents, goEvents},
		{"mutation methods", mustEnum(t, schema, "MutationMethod"), set.Capabilities.BridgeMutationMethods, MutationMethods},
		{"error codes", mustEnum(t, schema, "ErrorCode"), set.Capabilities.BridgeErrorCodes, ErrorCodes},
	} {
		if fmt.Sprint(check.manifest) != fmt.Sprint(check.declared) {
			t.Errorf("manifest %s = %v, schema declares %v", check.what, check.manifest, check.declared)
		}
		if fmt.Sprint(check.mine) != fmt.Sprint(check.declared) {
			t.Errorf("Go %s = %v, schema declares %v", check.what, check.mine, check.declared)
		}
	}

	for _, method := range MutationMethods {
		if !contains(goMethods, method) {
			t.Errorf("mutation method %q is not one of the methods", method)
		}
	}

	wantStatuses := []string{
		OperationAccepted,
		OperationConsumed,
		OperationRejected,
		OperationExpired,
		OperationSuperseded,
	}
	if got := mustEnum(t, schema, "OperationStatus"); fmt.Sprint(wantStatuses) != fmt.Sprint(got) {
		t.Errorf("Go operation statuses = %v, schema declares %v", wantStatuses, got)
	}

	wantFocus := []string{FocusReply, FocusVerification, FocusActive, FocusEvents}
	if got := mustEnum(t, schema, "Focus"); fmt.Sprint(wantFocus) != fmt.Sprint(got) {
		t.Errorf("Go workbench focus = %v, schema declares %v", wantFocus, got)
	}
}

func mustEnum(t *testing.T, schema aslcontract.Schema, definition string) []string {
	t.Helper()
	values, err := schema.Enum(definition)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return values
}

// ----------------------------------------------------------------
// Frame decoding

func TestEveryFixtureFitsOneLineWithinTheCeiling(t *testing.T) {
	for _, validity := range []string{"valid", "invalid"} {
		for _, fixture := range bridgeFixtures(t, validity) {
			var frame any
			if err := json.Unmarshal(fixture.Document, &frame); err != nil {
				t.Fatalf("%s: %v", fixture.Path, err)
			}
			encoded, err := json.Marshal(frame)
			if err != nil {
				t.Fatalf("%s: %v", fixture.Path, err)
			}
			if bytes.ContainsAny(encoded, "\n") {
				t.Errorf("%s: encoded frame carries a newline", fixture.Path)
			}
			if len(encoded)+1 > MaxFrameBytes {
				t.Errorf("%s: encoded frame is %d bytes, ceiling is %d",
					fixture.Path, len(encoded)+1, MaxFrameBytes)
			}
		}
	}
}

func TestHelloFixtureDecodesIntoTheServerFirstFrame(t *testing.T) {
	set, _ := bridgeContract(t)
	fixture := bridgeFixture(t, "valid", "hello")

	var envelope helloEnvelope
	decodeStrict(t, fixture.Path, realized(fixture.Document), &envelope)

	if envelope.Type != "hello" {
		t.Fatalf("type = %q, want hello", envelope.Type)
	}
	if envelope.Protocol != ProtocolVersion {
		t.Errorf("protocol = %d, want %d", envelope.Protocol, ProtocolVersion)
	}
	if envelope.ContractSetID != ContractSetID {
		t.Errorf("contract_set_id = %q, want %q", envelope.ContractSetID, ContractSetID)
	}
	if envelope.MaxFrameBytes != MaxFrameBytes {
		t.Errorf("max_frame_bytes = %d, want %d", envelope.MaxFrameBytes, MaxFrameBytes)
	}
	if envelope.ASLVersion == "" {
		t.Error("asl_version is empty")
	}
	if envelope.PollIntervalSeconds <= 0 {
		t.Errorf("poll_interval_seconds = %v, want positive", envelope.PollIntervalSeconds)
	}
	// The daemon refuses to take a snapshot before handshaking, so a hello that
	// did not require one would describe a wire this build does not implement.
	if !envelope.HandshakeRequired {
		t.Error("handshake_required is false")
	}
	if fmt.Sprint(envelope.Capabilities) != fmt.Sprint(set.Capabilities.BridgeMethods) {
		t.Errorf("capabilities = %v, want %v", envelope.Capabilities, set.Capabilities.BridgeMethods)
	}
	if fmt.Sprint(envelope.Events) != fmt.Sprint(set.Capabilities.BridgeEvents) {
		t.Errorf("events = %v, want %v", envelope.Events, set.Capabilities.BridgeEvents)
	}
	if fmt.Sprint(envelope.MutationMethods) != fmt.Sprint(set.Capabilities.BridgeMutationMethods) {
		t.Errorf("mutation_methods = %v, want %v",
			envelope.MutationMethods, set.Capabilities.BridgeMutationMethods)
	}
}

// TestTheHelloCarriesNoPerComponentVersionList records why one identifier is
// the whole compatibility answer. HelloFrame forbids unknown properties, so
// there is nowhere for a per-component version list to live, and there is no
// need for one: the contract set behind the ID pins every component's schema
// version, and the ID is a digest over the corpus that carries them.
func TestTheHelloCarriesNoPerComponentVersionList(t *testing.T) {
	set, _ := bridgeContract(t)
	fixture := bridgeFixture(t, "valid", "hello")

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(fixture.Document, &fields); err != nil {
		t.Fatalf("%s: %v", fixture.Path, err)
	}
	for name := range fields {
		if strings.Contains(name, "component") || strings.Contains(name, "schema") {
			t.Errorf("hello carries %q; the contract-set ID is the version answer", name)
		}
	}
	if _, ok := fields["contract_set_id"]; !ok {
		t.Fatal("hello carries no contract_set_id")
	}
	for _, component := range []string{
		aslcontract.ComponentBridgeProtocol,
		aslcontract.ComponentLifecycleHook,
		aslcontract.ComponentProjection,
	} {
		if got, ok := set.Components[component]; !ok || got.SchemaVersion == 0 {
			t.Errorf("contract set pins no schema version for %s", component)
		}
	}
}

func TestHandshakeFixturesSeparateTheGrantFromTheRefusal(t *testing.T) {
	var accepted Handshake
	decodeResponse(t, "handshake-accepted", &accepted)
	var readOnly Handshake
	decodeResponse(t, "handshake-read-only", &readOnly)

	for _, result := range []Handshake{accepted, readOnly} {
		if result.ContractSetID != ContractSetID {
			t.Errorf("handshake contract_set_id = %q, want %q", result.ContractSetID, ContractSetID)
		}
		if result.SnapshotDigest == "" {
			t.Error("handshake names no snapshot digest")
		}
	}
	if !accepted.MutationsEnabled {
		t.Error("the accepted handshake does not enable mutations")
	}
	if accepted.HandshakeID == "" {
		t.Error("the accepted handshake carries no handshake id")
	}
	// A refused handshake still names the digest the bridge actually holds. That
	// is what the daemon refreshes towards, so read-only is a recoverable state
	// rather than a dead end.
	if readOnly.MutationsEnabled {
		t.Error("the read-only handshake enables mutations")
	}
	if readOnly.SnapshotDigest == accepted.SnapshotDigest {
		t.Error("the read-only handshake echoes the accepted digest")
	}
}

func TestSnapshotFixtureDecodesIntoTypedRows(t *testing.T) {
	set, _ := bridgeContract(t)
	var snapshot Snapshot
	decodeResponse(t, "runs-snapshot", &snapshot)

	if snapshot.Protocol != ProtocolVersion {
		t.Errorf("snapshot protocol = %d, want %d", snapshot.Protocol, ProtocolVersion)
	}
	// The identity is inside the digested material, so a snapshot cannot be
	// quoted back under a contract set it was not built under.
	if snapshot.ContractSetID != ContractSetID {
		t.Errorf("snapshot contract_set_id = %q, want %q", snapshot.ContractSetID, ContractSetID)
	}
	if snapshot.RowCount != len(snapshot.Rows) {
		t.Errorf("row_count = %d, rows = %d", snapshot.RowCount, len(snapshot.Rows))
	}
	if snapshot.Digest == "" {
		t.Error("snapshot digest is empty")
	}

	var sawQuestion, sawWindow bool
	for _, row := range snapshot.Rows {
		if row.RowID == "" || row.PlanDir == "" || row.RunID == "" || row.ActionToken == "" {
			t.Errorf("row %q is missing an identity field", row.RowID)
		}
		for _, action := range row.Actions {
			if !contains(set.Capabilities.BridgeMethods, action) {
				t.Errorf("row %s offers action %q outside the method list", row.RowID, action)
			}
		}
		for method := range row.DisabledReasons {
			if !contains(set.Capabilities.BridgeMethods, method) {
				t.Errorf("row %s disables %q outside the method list", row.RowID, method)
			}
		}
		if row.Window != nil {
			sawWindow = true
		}
		if row.Question != nil {
			sawQuestion = true
			if row.Question.QuestionID == "" || row.Question.ContextKind == "" {
				t.Errorf("row %s carries an incomplete question projection", row.RowID)
			}
		}
	}
	if !sawQuestion {
		t.Error("no fixture row exercises the question projection")
	}
	if !sawWindow {
		t.Error("no fixture row exercises the Kitty window binding")
	}
}

func TestSnapshotFixtureCarriesTheProjectionCadence(t *testing.T) {
	fixture := bridgeFixture(t, "valid", "hello")
	var hello helloEnvelope
	decodeStrict(t, fixture.Path, realized(fixture.Document), &hello)

	var snapshot Snapshot
	decodeResponse(t, "runs-snapshot", &snapshot)

	cadence := snapshot.Projection
	if cadence == nil {
		t.Fatal("snapshot carries no projection cadence")
	}
	if cadence.IntervalSeconds != hello.PollIntervalSeconds {
		t.Errorf("interval_seconds = %v, want the declared poll interval %v",
			cadence.IntervalSeconds, hello.PollIntervalSeconds)
	}
	// The declared interval is a floor the bridge cannot beat on a corpus that
	// takes longer to walk than the interval, which is the case this fixture
	// describes. A client that labels the floor would understate staleness.
	if cadence.ObservedIntervalSeconds <= cadence.IntervalSeconds {
		t.Errorf("observed_interval_seconds = %v, want more than the floor %v",
			cadence.ObservedIntervalSeconds, cadence.IntervalSeconds)
	}
	if cadence.BuildSeconds <= 0 || cadence.AgeSeconds < 0 {
		t.Errorf("build/age = %v/%v, want a measured walk and a non-negative age",
			cadence.BuildSeconds, cadence.AgeSeconds)
	}
	want := cadence.BuildSeconds + cadence.AgeSeconds + cadence.ObservedIntervalSeconds
	if cadence.StalenessBoundSeconds != want {
		t.Errorf("staleness_bound_seconds = %v, want %v", cadence.StalenessBoundSeconds, want)
	}
	if cadence.FailedProjections != 0 {
		t.Errorf("failed_projections = %d, want 0", cadence.FailedProjections)
	}
}

func TestQuestionBodiesLiveOutsideEverySharedFrame(t *testing.T) {
	_, schema := bridgeContract(t)

	var body QuestionBody
	decodeResponse(t, "question-body", &body)
	if body.Body.Reveal() == "" {
		t.Fatal("question body fixture is empty")
	}
	if got := fmt.Sprint(body.Body); got != "[redacted]" {
		t.Errorf("formatted body = %q, want [redacted]", got)
	}
	if body.BodyBytes != len(body.Body.Reveal()) {
		t.Errorf("body_bytes = %d, body is %d bytes", body.BodyBytes, len(body.Body.Reveal()))
	}

	// The schema names the one pointer allowed to carry prose. Every other frame
	// in the corpus, valid or invalid, is checked against the forbidden property
	// names, so a body cannot reappear inside a snapshot, a receipt, or an event.
	if fmt.Sprint(schema.BodyBearingPointers) != fmt.Sprint([]string{"/result/body"}) {
		t.Fatalf("body-bearing pointers = %v, want exactly /result/body", schema.BodyBearingPointers)
	}
	if len(schema.ForbiddenPropertyNames) == 0 {
		t.Fatal("the schema forbids no property names")
	}
	for _, fixture := range bridgeFixtures(t, "valid") {
		if fixture.Name == "question-body" {
			continue
		}
		if bytes.Contains(fixture.Document, []byte(body.Body.Reveal())) {
			t.Errorf("%s carries the question body", fixture.Path)
		}
		for _, forbidden := range schema.ForbiddenPropertyNames {
			if bytes.Contains(fixture.Document, []byte(`"`+forbidden+`":`)) {
				t.Errorf("%s carries a %q key", fixture.Path, forbidden)
			}
		}
	}
}

func TestOperationFixturesShareTheReceiptShape(t *testing.T) {
	var receipt Operation
	decodeResponse(t, "operation-receipt", &receipt)

	var updated Operation
	frame := decodeEvent(t, "operation-updated-event", &updated)

	if frame.Event != EventOperationUpdated {
		t.Fatalf("event = %q, want %s", frame.Event, EventOperationUpdated)
	}
	if receipt.Status != OperationAccepted {
		t.Errorf("receipt status = %q, want %q", receipt.Status, OperationAccepted)
	}
	if receipt.Method != MethodQuestionReply {
		t.Errorf("receipt method = %q, want %q", receipt.Method, MethodQuestionReply)
	}
	if !contains(MutationMethods, receipt.Method) {
		t.Errorf("a receipt exists for %q, which is not a mutation", receipt.Method)
	}
	if updated.Status != OperationRejected {
		t.Errorf("updated status = %q, want %q", updated.Status, OperationRejected)
	}
	if updated.OperationID == "" || updated.Detail == "" {
		t.Error("terminal receipt must name its operation and carry a detail")
	}
	// The receipt and the update are the same shape carried by different frames:
	// one answers a request, the other arrives unsolicited. A client correlates
	// an outcome by operation_id, so both must always carry one.
	if receipt.OperationID == "" {
		t.Error("a receipt with no operation id cannot be correlated")
	}
}

func TestRunsChangedEventFixtureIsBodyFree(t *testing.T) {
	var data struct {
		Digest   string `json:"digest"`
		RowCount int    `json:"row_count"`
	}
	frame := decodeEvent(t, "runs-changed-event", &data)

	if frame.Event != EventRunsChanged {
		t.Fatalf("event = %q, want %q", frame.Event, EventRunsChanged)
	}
	if data.Digest == "" {
		t.Error("runs.changed carries no digest")
	}
	if data.RowCount < 0 {
		t.Errorf("row_count = %d", data.RowCount)
	}
}
