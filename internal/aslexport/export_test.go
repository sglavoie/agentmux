package aslexport

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testContractSet = "asl-amx-1.6f30193cd1744a98"

// fakeRunner installs a scripted subprocess for one test. Every degraded path
// this package exists to name is unreachable with a real ASL on the box, so the
// seam is where they are covered.
func fakeRunner(
	t *testing.T,
	fn func(args []string) (stdout []byte, stderr string, exitCode int, err error),
) *[]string {
	t.Helper()
	captured := &[]string{}
	previous := runner
	runner = func(_ context.Context, _ string, args []string, _ time.Duration, _ int64) ([]byte, string, int, error) {
		*captured = append([]string(nil), args...)
		return fn(args)
	}
	t.Cleanup(func() { runner = previous })
	return captured
}

func validDocument() Document {
	return Document{
		SchemaVersion: SchemaVersion,
		Kind:          Kind,
		ContractSetID: testContractSet,
		Build: Build{
			Product: "agent-session-loop", Version: "1.2.3",
			Executable: "/opt/bin/asl", BinaryHash: strings.Repeat("a", 64),
			ContractSetID: testContractSet, StateSchemaVersion: 3,
			ProjectionSchemaVersion: 1, HookSchemaVersion: 2, BridgeProtocol: 2,
		},
		StateRoot:        "/state",
		StateRootPresent: true,
		Limits:           Limits{MaxRuns: 25, MaxExportBytes: 1 << 20},
		Counts:           Counts{PlansScanned: 1, RunsMatched: 1, RunsExported: 1},
		Runs: []Run{{
			PlanDigest: strings.Repeat("b", 64), RunID: "run-1", Phase: "running",
			Delivery: DeliverySection{
				Total: 1, Exported: 1, Acknowledged: 1,
				ByStatus: []StatusTally{{Status: "accepted", Count: 1}},
				Records:  []Delivery{{RunID: "run-1", RequestID: "req-1", FinalStatus: "accepted", Acknowledged: true}},
			},
		}},
		Digest: strings.Repeat("c", 64),
		OK:     true,
		Reason: "ok",
	}
}

func mustJSON(t *testing.T, document Document) []byte {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fetch(t *testing.T, options Options) Result {
	t.Helper()
	result, err := Fetch(t.Context(), options)
	if err != nil {
		t.Fatalf("Fetch returned an error for a survivable condition: %v", err)
	}
	return result
}

func TestFetchDecodesAValidDocumentAndPassesNarrowingArguments(t *testing.T) {
	document := validDocument()
	args := fakeRunner(t, func([]string) ([]byte, string, int, error) {
		return mustJSON(t, document), "", 0, nil
	})
	result := fetch(t, Options{
		Executable: "/opt/bin/asl", StateRoot: "/state", RunID: "run-1",
		PlanDir: "/plans/one", ContractSetID: testContractSet,
	})
	if !result.Availability.Available || result.Availability.Reason != ReasonOK {
		t.Fatalf("availability = %+v", result.Availability)
	}
	if result.Document == nil || result.Document.Runs[0].RunID != "run-1" {
		t.Fatalf("document = %+v", result.Document)
	}
	if result.Document.Runs[0].Delivery.ByStatus[0] != (StatusTally{Status: "accepted", Count: 1}) {
		t.Fatalf("tally = %+v", result.Document.Runs[0].Delivery.ByStatus)
	}
	want := "diagnostics export --agent-mux --state-dir /state --run-id run-1 --plan-dir /plans/one"
	if got := strings.Join(*args, " "); got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestFetchDropsFieldsThisBuildDoesNotImplement(t *testing.T) {
	// Typed decoding is the containment boundary: a newer ASL cannot put an
	// unreviewed field into an AgentMux bundle by adding it to its export.
	raw := `{"schema_version":1,"kind":"` + Kind + `","contract_set_id":"` + testContractSet + `",` +
		`"ok":true,"question_body":"CANARY-BODY","runs":[{"run_id":"run-1","prompt":"CANARY-PROMPT"}]}`
	fakeRunner(t, func([]string) ([]byte, string, int, error) { return []byte(raw), "", 0, nil })
	result := fetch(t, Options{Executable: "/opt/bin/asl", ContractSetID: testContractSet})
	if result.Document == nil {
		t.Fatalf("availability = %+v", result.Availability)
	}
	encoded, err := json.Marshal(result.Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"CANARY-BODY", "CANARY-PROMPT", "question_body"} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("typed decode preserved %q: %s", canary, encoded)
		}
	}
}

func TestFetchNamesEveryDegradedCondition(t *testing.T) {
	foreign := validDocument()
	foreign.ContractSetID = "asl-amx-9.deadbeefdeadbeef"
	newerSchema := validDocument()
	newerSchema.SchemaVersion = SchemaVersion + 1
	wrongKind := validDocument()
	wrongKind.Kind = "something-else"
	failed := validDocument()
	failed.OK = false
	failed.Reason = "state-root-error"
	failed.Error = "no such directory"

	cases := []struct {
		name       string
		executable string
		run        func(args []string) ([]byte, string, int, error)
		reason     string
		detail     string
		document   bool
	}{
		{
			name: "no executable resolved", executable: "",
			run:    func([]string) ([]byte, string, int, error) { return nil, "", 0, nil },
			reason: ReasonNotFound,
		},
		{
			name:       "predates the subcommand",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return nil, "asl: invalid choice: 'diagnostics'", 2, nil
			},
			reason: ReasonUnsupported, detail: "invalid choice",
		},
		{
			// Only argparse's status 2 says the subcommand is unknown; a
			// crash writes nothing to stdout either.
			name:       "crashed without output",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return nil, "Traceback (most recent call last): boom", 1, nil
			},
			reason: ReasonFailed, detail: "status 1",
		},
		{
			name:       "could not be executed",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return nil, "", 0, errors.New("fork/exec /opt/bin/asl: permission denied")
			},
			reason: ReasonFailed, detail: "permission denied",
		},
		{
			name:       "canceled",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return nil, "", 0, context.Canceled
			},
			reason: ReasonFailed, detail: "canceled",
		},
		{
			name:       "hung",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return nil, "", 0, context.DeadlineExceeded
			},
			reason: ReasonTimeout,
		},
		{
			name:       "oversize",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return []byte("{"), "", 0, errOversize
			},
			reason: ReasonOversize,
		},
		{
			name:       "unreadable output",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return []byte("not json at all"), "", 0, nil
			},
			reason: ReasonMalformed,
		},
		{
			name:       "silent success",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return nil, "", 0, nil
			},
			reason: ReasonMalformed, detail: "wrote no output",
		},
		{
			name:       "unnamed document",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return mustJSON(t, wrongKind), "", 0, nil
			},
			reason: ReasonMalformed, detail: "names kind",
		},
		{
			name:       "newer schema",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return mustJSON(t, newerSchema), "", 0, nil
			},
			reason: ReasonUnsupportedSchema,
		},
		{
			name:       "foreign contract set",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return mustJSON(t, foreign), "", 0, nil
			},
			reason: ReasonForeignContract,
		},
		{
			name:       "export reported its own failure",
			executable: "/opt/bin/asl",
			run: func([]string) ([]byte, string, int, error) {
				return mustJSON(t, failed), "", 0, nil
			},
			reason: ReasonFailed, detail: "state-root-error: no such directory", document: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fakeRunner(t, testCase.run)
			result := fetch(t, Options{Executable: testCase.executable, ContractSetID: testContractSet})
			if result.Availability.Available {
				t.Fatalf("availability = %+v, want unavailable", result.Availability)
			}
			if result.Availability.Reason != testCase.reason {
				t.Fatalf("reason = %q, want %q (detail %q)",
					result.Availability.Reason, testCase.reason, result.Availability.Detail)
			}
			if testCase.detail != "" && !strings.Contains(result.Availability.Detail, testCase.detail) {
				t.Fatalf("detail = %q, want it to mention %q", result.Availability.Detail, testCase.detail)
			}
			if (result.Document != nil) != testCase.document {
				t.Fatalf("document present = %t, want %t", result.Document != nil, testCase.document)
			}
		})
	}
}

func TestFetchSkipsTheContractCheckWhenNoneIsDemanded(t *testing.T) {
	document := validDocument()
	document.ContractSetID = "asl-amx-9.deadbeefdeadbeef"
	fakeRunner(t, func([]string) ([]byte, string, int, error) {
		return mustJSON(t, document), "", 0, nil
	})
	result := fetch(t, Options{Executable: "/opt/bin/asl"})
	if !result.Availability.Available {
		t.Fatalf("availability = %+v", result.Availability)
	}
}

func TestFetchRecordsBytesEvenForUnreadableOutput(t *testing.T) {
	payload := []byte(strings.Repeat("x", 4096))
	fakeRunner(t, func([]string) ([]byte, string, int, error) { return payload, "", 0, nil })
	result := fetch(t, Options{Executable: "/opt/bin/asl", ContractSetID: testContractSet})
	if result.Bytes != len(payload) {
		t.Fatalf("bytes = %d, want %d", result.Bytes, len(payload))
	}
}

func TestFetchToleratesANilContext(t *testing.T) {
	fakeRunner(t, func([]string) ([]byte, string, int, error) {
		return mustJSON(t, validDocument()), "", 0, nil
	})
	var unset context.Context // a nil context is the condition under test
	result, err := Fetch(unset, Options{Executable: "/opt/bin/asl", ContractSetID: testContractSet})
	if err != nil || !result.Availability.Available {
		t.Fatalf("result = %+v err = %v", result.Availability, err)
	}
}

func TestAvailabilityReasonSerializesAsAStableCode(t *testing.T) {
	// reason_code rather than reason, because bundle redaction hashes free-form
	// reason strings and this enum has to survive a shared bundle.
	raw, err := json.Marshal(unavailable(ReasonTimeout, "slow"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reason_code":"timeout"`) {
		t.Fatalf("availability JSON = %s", raw)
	}
}

func TestLimitedBufferStopsAtTheCeiling(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 8
	if _, err := buffer.Write([]byte("0123456789")); err != nil && !errors.Is(err, errOversize) {
		t.Fatalf("write error = %v", err)
	}
	if !buffer.exceeded {
		t.Fatal("limited buffer did not flag the overrun")
	}
}
