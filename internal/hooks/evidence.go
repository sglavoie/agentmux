package hooks

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

var errDiagnosticJournalDisabled = errors.New("diagnostic journal is disabled")

type statusEvidenceKey struct {
	harness string
	profile string
}

// StatusEvidence accumulates the newest hook record for each normalized
// harness/profile pair. Visit is suitable for diagnostics.SnapshotWithVisitor,
// which lets callers gather hook evidence without changing the snapshot's
// selector or record limit.
type StatusEvidence struct {
	lastSeen map[statusEvidenceKey]time.Time
}

// NewStatusEvidence returns an empty hook-status evidence accumulator.
func NewStatusEvidence() *StatusEvidence {
	return &StatusEvidence{lastSeen: make(map[statusEvidenceKey]time.Time)}
}

// Visit records hook evidence from one journal envelope. Non-hook stages are
// deliberately ignored.
func (e *StatusEvidence) Visit(envelope diagnostics.TraceEnvelope) error {
	if e == nil || !strings.HasPrefix(envelope.Stage, "hook.") {
		return nil
	}
	if e.lastSeen == nil {
		e.lastSeen = make(map[statusEvidenceKey]time.Time)
	}
	key := statusEvidenceKey{
		harness: normalizeEvidenceHarness(envelope.Harness),
		profile: normalizeEvidenceProfile(envelope.Profile),
	}
	if envelope.ObservedAt.After(e.lastSeen[key]) {
		e.lastSeen[key] = envelope.ObservedAt
	}
	return nil
}

func (e *StatusEvidence) latest(harnessName, profile string) time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.lastSeen[statusEvidenceKey{
		harness: normalizeEvidenceHarness(harnessName),
		profile: normalizeEvidenceProfile(profile),
	}]
}

func normalizeEvidenceHarness(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func normalizeEvidenceProfile(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

var activationEvidenceSnapshot = func(ctx context.Context, journal *diagnostics.Journal, visitor diagnostics.SnapshotVisitor) error {
	// The selector intentionally matches no production stage. The visitor still
	// observes every valid deduplicated record without retaining the journal in
	// the returned snapshot.
	_, err := journal.SnapshotWithVisitor(ctx, diagnostics.Selector{
		Stages: []string{"hook.status.__evidence_only__"},
		Limit:  1,
	}, nil, visitor)
	return err
}

var activationEvidenceProbe = probeStatusEvidence

func probeStatusEvidence(ctx context.Context, cfg *config.Config) (*StatusEvidence, error) {
	evidence := NewStatusEvidence()
	if cfg != nil && !cfg.Defaults.Diagnostics.JournalEnabledValue() {
		return evidence, errDiagnosticJournalDisabled
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return evidence, err
	}
	if _, err := os.Stat(paths.JournalDir); os.IsNotExist(err) {
		return evidence, nil
	} else if err != nil {
		return evidence, err
	}
	options := diagnostics.JournalOptions{}
	if cfg != nil {
		options.KeepDays = cfg.Defaults.Diagnostics.JournalKeepDays
		options.MaxBytes = int64(cfg.Defaults.Diagnostics.JournalMaxMiB) << 20
	}
	journal, err := diagnostics.NewJournal(paths, options)
	if err != nil {
		return evidence, err
	}
	if err := activationEvidenceSnapshot(ctx, journal, evidence.Visit); err != nil {
		return evidence, err
	}
	return evidence, nil
}
