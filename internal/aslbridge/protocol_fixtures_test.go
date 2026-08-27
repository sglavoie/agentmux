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

func TestErrorFixtureDecodesIntoADeclaredCode(t *testing.T) {
	response := decodeResponse(t, "stale-action-token-error", nil)

	if response.OK {
		t.Fatal("error fixture reports ok")
	}
	if response.Error == nil {
		t.Fatal("error fixture carries no error")
	}
	if response.Error.Code != CodeStaleActionToken {
		t.Errorf("code = %q, want %q", response.Error.Code, CodeStaleActionToken)
	}
	// Retryable here means "refresh the snapshot and reissue", not "resend this
	// request": the daemon never resubmits a mutation on its own, so the flag
	// must not be read as permission to retry the same token.
	if !response.Error.Retryable {
		t.Error("stale-action-token is retryable after a refresh")
	}
	if _, ok := response.Error.Details["expected_fingerprint"]; !ok {
		t.Error("stale-action-token carries no expected fingerprint")
	}
	if !contains(ErrorCodes, response.Error.Code) {
		t.Errorf("code %q is not in ErrorCodes", response.Error.Code)
	}
}

// TestTheMismatchReasonIsStableAndSanitized covers the reason an operator sees
// when the two sides were built from different corpora.
//
// It must be stable, because a mismatch is not transient: the same pairing
// reproduces it forever, so the text is a fact to act on rather than a symptom
// to wait out. It must be sanitized, because the alternative — quoting the ID
// the peer offered — puts bridge-authored text on an operator's screen. The
// offered value is real evidence, so it goes to the private daemon log; the
// published reason names only this build's own constant.
func TestTheMismatchReasonIsStableAndSanitized(t *testing.T) {
	if !contains(ErrorCodes, CodeContractSetMismatch) {
		t.Fatalf("%q is not a declared code", CodeContractSetMismatch)
	}
	if !contains(ErrorCodes, CodeStaleSnapshot) {
		t.Fatalf("%q is not a declared code", CodeStaleSnapshot)
	}

	reason := sanitizeReason(ErrContractSetMismatch)
	if reason != sanitizeReason(ErrContractSetMismatch) {
		t.Fatal("the mismatch reason is not stable")
	}
	if !strings.Contains(reason, ContractSetID) {
		t.Errorf("reason %q does not name the set this build implements", reason)
	}
	if len(reason) > maxReasonChars {
		t.Errorf("reason is %d chars, bound is %d", len(reason), maxReasonChars)
	}
	if strings.ContainsAny(reason, "\n\r") {
		t.Errorf("reason %q is not flattened", reason)
	}

	// Wrapping it with a child's stderr must not change what is published.
	tail := newStderrTail()
	tail.append([]byte("Traceback: /home/op/plans/alpha exploded\n"))
	wrapped := sanitizeReason(&privateError{public: ErrContractSetMismatch, detail: tail.lastLine()})
	if wrapped != reason {
		t.Errorf("wrapped reason = %q, want %q", wrapped, reason)
	}
	if strings.Contains(wrapped, "plans/alpha") {
		t.Error("the published reason leaked child-authored stderr")
	}
}

func TestWorkbenchLinkFixtureIsALoopbackURL(t *testing.T) {
	var link WorkbenchLink
	decodeResponse(t, "workbench-link", &link)

	if link.Focus != FocusReply {
		t.Errorf("focus = %q, want %q", link.Focus, FocusReply)
	}
	if !isLoopbackWorkbenchURL(link.URL) {
		t.Errorf("url = %q, want a loopback origin", link.URL)
	}
	if !strings.Contains(link.URL, "token=") {
		t.Error("workbench link carries no token")
	}
}

func isLoopbackWorkbenchURL(url string) bool {
	return strings.HasPrefix(url, "http://127.0.0.1:")
}

// ----------------------------------------------------------------
// The invalid half of the corpus

// TestEveryInvalidFixtureIsRefusedForItsDeclaredReason runs the negative corpus
// through the same decode path a live frame takes.
//
// Two reason categories are structural in Go: an unknown field and a body where
// none belongs both fail DisallowUnknownFields, because this package has no home
// for either. Three more are vocabulary or shape checks this package makes
// itself. The remaining two — a string past its bound and an array past its
// bound — are the schema's to enforce and ASL's suite validates them there; Go's
// decoder has no opinion on length, so claiming a refusal here would be a test
// that passes for the wrong reason.
//
// The point of the closed switch is that a reason category ASL adds later has
// nowhere to land silently: it fails this test until someone decides which
// bucket it belongs in.
func TestEveryInvalidFixtureIsRefusedForItsDeclaredReason(t *testing.T) {
	set, _ := bridgeContract(t)
	schemaOnly := map[string]bool{"string-too-long": true, "array-too-long": true}

	seen := make(map[string]bool)
	for _, fixture := range bridgeFixtures(t, "invalid") {
		seen[fixture.Reason] = true
		if fixture.Pointer == "" {
			t.Errorf("%s declares no pointer", fixture.Path)
		}
		refused, why := refuseFixture(set, fixture)
		switch {
		case schemaOnly[fixture.Reason]:
			// Decoding must still succeed: the document is well-shaped and only
			// its bounds are wrong, so a failure here would mean the Go types
			// disagree with the schema about something other than length.
			if refused && why != "" && strings.Contains(why, "unknown field") {
				t.Errorf("%s: %s is bounded by the schema, but Go rejected it as %s",
					fixture.Path, fixture.Reason, why)
			}
		case !refused:
			t.Errorf("%s: declared %s at %s, but this package accepted it",
				fixture.Path, fixture.Reason, fixture.Pointer)
		}
	}

	for _, reason := range []string{
		"unknown-field", "body-not-allowed", "const-mismatch", "invalid-enum", "pattern-mismatch",
	} {
		if !seen[reason] {
			t.Errorf("no invalid fixture exercises %s", reason)
		}
	}
	for reason := range seen {
		switch reason {
		case "unknown-field", "body-not-allowed", "const-mismatch", "invalid-enum",
			"pattern-mismatch", "string-too-long", "array-too-long":
		default:
			t.Errorf("reason %q has no decided Go-side meaning", reason)
		}
	}
}

// TestEveryValidFixtureSurvivesTheSameRefusalPath is the other half: the checks
// above must not be refusing documents ASL considers canonical.
func TestEveryValidFixtureSurvivesTheSameRefusalPath(t *testing.T) {
	set, _ := bridgeContract(t)
	for _, fixture := range bridgeFixtures(t, "valid") {
		if refused, why := refuseFixture(set, fixture); refused {
			t.Errorf("%s is canonical but this package refused it: %s", fixture.Path, why)
		}
	}
}

// refuseFixture decodes one document the way a live frame is decoded and applies
// the closed-vocabulary checks this package makes, returning the first reason it
// is unusable here.
func refuseFixture(set aslcontract.ContractSet, fixture aslcontract.Fixture) (bool, string) {
	doc := realized(fixture.Document)
	switch fixture.Root {
	case "hello":
		var envelope helloEnvelope
		if err := decodeInto(doc, &envelope); err != nil {
			return true, err.Error()
		}
		if envelope.Protocol != ProtocolVersion {
			return true, fmt.Sprintf("protocol %d", envelope.Protocol)
		}
		for _, name := range envelope.Capabilities {
			if !contains(set.Capabilities.BridgeMethods, name) {
				return true, "undeclared method " + name
			}
		}
		for _, name := range envelope.Events {
			if !contains(set.Capabilities.BridgeEvents, name) {
				return true, "undeclared event " + name
			}
		}
		for _, name := range envelope.MutationMethods {
			if !contains(set.Capabilities.BridgeMutationMethods, name) {
				return true, "undeclared mutation " + name
			}
		}
		return false, ""
	case "error_response":
		response, err := refuseResponse(doc, nil)
		if err != nil {
			return true, err.Error()
		}
		if response.Error == nil {
			return true, "error response carries no error"
		}
		if !contains(ErrorCodes, response.Error.Code) {
			return true, "undeclared code " + response.Error.Code
		}
		return false, ""
	case "handshake_response":
		var result Handshake
		if _, err := refuseResponse(doc, &result); err != nil {
			return true, err.Error()
		}
		if result.ContractSetID != ContractSetID {
			return true, "foreign contract set " + result.ContractSetID
		}
		return false, ""
	case "runs_snapshot_response":
		var snapshot Snapshot
		if _, err := refuseResponse(doc, &snapshot); err != nil {
			return true, err.Error()
		}
		if snapshot.ContractSetID != ContractSetID {
			return true, "foreign contract set " + snapshot.ContractSetID
		}
		for _, row := range snapshot.Rows {
			for _, action := range row.Actions {
				if !contains(set.Capabilities.BridgeMethods, action) {
					return true, "undeclared action " + action
				}
			}
		}
		return false, ""
	case "question_body_response":
		var body QuestionBody
		if _, err := refuseResponse(doc, &body); err != nil {
			return true, err.Error()
		}
		return false, ""
	case "operation_receipt_response":
		var receipt Operation
		if _, err := refuseResponse(doc, &receipt); err != nil {
			return true, err.Error()
		}
		return refuseOperation(set, receipt)
	case "workbench_link_response":
		var link WorkbenchLink
		if _, err := refuseResponse(doc, &link); err != nil {
			return true, err.Error()
		}
		if !validFocus(link.Focus) {
			return true, "undeclared focus " + link.Focus
		}
		if !isLoopbackWorkbenchURL(link.URL) {
			return true, "non-loopback url"
		}
		return false, ""
	case "operation_updated_event":
		var updated Operation
		if err := refuseEvent(doc, EventOperationUpdated, &updated); err != nil {
			return true, err.Error()
		}
		return refuseOperation(set, updated)
	case "runs_changed_event":
		var data struct {
			Digest   string `json:"digest"`
			RowCount int    `json:"row_count"`
		}
		if err := refuseEvent(doc, EventRunsChanged, &data); err != nil {
			return true, err.Error()
		}
		return false, ""
	default:
		return true, "unknown root " + fixture.Root
	}
}

func refuseOperation(set aslcontract.ContractSet, op Operation) (bool, string) {
	switch op.Status {
	case OperationAccepted, OperationConsumed, OperationRejected, OperationExpired, OperationSuperseded:
	default:
		return true, "undeclared status " + op.Status
	}
	if !contains(set.Capabilities.BridgeMutationMethods, op.Method) {
		return true, "receipt for non-mutation " + op.Method
	}
	return false, ""
}

func refuseResponse(doc []byte, target any) (fixtureResponse, error) {
	var response fixtureResponse
	if err := decodeInto(doc, &response); err != nil {
		return response, err
	}
	if response.Type != "response" {
		return response, fmt.Errorf("type %q is not a response", response.Type)
	}
	if response.Protocol != ProtocolVersion {
		return response, fmt.Errorf("protocol %d", response.Protocol)
	}
	if target != nil {
		if err := decodeInto(response.Result, target); err != nil {
			return response, err
		}
	}
	return response, nil
}

func refuseEvent(doc []byte, want string, target any) error {
	var frame fixtureEvent
	if err := decodeInto(doc, &frame); err != nil {
		return err
	}
	if frame.Type != "event" {
		return fmt.Errorf("type %q is not an event", frame.Type)
	}
	if frame.Protocol != ProtocolVersion {
		return fmt.Errorf("protocol %d", frame.Protocol)
	}
	if frame.Event != want {
		return fmt.Errorf("event %q, want %s", frame.Event, want)
	}
	return decodeInto(frame.Data, target)
}
