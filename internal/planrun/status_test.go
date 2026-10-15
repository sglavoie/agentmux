package planrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type statusASL struct {
	output string
	args   []string
}

func (s *statusASL) Output(_ context.Context, args ...string) ([]byte, error) {
	s.args = args
	return []byte(s.output), nil
}

func TestReadStatusProjectsActualProgressAndOnlyMatchingVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lines := []string{
		`{"name":"verification_finished","created_at":"old","payload":{"run_id":"old","passed":true}}`,
		`{"name":"question","payload":{"run_id":"run","body":"secret-question"}}`,
		`{"name":"verification_check_finished","created_at":"now","payload":{"run_id":"run","session_id":"2.1","name":"unit","status":"failed","returncode":1,"failure_diagnostic":"secret-output"}}`,
		`{"name":"verification_finished","created_at":"now","payload":{"run_id":"run","session_id":"2.1","passed":false,"check_count":3,"passed_count":2}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	encodedPath, _ := json.Marshal(path)
	asl := &statusASL{output: `{"ok":true,"plan_dir":"/plan","plan_title":"Plan","counts":{"complete":2,"pending":1},"sessions":[{"id":"2.1","status":"pending","path":"/plan/session.md"}],"runner_state":{"run_id":"run","paused":true,"reason":"verification-failed"},"events_path":` + string(encodedPath) + `}`}
	status, err := ReadStatus(context.Background(), asl, "/plan", "/state")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Sessions) != 1 || status.Counts["complete"] != 2 || !status.Runner.Paused {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Verification) != 2 || status.Verification[0].SessionID != "2.1" || *status.Verification[1].Passed {
		t.Fatalf("evidence = %+v", status.Verification)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "secret") {
		t.Fatalf("unreviewed output leaked: %s", data)
	}
	if got := strings.Join(asl.args, "|"); got != "status|/plan|--json|--state-dir|/state" {
		t.Fatal(got)
	}
}

func TestReadStatusRejectsMissingProgressAndMarksUnavailableEvidence(t *testing.T) {
	for _, output := range []string{`garbage`, `{"ok":false}`, `{"ok":true,"counts":{}}`} {
		if _, err := ReadStatus(context.Background(), &statusASL{output: output}, "/plan", ""); err == nil {
			t.Fatalf("accepted %s", output)
		}
	}
	status, err := ReadStatus(context.Background(), &statusASL{output: `{"ok":true,"counts":{},"sessions":[],"runner_state":{"run_id":"r"},"events_path":"/missing/events"}`}, "/plan", "")
	if err != nil || status.VerificationError == "" {
		t.Fatalf("%+v, %v", status, err)
	}
}

func TestVerificationReadIsBoundedAndShowsSkippedEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events")
	line := `{"name":"verification_check_skipped","payload":{"run_id":"r","name":"check"}}` + "\n"
	if err := os.WriteFile(path, []byte(strings.Repeat(line, 210)), 0600); err != nil {
		t.Fatal(err)
	}
	rows, truncated, err := readVerification(path, "r")
	if err != nil || !truncated || len(rows) != verificationRecordLimit || rows[0].Event != "verification_check_skipped" {
		t.Fatalf("%d %v %v", len(rows), truncated, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", verificationTailBytes)+"\n"+line), 0600); err != nil {
		t.Fatal(err)
	}
	_, truncated, err = readVerification(path, "r")
	if !truncated || err == nil {
		t.Fatalf("oversized line must be explicit: %v %v", truncated, err)
	}
}
