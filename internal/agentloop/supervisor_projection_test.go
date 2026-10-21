package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testSupervisorID = "00000000-0000-0000-0000-000000000099"
	testQuestionID   = "00000000-0000-0000-0000-000000000011"
)

func writeSupervisorLeaseFixture(t *testing.T, stateDir string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if len(data) > mailboxLeaseBytes {
		t.Fatalf("fixture exceeds fixed lease size: %d", len(data))
	}
	padding := make([]byte, mailboxLeaseBytes-len(data))
	for i := range padding {
		padding[i] = ' '
	}
	data = append(data, padding...)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, mailboxLeaseFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func validSupervisorLeasePayload(now time.Time) map[string]any {
	return map[string]any{
		"version":                               2,
		"supervisor_id":                         testSupervisorID,
		"updated_at":                            now.Add(-time.Second).Format(time.RFC3339Nano),
		"run_id":                                "run-current",
		"projection_seq":                        7,
		"unanswered_questions":                  1,
		"latest_unanswered_question_id":         testQuestionID,
		"latest_unanswered_question_created_at": now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}
}

func TestReadSupervisorQuestionProjectionValidPendingAndClear(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	stateDir := t.TempDir()
	candidate := Candidate{StateDir: stateDir, RunID: "run-current"}
	payload := validSupervisorLeasePayload(now)
	writeSupervisorLeaseFixture(t, stateDir, payload)

	projection, ok, err := ReadSupervisorQuestionProjection(candidate, now)
	if err != nil || !ok {
		t.Fatalf("pending projection = %+v, ok=%v err=%v", projection, ok, err)
	}
	if projection.ProjectionSeq != 7 || projection.UnansweredQuestions != 1 ||
		projection.LatestQuestionID != testQuestionID {
		t.Fatalf("pending projection = %+v", projection)
	}

	payload["projection_seq"] = 8
	payload["unanswered_questions"] = 0
	delete(payload, "latest_unanswered_question_id")
	delete(payload, "latest_unanswered_question_created_at")
	writeSupervisorLeaseFixture(t, stateDir, payload)
	projection, ok, err = ReadSupervisorQuestionProjection(candidate, now)
	if err != nil || !ok || projection.UnansweredQuestions != 0 ||
		projection.ProjectionSeq != 8 || projection.LatestQuestionID != "" {
		t.Fatalf("clear projection = %+v, ok=%v err=%v", projection, ok, err)
	}
}

func TestReadSupervisorQuestionProjectionRejectsNonAuthoritativeEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	cases := map[string]func(map[string]any){
		"v1": func(payload map[string]any) {
			payload["version"] = 1
		},
		"foreign run": func(payload map[string]any) {
			payload["run_id"] = "run-other"
		},
		"expired": func(payload map[string]any) {
			payload["updated_at"] = now.Add(-91 * time.Second).Format(time.RFC3339Nano)
		},
		"malformed count": func(payload map[string]any) {
			payload["unanswered_questions"] = -1
		},
		"zero pending sequence": func(payload map[string]any) {
			payload["projection_seq"] = 0
		},
		"missing latest id": func(payload map[string]any) {
			delete(payload, "latest_unanswered_question_id")
		},
		"non-UTC timestamp": func(payload map[string]any) {
			payload["updated_at"] = now.Add(-time.Second).In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			payload := validSupervisorLeasePayload(now)
			mutate(payload)
			writeSupervisorLeaseFixture(t, stateDir, payload)
			got, ok, err := ReadSupervisorQuestionProjection(
				Candidate{StateDir: stateDir, RunID: "run-current"},
				now,
			)
			if err != nil || ok {
				t.Fatalf("projection = %+v, ok=%v err=%v; want non-authoritative", got, ok, err)
			}
		})
	}
}
