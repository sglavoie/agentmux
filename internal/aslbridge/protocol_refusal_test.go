package aslbridge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

// Refusing a frame: the declared invalid fixtures, the sanitized mismatch
// reason, and the shared refusal path every valid fixture must survive.

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
