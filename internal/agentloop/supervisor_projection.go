package agentloop

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	mailboxLeaseFileName     = "mailbox-lease.json"
	mailboxLeaseBytes        = 512
	mailboxLeaseVersion      = 2
	mailboxLeaseFreshSeconds = 90
)

// SupervisorQuestionProjection is ASL's text-free, run-scoped view of mailbox
// questions observed by the active supervisor. ProjectionSeq is the physical
// mailbox sequence at which the view was computed, so it is also a durable
// source sequence for Agent Mux's reducer probe.
type SupervisorQuestionProjection struct {
	RunID                   string
	ProjectionSeq           uint64
	UnansweredQuestions     int
	LatestQuestionID        string
	LatestQuestionCreatedAt time.Time
	UpdatedAt               time.Time
}

type mailboxLeaseV2 struct {
	Version                           int     `json:"version"`
	SupervisorID                      string  `json:"supervisor_id"`
	UpdatedAt                         string  `json:"updated_at"`
	RunID                             *string `json:"run_id"`
	ProjectionSeq                     *uint64 `json:"projection_seq"`
	UnansweredQuestions               *int    `json:"unanswered_questions"`
	LatestUnansweredQuestionID        *string `json:"latest_unanswered_question_id"`
	LatestUnansweredQuestionCreatedAt *string `json:"latest_unanswered_question_created_at"`
}

// ReadSupervisorQuestionProjection reads the fixed-size ASL supervisor lease.
// The boolean is true only for a fresh, valid v2 projection bound to candidate's
// exact run. Missing, stale, v1, malformed, and foreign-run leases are
// non-authoritative rather than errors; I/O and locking failures are returned so
// callers can preserve prior evidence without treating it as a valid clear.
func ReadSupervisorQuestionProjection(candidate Candidate, now time.Time) (SupervisorQuestionProjection, bool, error) {
	if candidate.StateDir == "" || candidate.RunID == "" {
		return SupervisorQuestionProjection{}, false, nil
	}
	path := filepath.Join(candidate.StateDir, mailboxLeaseFileName)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return SupervisorQuestionProjection{}, false, nil
	}
	if err != nil {
		return SupervisorQuestionProjection{}, false, err
	}
	defer file.Close()

	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH); err != nil {
		return SupervisorQuestionProjection{}, false, err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN) //nolint:errcheck

	data, err := io.ReadAll(io.LimitReader(file, mailboxLeaseBytes+1))
	if err != nil {
		return SupervisorQuestionProjection{}, false, err
	}
	if len(data) != mailboxLeaseBytes {
		return SupervisorQuestionProjection{}, false, nil
	}
	data = bytes.TrimRight(data, " \t\r\n\x00")
	if len(data) == 0 {
		return SupervisorQuestionProjection{}, false, nil
	}

	var lease mailboxLeaseV2
	if err := json.Unmarshal(data, &lease); err != nil {
		return SupervisorQuestionProjection{}, false, nil
	}
	if lease.Version != mailboxLeaseVersion || !validUUID(lease.SupervisorID) {
		return SupervisorQuestionProjection{}, false, nil
	}
	updatedAt, ok := parseUTCTimestamp(lease.UpdatedAt)
	if !ok || updatedAt.After(now) || now.Sub(updatedAt) > mailboxLeaseFreshSeconds*time.Second {
		return SupervisorQuestionProjection{}, false, nil
	}
	if lease.RunID == nil || *lease.RunID != candidate.RunID ||
		lease.ProjectionSeq == nil || lease.UnansweredQuestions == nil ||
		*lease.UnansweredQuestions < 0 {
		return SupervisorQuestionProjection{}, false, nil
	}

	projection := SupervisorQuestionProjection{
		RunID:               *lease.RunID,
		ProjectionSeq:       *lease.ProjectionSeq,
		UnansweredQuestions: *lease.UnansweredQuestions,
		UpdatedAt:           updatedAt,
	}
	if projection.UnansweredQuestions == 0 {
		if lease.LatestUnansweredQuestionID != nil ||
			lease.LatestUnansweredQuestionCreatedAt != nil {
			return SupervisorQuestionProjection{}, false, nil
		}
		return projection, true, nil
	}
	if projection.ProjectionSeq == 0 {
		return SupervisorQuestionProjection{}, false, nil
	}
	if lease.LatestUnansweredQuestionID == nil ||
		!validUUID(*lease.LatestUnansweredQuestionID) ||
		lease.LatestUnansweredQuestionCreatedAt == nil {
		return SupervisorQuestionProjection{}, false, nil
	}
	createdAt, ok := parseUTCTimestamp(*lease.LatestUnansweredQuestionCreatedAt)
	if !ok {
		return SupervisorQuestionProjection{}, false, nil
	}
	projection.LatestQuestionID = *lease.LatestUnansweredQuestionID
	projection.LatestQuestionCreatedAt = createdAt
	return projection, true, nil
}

func parseUTCTimestamp(raw string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func validUUID(raw string) bool {
	compact := strings.ReplaceAll(raw, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}
