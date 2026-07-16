package debugger

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const TailSchemaVersion = 1

type TailOptions struct {
	Selector     Selector
	Limit        int
	PollInterval time.Duration
}

type TailReport struct {
	SchemaVersion     int                         `json:"schema_version"`
	GeneratedAt       time.Time                   `json:"generated_at"`
	Selector          Selector                    `json:"selector"`
	Records           []diagnostics.TraceEnvelope `json:"records"`
	FilesRead         int                         `json:"files_read"`
	CorruptLines      int                         `json:"corrupt_lines"`
	ReconciledFiles   int                         `json:"reconciled_files"`
	ReconciledRecords int                         `json:"reconciled_records"`
}

// Tail reads persisted traces after merging emergency spool files. It does not
// contact the daemon and skips corrupt JSONL records.
func Tail(ctx context.Context, journal *diagnostics.Journal, options TailOptions) (TailReport, error) {
	if journal == nil {
		return TailReport{}, fmt.Errorf("debugger: journal is nil")
	}
	if err := options.Selector.Normalize(); err != nil {
		return TailReport{}, err
	}
	if options.Limit < 0 {
		return TailReport{}, fmt.Errorf("tail limit must not be negative")
	}
	maintenance, err := journal.ReconcileSpool(ctx)
	if err != nil {
		return TailReport{}, err
	}
	read, err := journal.ReadTolerant(ctx, options.Selector.JournalSelector(options.Limit))
	if err != nil {
		return TailReport{}, err
	}
	sortEnvelopes(read.Records)
	return TailReport{
		SchemaVersion: TailSchemaVersion, GeneratedAt: time.Now().UTC(), Selector: options.Selector,
		Records: read.Records, FilesRead: read.FilesRead,
		CorruptLines:    read.CorruptLines + maintenance.CorruptLines,
		ReconciledFiles: maintenance.ReconciledFiles, ReconciledRecords: maintenance.ReconciledRecords,
	}, nil
}

// Follow emits the initial tail and then new records until ctx is cancelled.
func Follow(ctx context.Context, journal *diagnostics.Journal, options TailOptions, emit func(diagnostics.TraceEnvelope) error) error {
	if emit == nil {
		return fmt.Errorf("debugger: tail emitter is nil")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	seen := make(map[string]struct{})
	emitReport := func(report TailReport) error {
		for _, record := range report.Records {
			key := record.SourceID + ":" + fmt.Sprint(record.SourceSequence) + ":" + record.SpanID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if err := emit(record); err != nil {
				return err
			}
		}
		return nil
	}
	report, err := Tail(ctx, journal, options)
	if err != nil {
		return err
	}
	if err := emitReport(report); err != nil {
		return err
	}
	ticker := time.NewTicker(options.PollInterval)
	defer ticker.Stop()
	followOptions := options
	followOptions.Limit = 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			report, err := Tail(ctx, journal, followOptions)
			if err != nil {
				return err
			}
			if err := emitReport(report); err != nil {
				return err
			}
		}
	}
}

func sortEnvelopes(records []diagnostics.TraceEnvelope) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ObservedAt.Equal(records[j].ObservedAt) {
			if records[i].SourceID == records[j].SourceID {
				return records[i].SourceSequence < records[j].SourceSequence
			}
			return records[i].SourceID < records[j].SourceID
		}
		return records[i].ObservedAt.Before(records[j].ObservedAt)
	})
}
