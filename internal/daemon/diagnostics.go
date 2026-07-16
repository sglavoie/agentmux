package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type lifecycleRecorder struct {
	journal *diagnostics.Journal
	traceID string
	runID   string
}

func newLifecycleRecorder(cfg *config.Config) (*lifecycleRecorder, error) {
	if cfg == nil || !cfg.Defaults.Diagnostics.JournalEnabledValue() {
		return nil, nil
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return nil, err
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{
		KeepDays: cfg.Defaults.Diagnostics.JournalKeepDays,
		MaxBytes: int64(cfg.Defaults.Diagnostics.JournalMaxMiB) << 20,
	})
	if err != nil {
		return nil, err
	}
	seed := diagnostics.NewEnvelope("daemon.lifecycle")
	runID := os.Getenv(daemonLaunchIDEnv)
	if runID == "" {
		runID = fmt.Sprintf("daemon-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return &lifecycleRecorder{journal: journal, traceID: seed.TraceID, runID: runID}, nil
}

func (r *lifecycleRecorder) record(stage, outcome, reason string, evidence map[string]string) {
	if r == nil {
		return
	}
	envelope := diagnostics.NewEnvelope(stage)
	envelope.TraceID = r.traceID
	envelope.RunID = r.runID
	envelope.Outcome = outcome
	envelope.Reason = reason
	envelope.Evidence = evidence
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r.journal.TryAppend(ctx, envelope)
}

func (r *lifecycleRecorder) maintain(ctx context.Context) {
	if r == nil {
		return
	}
	report, err := r.journal.Maintain(ctx)
	if err != nil {
		r.record("journal.maintenance", "error", err.Error(), nil)
		return
	}
	r.record("journal.maintenance", "success", "startup", map[string]string{
		"reconciled_records": fmt.Sprint(report.ReconciledRecords),
		"reconciled_files":   fmt.Sprint(report.ReconciledFiles),
		"quarantined_files":  fmt.Sprint(len(report.QuarantinedFiles)),
		"rotated_files":      fmt.Sprint(report.RotatedFiles),
		"removed_files":      fmt.Sprint(report.RemovedFiles),
	})
}
