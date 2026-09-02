package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const (
	journalPromotionInterval = 60 * time.Second
	journalRetentionInterval = 15 * time.Minute
	journalMaintenanceBudget = 5 * time.Second
)

type journalMaintainer interface {
	PromoteSpool(context.Context) (diagnostics.MaintenanceReport, error)
	Compact(context.Context) (diagnostics.MaintenanceReport, error)
	RotateAndRetain(context.Context) (diagnostics.MaintenanceReport, error)
}

type journalMaintenanceSchedule struct {
	promotionInterval time.Duration
	retentionInterval time.Duration
	passBudget        time.Duration
}

var productionJournalMaintenanceSchedule = journalMaintenanceSchedule{
	promotionInterval: journalPromotionInterval,
	retentionInterval: journalRetentionInterval,
	passBudget:        journalMaintenanceBudget,
}

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
	runID := os.Getenv(process.LaunchIDEnv)
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

func (r *lifecycleRecorder) runJournalMaintenance(ctx context.Context) {
	if r == nil {
		return
	}
	runJournalMaintenance(ctx, r.journal, r.record, productionJournalMaintenanceSchedule)
}

func runJournalMaintenance(
	ctx context.Context,
	journal journalMaintainer,
	record func(string, string, string, map[string]string),
	schedule journalMaintenanceSchedule,
) {
	if journal == nil {
		return
	}
	if record == nil {
		record = func(string, string, string, map[string]string) {}
	}
	runPass := func(reason string, maintain func(context.Context) (diagnostics.MaintenanceReport, error)) {
		passCtx, cancel := context.WithTimeout(ctx, schedule.passBudget)
		report, err := maintain(passCtx)
		cancel()
		if parentErr := ctx.Err(); err != nil && parentErr != nil && errors.Is(err, parentErr) {
			return
		}
		evidence := map[string]string{
			"promoted_records":              fmt.Sprint(report.PromotedRecords),
			"promoted_files":                fmt.Sprint(report.PromotedFiles),
			"busy_spool_files":              fmt.Sprint(report.BusySpoolFiles),
			"corrupt_lines":                 fmt.Sprint(report.CorruptLines),
			"quarantined_files":             fmt.Sprint(len(report.QuarantinedFiles)),
			"compaction_input_files":        fmt.Sprint(report.CompactionInputFiles),
			"compaction_input_bytes":        fmt.Sprint(report.CompactionInputBytes),
			"compaction_output_files":       fmt.Sprint(report.CompactionOutputFiles),
			"compaction_output_bytes":       fmt.Sprint(report.CompactionOutputBytes),
			"compaction_duplicates_omitted": fmt.Sprint(report.CompactionDuplicatesOmitted),
			"rotated_files":                 fmt.Sprint(report.RotatedFiles),
			"removed_files":                 fmt.Sprint(report.RemovedFiles),
			"removed_bytes":                 fmt.Sprint(report.RemovedBytes),
		}
		if err != nil {
			record("journal.maintenance", "error", reason+": "+err.Error(), evidence)
			return
		}
		record("journal.maintenance", "success", reason, evidence)
	}

	runPass("startup_spool_promotion", journal.PromoteSpool)
	if ctx.Err() != nil {
		return
	}
	runPass("startup_compaction", journal.Compact)
	if ctx.Err() != nil {
		return
	}
	runPass("startup_rotation_retention", journal.RotateAndRetain)
	if ctx.Err() != nil {
		return
	}
	promotionTicker := time.NewTicker(schedule.promotionInterval)
	retentionTicker := time.NewTicker(schedule.retentionInterval)
	defer promotionTicker.Stop()
	defer retentionTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-promotionTicker.C:
			runPass("periodic_spool_promotion", journal.PromoteSpool)
			if ctx.Err() != nil {
				return
			}
			runPass("periodic_compaction", journal.Compact)
		case <-retentionTicker.C:
			runPass("periodic_rotation_retention", journal.RotateAndRetain)
		}
	}
}
