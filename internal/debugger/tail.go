package debugger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const TailSchemaVersion = 2

type TailOptions struct {
	Selector     Selector
	Limit        int
	PollInterval time.Duration
}

type TailReport struct {
	SchemaVersion   int                            `json:"schema_version"`
	GeneratedAt     time.Time                      `json:"generated_at"`
	Selector        Selector                       `json:"selector"`
	JournalSnapshot diagnostics.SnapshotStatistics `json:"journal_snapshot"`
	Records         []diagnostics.TraceEnvelope    `json:"records"`
}

// TailFollowRecord is the v2 JSONL wrapper for one record discovered by a
// follow snapshot. Snapshot metadata is repeated so every line remains
// independently interpretable when captured or streamed.
type TailFollowRecord struct {
	SchemaVersion   int                            `json:"schema_version"`
	JournalSnapshot diagnostics.SnapshotStatistics `json:"journal_snapshot"`
	Record          diagnostics.TraceEnvelope      `json:"record"`
}

// Tail reads a merged, read-only snapshot of journal and emergency spool
// evidence. It does not contact the daemon and skips corrupt JSONL records.
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
	snapshot, err := journal.Snapshot(ctx, options.Selector.JournalSelector(options.Limit), nil)
	if err != nil {
		return TailReport{}, err
	}
	return TailReport{
		SchemaVersion: TailSchemaVersion, GeneratedAt: time.Now().UTC(), Selector: options.Selector,
		JournalSnapshot: snapshot.Statistics, Records: snapshot.Records,
	}, nil
}

// Follow emits the initial tail and then new records until ctx is cancelled.
// Callers that need per-poll snapshot metadata can use FollowWithMetadata.
func Follow(ctx context.Context, journal *diagnostics.Journal, options TailOptions, emit func(diagnostics.TraceEnvelope) error) error {
	if emit == nil {
		return fmt.Errorf("debugger: tail emitter is nil")
	}
	return FollowWithMetadata(ctx, journal, options, func(wrapper TailFollowRecord) error {
		return emit(wrapper.Record)
	})
}

// FollowWithMetadata emits the v2 JSONL-shaped record wrapper used by the CLI.
func FollowWithMetadata(ctx context.Context, journal *diagnostics.Journal, options TailOptions, emit func(TailFollowRecord) error) error {
	if journal == nil {
		return fmt.Errorf("debugger: journal is nil")
	}
	if emit == nil {
		return fmt.Errorf("debugger: tail emitter is nil")
	}
	if err := options.Selector.Normalize(); err != nil {
		return err
	}
	if options.Limit < 0 {
		return fmt.Errorf("tail limit must not be negative")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	seen := make(map[[sha256.Size]byte]struct{})
	emitSnapshot := func(snapshot diagnostics.JournalSnapshot, first int) error {
		for index, record := range snapshot.Records {
			key, err := followRecordIdentity(record)
			if err != nil {
				return err
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if index < first {
				continue
			}
			if err := emit(TailFollowRecord{
				SchemaVersion: TailSchemaVersion, JournalSnapshot: snapshot.Statistics, Record: record,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	// Remember every initially matched record even when only the latest records
	// are emitted. If maintenance replaces an inode, a later full-file rescan
	// must not turn history hidden by --last into new follow output.
	selector := options.Selector.JournalSelector(0)
	snapshot, err := journal.Snapshot(ctx, selector, nil)
	if err != nil {
		return err
	}
	first := 0
	if options.Limit > 0 && len(snapshot.Records) > options.Limit {
		first = len(snapshot.Records) - options.Limit
	}
	if err := emitSnapshot(snapshot, first); err != nil {
		return err
	}
	cursor := snapshot.Cursor
	ticker := time.NewTicker(options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			snapshot, err := journal.Snapshot(ctx, selector, &cursor)
			if err != nil {
				return err
			}
			cursor = snapshot.Cursor
			if err := emitSnapshot(snapshot, 0); err != nil {
				return err
			}
		}
	}
}

func followRecordIdentity(record diagnostics.TraceEnvelope) ([sha256.Size]byte, error) {
	if record.SourceID != "" && record.SourceSequence != 0 {
		return sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", record.SourceID, record.SourceSequence))), nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("debugger: encoding follow record identity: %w", err)
	}
	return sha256.Sum256(data), nil
}

func sortEnvelopes(records []diagnostics.TraceEnvelope) {
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].ObservedAt.Equal(records[j].ObservedAt) {
			return records[i].ObservedAt.Before(records[j].ObservedAt)
		}
		if records[i].SourceID != records[j].SourceID {
			return records[i].SourceID < records[j].SourceID
		}
		if records[i].SourceSequence != records[j].SourceSequence {
			return records[i].SourceSequence < records[j].SourceSequence
		}
		if records[i].SpanID != records[j].SpanID {
			return records[i].SpanID < records[j].SpanID
		}
		return records[i].TraceID < records[j].TraceID
	})
}
