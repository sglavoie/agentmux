package aslbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The fixtures in testdata/protocol_v1 are a byte-identical copy of the frames
// agent-session-loop owns. Decoding them here with DisallowUnknownFields is the
// Go half of the cross-language contract: a field the bridge starts sending
// that this package has no home for fails on this side, and a digest that no
// longer matches the frame files means the copy drifted from its source.

const fixtureDir = "testdata/protocol_v1"

type fixtureManifest struct {
	Protocol            int               `json:"protocol"`
	MaxFrameBytes       int               `json:"max_frame_bytes"`
	PollIntervalSeconds float64           `json:"poll_interval_seconds"`
	Capabilities        []string          `json:"capabilities"`
	ErrorCodes          []string          `json:"error_codes"`
	OperationStatuses   []string          `json:"operation_statuses"`
	WorkbenchFocus      []string          `json:"workbench_focus"`
	ContextKinds        []string          `json:"context_kinds"`
	Frames              map[string]string `json:"frames"`
	Digest              string            `json:"digest"`
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

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func loadManifest(t *testing.T) fixtureManifest {
	t.Helper()
	var manifest fixtureManifest
	decodeStrict(t, "manifest.json", readFixture(t, "manifest.json"), &manifest)
	return manifest
}

func frameNames(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		name := filepath.Base(match)
		if name != "manifest.json" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func decodeStrict(t *testing.T, name string, raw []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

func decodeResponse(t *testing.T, name string, target any) fixtureResponse {
	t.Helper()
	var response fixtureResponse
	decodeStrict(t, name, readFixture(t, name), &response)
	if response.Type != "response" {
		t.Fatalf("%s: type = %q, want response", name, response.Type)
	}
	if response.Protocol != ProtocolVersion {
		t.Fatalf("%s: protocol = %d, want %d", name, response.Protocol, ProtocolVersion)
	}
	if target != nil {
		decodeStrict(t, name+" result", response.Result, target)
	}
	return response
}

func TestFixtureManifestDigestMatchesTheFrameFilesBesideIt(t *testing.T) {
	manifest := loadManifest(t)
	names := frameNames(t)

	pinned := make([]string, 0, len(manifest.Frames))
	for name := range manifest.Frames {
		pinned = append(pinned, name)
	}
	sort.Strings(pinned)
	if fmt.Sprint(names) != fmt.Sprint(pinned) {
		t.Fatalf("manifest pins %v, directory holds %v", pinned, names)
	}

	var joined bytes.Buffer
	for _, name := range names {
		sum := sha256.Sum256(readFixture(t, name))
		digest := hex.EncodeToString(sum[:])
		if manifest.Frames[name] != digest {
			t.Errorf("%s: digest = %s, manifest pins %s", name, digest, manifest.Frames[name])
		}
		fmt.Fprintf(&joined, "%s\n%s\n", name, digest)
	}
	combined := sha256.Sum256(joined.Bytes())
	if got := hex.EncodeToString(combined[:]); got != manifest.Digest {
		t.Errorf("combined digest = %s, manifest pins %s", got, manifest.Digest)
	}
}

func TestFixtureManifestPinsTheProtocolVocabulary(t *testing.T) {
	manifest := loadManifest(t)

	if manifest.Protocol != ProtocolVersion {
		t.Errorf("protocol = %d, want %d", manifest.Protocol, ProtocolVersion)
	}
	if manifest.MaxFrameBytes != MaxFrameBytes {
		t.Errorf("max_frame_bytes = %d, want %d", manifest.MaxFrameBytes, MaxFrameBytes)
	}
	if fmt.Sprint(manifest.ErrorCodes) != fmt.Sprint(ErrorCodes) {
		t.Errorf("error codes = %v, want %v", manifest.ErrorCodes, ErrorCodes)
	}

	wantMethods := map[string]bool{
		MethodRunsSnapshot:        true,
		MethodQuestionGet:         true,
		MethodQuestionReply:       true,
		MethodQuestionResolve:     true,
		MethodRunResume:           true,
		MethodRunStopAfterCurrent: true,
		MethodRunAbort:            true,
		MethodWorkbenchEnsure:     true,
		EventRunsChanged:          true,
		EventOperationUpdated:     true,
	}
	if len(manifest.Capabilities) != len(wantMethods) {
		t.Fatalf("capabilities = %v, want %d entries", manifest.Capabilities, len(wantMethods))
	}
	for _, capability := range manifest.Capabilities {
		if !wantMethods[capability] {
			t.Errorf("capability %q has no method or event constant", capability)
		}
	}

	wantStatuses := []string{
		OperationAccepted,
		OperationConsumed,
		OperationRejected,
		OperationExpired,
		OperationSuperseded,
	}
	if fmt.Sprint(manifest.OperationStatuses) != fmt.Sprint(wantStatuses) {
		t.Errorf("operation statuses = %v, want %v", manifest.OperationStatuses, wantStatuses)
	}

	wantFocus := []string{FocusReply, FocusVerification, FocusActive, FocusEvents}
	if fmt.Sprint(manifest.WorkbenchFocus) != fmt.Sprint(wantFocus) {
		t.Errorf("workbench focus = %v, want %v", manifest.WorkbenchFocus, wantFocus)
	}
}

func TestEveryFixtureFrameFitsOneLineWithinTheCeiling(t *testing.T) {
	for _, name := range frameNames(t) {
		var frame any
		if err := json.Unmarshal(readFixture(t, name), &frame); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		encoded, err := json.Marshal(frame)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bytes.ContainsAny(encoded, "\n") {
			t.Errorf("%s: encoded frame carries a newline", name)
		}
		if len(encoded)+1 > MaxFrameBytes {
			t.Errorf("%s: encoded frame is %d bytes, ceiling is %d", name, len(encoded)+1, MaxFrameBytes)
		}
	}
}

func TestHelloFixtureDecodesIntoTheHandshake(t *testing.T) {
	raw := readFixture(t, "hello.json")
	var envelope struct {
		Type string `json:"type"`
		Hello
	}
	decodeStrict(t, "hello.json", raw, &envelope)

	if envelope.Type != "hello" {
		t.Fatalf("type = %q, want hello", envelope.Type)
	}
	if envelope.Protocol != ProtocolVersion {
		t.Errorf("protocol = %d, want %d", envelope.Protocol, ProtocolVersion)
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
	if fmt.Sprint(envelope.Capabilities) != fmt.Sprint(loadManifest(t).Capabilities) {
		t.Errorf("capabilities = %v, want the manifest list", envelope.Capabilities)
	}
}

func TestSnapshotFixtureDecodesIntoTypedRows(t *testing.T) {
	var snapshot Snapshot
	decodeResponse(t, "runs_snapshot.json", &snapshot)

	if snapshot.Protocol != ProtocolVersion {
		t.Errorf("snapshot protocol = %d, want %d", snapshot.Protocol, ProtocolVersion)
	}
	if snapshot.RowCount != len(snapshot.Rows) {
		t.Errorf("row_count = %d, rows = %d", snapshot.RowCount, len(snapshot.Rows))
	}
	if snapshot.Digest == "" {
		t.Error("snapshot digest is empty")
	}

	capabilities := make(map[string]bool)
	for _, capability := range loadManifest(t).Capabilities {
		capabilities[capability] = true
	}
	var sawQuestion, sawWindow bool
	for _, row := range snapshot.Rows {
		if row.RowID == "" || row.PlanDir == "" || row.RunID == "" || row.ActionToken == "" {
			t.Errorf("row %+v is missing an identity field", row.RowID)
		}
		for _, action := range row.Actions {
			if !capabilities[action] {
				t.Errorf("row %s offers action %q outside the capability list", row.RowID, action)
			}
		}
		for method := range row.DisabledReasons {
			if !capabilities[method] {
				t.Errorf("row %s disables %q outside the capability list", row.RowID, method)
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
	var snapshot Snapshot
	decodeResponse(t, "runs_snapshot.json", &snapshot)

	cadence := snapshot.Projection
	if cadence == nil {
		t.Fatal("snapshot carries no projection cadence")
	}
	if cadence.IntervalSeconds != loadManifest(t).PollIntervalSeconds {
		t.Errorf("interval_seconds = %v, want the declared poll interval %v",
			cadence.IntervalSeconds, loadManifest(t).PollIntervalSeconds)
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

func TestSnapshotFixtureCarriesNoQuestionBody(t *testing.T) {
	var body QuestionBody
	decodeResponse(t, "question_body.json", &body)
	if body.Body.Reveal() == "" {
		t.Fatal("question body fixture is empty")
	}
	if got := fmt.Sprint(body.Body); got != "[redacted]" {
		t.Errorf("formatted body = %q, want [redacted]", got)
	}

	raw := readFixture(t, "runs_snapshot.json")
	if bytes.Contains(raw, []byte(body.Body.Reveal())) {
		t.Error("snapshot fixture carries the question body")
	}
	for _, forbidden := range []string{`"body"`, `"text"`, `"reply":`, `"answer"`, `"url"`, `"token"`} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Errorf("snapshot fixture carries a %s key", forbidden)
		}
	}
}

func TestOperationFixturesShareTheReceiptShape(t *testing.T) {
	var receipt Operation
	decodeResponse(t, "operation_receipt.json", &receipt)

	var event fixtureEvent
	decodeStrict(t, "operation_updated_event.json", readFixture(t, "operation_updated_event.json"), &event)
	var updated Operation
	decodeStrict(t, "operation_updated_event.json data", event.Data, &updated)

	if event.Type != "event" || event.Event != EventOperationUpdated {
		t.Fatalf("event = %q/%q, want event/%s", event.Type, event.Event, EventOperationUpdated)
	}
	if event.Seq == 0 {
		t.Error("event seq is zero")
	}
	if receipt.Status != OperationAccepted {
		t.Errorf("receipt status = %q, want %q", receipt.Status, OperationAccepted)
	}
	if receipt.Method != MethodQuestionReply {
		t.Errorf("receipt method = %q, want %q", receipt.Method, MethodQuestionReply)
	}
	if updated.Status != OperationRejected {
		t.Errorf("updated status = %q, want %q", updated.Status, OperationRejected)
	}
	if updated.OperationID == "" || updated.Detail == "" {
		t.Error("terminal receipt must name its operation and carry a detail")
	}
}

func TestRunsChangedEventFixtureIsBodyFree(t *testing.T) {
	var event fixtureEvent
	decodeStrict(t, "runs_changed_event.json", readFixture(t, "runs_changed_event.json"), &event)

	if event.Event != EventRunsChanged {
		t.Fatalf("event = %q, want %q", event.Event, EventRunsChanged)
	}
	var data struct {
		Digest   string `json:"digest"`
		RowCount int    `json:"row_count"`
	}
	decodeStrict(t, "runs_changed_event.json data", event.Data, &data)
	if data.Digest == "" {
		t.Error("runs.changed carries no digest")
	}
}

func TestErrorFixtureDecodesIntoADeclaredCode(t *testing.T) {
	response := decodeResponse(t, "error_response.json", nil)

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
	declared := false
	for _, code := range ErrorCodes {
		if code == response.Error.Code {
			declared = true
			break
		}
	}
	if !declared {
		t.Errorf("code %q is not in ErrorCodes", response.Error.Code)
	}
}

func TestWorkbenchLinkFixtureIsALoopbackURL(t *testing.T) {
	var link WorkbenchLink
	decodeResponse(t, "workbench_link.json", &link)

	if link.Focus != FocusReply {
		t.Errorf("focus = %q, want %q", link.Focus, FocusReply)
	}
	if !bytes.HasPrefix([]byte(link.URL), []byte("http://127.0.0.1:")) {
		t.Errorf("url = %q, want a loopback origin", link.URL)
	}
	if !bytes.Contains([]byte(link.URL), []byte("token=")) {
		t.Error("workbench link carries no token")
	}
}
