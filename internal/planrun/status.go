package planrun

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// RunStatus is a deliberately narrow projection of `asl status`. Question
// bodies, prompts, action tokens and arbitrary command output are excluded.
type RunStatus struct {
	PlanDir               string               `json:"plan_dir"`
	Title                 string               `json:"plan_title"`
	Counts                map[string]int       `json:"counts"`
	Sessions              []RunSession         `json:"sessions"`
	Runner                RunPosition          `json:"runner_state"`
	Verification          []VerificationRecord `json:"verification"`
	VerificationTruncated bool                 `json:"verification_truncated"`
	VerificationError     string               `json:"verification_error,omitempty"`
}

type RunSession struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Path   string `json:"path"`
}

type RunPosition struct {
	Present            bool              `json:"present"`
	RunID              string            `json:"run_id"`
	Paused             bool              `json:"paused"`
	Reason             string            `json:"reason"`
	StartedAt          string            `json:"run_started_at"`
	FinishedAt         string            `json:"run_finished_at"`
	DurationSeconds    *float64          `json:"duration_seconds"`
	OK                 *bool             `json:"ok"`
	ActiveVerification *LiveVerification `json:"active_verification"`
}

type LiveVerification struct {
	SessionID        string   `json:"session_id"`
	CheckCount       int      `json:"check_count"`
	CurrentCheckName string   `json:"current_check_name"`
	StartedAt        string   `json:"started_at"`
	ElapsedSeconds   *float64 `json:"elapsed_seconds"`
}

type VerificationRecord struct {
	Event           string   `json:"event"`
	At              string   `json:"at"`
	SessionID       string   `json:"session_id"`
	Name            string   `json:"name,omitempty"`
	Status          string   `json:"status,omitempty"`
	Passed          *bool    `json:"passed,omitempty"`
	CheckCount      *int     `json:"check_count,omitempty"`
	PassedCount     *int     `json:"passed_count,omitempty"`
	SkippedCount    *int     `json:"skipped_count,omitempty"`
	ReturnCode      *int     `json:"returncode,omitempty"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
}

// ReadStatus asks ASL for the authoritative session counts and current run,
// then reads only that run's recorded verification outcomes. ASL may reconcile
// an abandoned run while servicing status; no execution or verification is run.
func ReadStatus(ctx context.Context, asl ASL, planDir, stateRoot string) (RunStatus, error) {
	args := []string{"status", planDir, "--json"}
	if stateRoot != "" {
		args = append(args, "--state-dir", stateRoot)
	}
	out, err := asl.Output(ctx, args...)
	if err != nil {
		return RunStatus{}, fmt.Errorf("ASL status: %w", err)
	}
	if len(out) > 8<<20 {
		return RunStatus{}, errors.New("ASL status exceeds 8 MiB")
	}
	var raw struct {
		RunStatus
		OK         bool   `json:"ok"`
		EventsPath string `json:"events_path"`
	}
	if err := json.Unmarshal(out, &raw); err != nil || !raw.OK {
		return RunStatus{}, errors.New("ASL did not return a valid status report")
	}
	if raw.Counts == nil || raw.Sessions == nil {
		return RunStatus{}, errors.New("ASL status is missing session progress")
	}
	result := raw.RunStatus
	result.Verification = []VerificationRecord{}
	result.VerificationError = ""
	result.VerificationTruncated = false
	if result.Runner.RunID != "" && raw.EventsPath != "" {
		result.Verification, result.VerificationTruncated, err = readVerification(raw.EventsPath, result.Runner.RunID)
		if err != nil {
			result.VerificationError = "Recorded verification could not be read: " + err.Error()
		}
	}
	return result, nil
}

const verificationTailBytes = 4 << 20
const verificationRecordLimit = 200

func readVerification(path, runID string) ([]VerificationRecord, bool, error) {
	rows := []VerificationRecord{}
	info, err := os.Lstat(path)
	if err != nil {
		return rows, false, err
	}
	if !info.Mode().IsRegular() {
		return rows, false, errors.New("event log is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return rows, false, err
	}
	defer func() { _ = f.Close() }() // read-only descriptor; read errors are reported below
	info, err = f.Stat()
	if err != nil {
		return rows, false, err
	}
	if !info.Mode().IsRegular() {
		return rows, false, errors.New("event log is not a regular file")
	}
	truncated := info.Size() > verificationTailBytes
	startsMidLine := false
	if truncated {
		start := info.Size() - verificationTailBytes
		var preceding [1]byte
		if _, err := f.ReadAt(preceding[:], start-1); err != nil {
			return rows, true, err
		}
		startsMidLine = preceding[0] != '\n'
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return rows, true, err
		}
	}
	scanner := bufio.NewScanner(io.LimitReader(f, verificationTailBytes))
	scanner.Buffer(make([]byte, 4096), 512<<10)
	// Discard only a partial first line. The tail can begin immediately
	// after a newline, in which case its first record is complete.
	if startsMidLine {
		scanner.Scan()
	}
	for scanner.Scan() {
		var event struct {
			Name      string `json:"name"`
			CreatedAt string `json:"created_at"`
			Payload   struct {
				VerificationRecord
				RunID string `json:"run_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Payload.RunID != runID {
			continue
		}
		switch event.Name {
		case "verification_finished", "verification_check_finished", "verification_check_skipped":
		default:
			continue
		}
		row := event.Payload.VerificationRecord
		row.Event, row.At = event.Name, event.CreatedAt
		rows = append(rows, row)
		if len(rows) > verificationRecordLimit {
			rows = rows[1:]
			truncated = true
		}
	}
	return rows, truncated, scanner.Err()
}
