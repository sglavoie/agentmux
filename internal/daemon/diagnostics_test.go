package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func boolPointer(value bool) *bool { return &value }

type testJournalMaintainer struct {
	calls     chan string
	promote   func(context.Context) (diagnostics.MaintenanceReport, error)
	compact   func(context.Context) (diagnostics.MaintenanceReport, error)
	retention func(context.Context) (diagnostics.MaintenanceReport, error)
}

func (m *testJournalMaintainer) PromoteSpool(ctx context.Context) (diagnostics.MaintenanceReport, error) {
	m.calls <- "promote"
	if m.promote != nil {
		return m.promote(ctx)
	}
	return diagnostics.MaintenanceReport{PromotedRecords: 1, PromotedFiles: 1}, nil
}

func (m *testJournalMaintainer) Compact(ctx context.Context) (diagnostics.MaintenanceReport, error) {
	m.calls <- "compact"
	if m.compact != nil {
		return m.compact(ctx)
	}
	return diagnostics.MaintenanceReport{
		CompactionInputFiles: 2, CompactionInputBytes: 200,
		CompactionOutputFiles: 1, CompactionOutputBytes: 100,
		CompactionDuplicatesOmitted: 1,
	}, nil
}

func (m *testJournalMaintainer) RotateAndRetain(ctx context.Context) (diagnostics.MaintenanceReport, error) {
	m.calls <- "retention"
	if m.retention != nil {
		return m.retention(ctx)
	}
	return diagnostics.MaintenanceReport{RotatedFiles: 1, RemovedFiles: 1}, nil
}

func TestJournalMaintenanceScheduleAndPassBudget(t *testing.T) {
	t.Parallel()

	if journalPromotionInterval != 60*time.Second || journalRetentionInterval != 15*time.Minute || journalMaintenanceBudget != 5*time.Second {
		t.Fatalf("production maintenance schedule = promotion %v retention %v budget %v",
			journalPromotionInterval, journalRetentionInterval, journalMaintenanceBudget)
	}
	maintainer := &testJournalMaintainer{calls: make(chan string, 32)}
	type maintenanceRecord struct {
		outcome  string
		reason   string
		evidence map[string]string
	}
	records := make(chan maintenanceRecord, 32)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runJournalMaintenance(ctx, maintainer, func(_ string, outcome, reason string, evidence map[string]string) {
			records <- maintenanceRecord{outcome: outcome, reason: reason, evidence: evidence}
		}, journalMaintenanceSchedule{
			promotionInterval: 10 * time.Millisecond,
			retentionInterval: 15 * time.Millisecond,
			passBudget:        100 * time.Millisecond,
		})
		close(done)
	}()
	counts := map[string]int{}
	var calls []string
	deadline := time.After(time.Second)
	for counts["promote"] < 2 || counts["compact"] < 2 || counts["retention"] < 2 {
		select {
		case call := <-maintainer.calls:
			counts[call]++
			calls = append(calls, call)
		case <-deadline:
			cancel()
			t.Fatalf("maintenance calls = %v, want startup and periodic calls", counts)
		}
	}
	if len(calls) < 3 || calls[0] != "promote" || calls[1] != "compact" || calls[2] != "retention" {
		t.Fatalf("startup maintenance calls = %v, want promote, compact, retention", calls)
	}
	for i, call := range calls {
		if call == "promote" && i >= 3 && (i+1 >= len(calls) || calls[i+1] != "compact") {
			t.Fatalf("periodic promotion was not immediately paired with compaction: %v", calls)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance loop ignored cancellation")
	}
	close(records)
	seenStartupPromotion := false
	seenStartupCompaction := false
	seenStartupRetention := false
	seenPeriodicCompaction := false
	for record := range records {
		seenStartupPromotion = seenStartupPromotion || record.outcome == "success" && record.reason == "startup_spool_promotion"
		seenStartupRetention = seenStartupRetention || record.outcome == "success" && record.reason == "startup_rotation_retention"
		if record.outcome == "success" && (record.reason == "startup_compaction" || record.reason == "periodic_compaction") {
			if record.evidence["compaction_input_files"] != "2" || record.evidence["compaction_input_bytes"] != "200" ||
				record.evidence["compaction_output_files"] != "1" || record.evidence["compaction_output_bytes"] != "100" ||
				record.evidence["compaction_duplicates_omitted"] != "1" {
				t.Fatalf("compaction maintenance evidence = %v", record.evidence)
			}
			seenStartupCompaction = seenStartupCompaction || record.reason == "startup_compaction"
			seenPeriodicCompaction = seenPeriodicCompaction || record.reason == "periodic_compaction"
		}
	}
	if !seenStartupPromotion || !seenStartupCompaction || !seenStartupRetention || !seenPeriodicCompaction {
		t.Fatalf("maintenance outcomes missing: promotion=%v startup_compaction=%v retention=%v periodic_compaction=%v",
			seenStartupPromotion, seenStartupCompaction, seenStartupRetention, seenPeriodicCompaction)
	}
}

func TestJournalMaintenanceContinuesAfterCompactionFailure(t *testing.T) {
	t.Parallel()

	maintainer := &testJournalMaintainer{
		calls: make(chan string, 16),
		compact: func(context.Context) (diagnostics.MaintenanceReport, error) {
			return diagnostics.MaintenanceReport{}, fmt.Errorf("compaction failed")
		},
	}
	type maintenanceRecord struct {
		stage   string
		outcome string
		reason  string
	}
	records := make(chan maintenanceRecord, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runJournalMaintenance(ctx, maintainer, func(stage, outcome, reason string, _ map[string]string) {
			records <- maintenanceRecord{stage: stage, outcome: outcome, reason: reason}
		}, journalMaintenanceSchedule{
			promotionInterval: 10 * time.Millisecond,
			retentionInterval: time.Hour,
			passBudget:        100 * time.Millisecond,
		})
		close(done)
	}()

	var calls []string
	deadline := time.After(time.Second)
	for len(calls) < 4 {
		select {
		case call := <-maintainer.calls:
			calls = append(calls, call)
		case <-deadline:
			cancel()
			t.Fatalf("maintenance calls = %v, want startup promote/compact/retention and periodic promotion", calls)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance loop ignored cancellation")
	}
	wantCalls := []string{"promote", "compact", "retention", "promote"}
	for i, want := range wantCalls {
		if calls[i] != want {
			t.Fatalf("maintenance calls = %v, want %v", calls[:4], wantCalls)
		}
	}

	close(records)
	for record := range records {
		if record.stage == "journal.maintenance" && record.outcome == "error" && strings.HasPrefix(record.reason, "startup_compaction: ") {
			return
		}
	}
	t.Fatal("startup compaction failure was not recorded as an error journal.maintenance outcome")
}

func TestJournalMaintenanceRecordsOnlyAfterTimedPassReturns(t *testing.T) {
	t.Parallel()

	var insidePass atomic.Bool
	maintainer := &testJournalMaintainer{
		calls: make(chan string, 2),
		promote: func(ctx context.Context) (diagnostics.MaintenanceReport, error) {
			insidePass.Store(true)
			<-ctx.Done()
			insidePass.Store(false)
			return diagnostics.MaintenanceReport{}, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	recorded := make(chan bool, 1)
	started := time.Now()
	go func() {
		runJournalMaintenance(ctx, maintainer, func(_ string, outcome, _ string, _ map[string]string) {
			recorded <- outcome == "error" && !insidePass.Load()
			cancel()
		}, journalMaintenanceSchedule{
			promotionInterval: time.Hour,
			retentionInterval: time.Hour,
			passBudget:        20 * time.Millisecond,
		})
		close(done)
	}()
	select {
	case valid := <-recorded:
		if !valid {
			t.Fatal("maintenance outcome was recorded before the timed pass released its work")
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed maintenance pass did not report")
	}
	<-done
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("maintenance pass elapsed = %v, want bounded by its timeout", elapsed)
	}
}

func TestJournalMaintenanceDoesNotRecordLifecycleCancellationAsError(t *testing.T) {
	t.Parallel()

	maintainer := &testJournalMaintainer{
		calls: make(chan string, 2),
		promote: func(ctx context.Context) (diagnostics.MaintenanceReport, error) {
			<-ctx.Done()
			return diagnostics.MaintenanceReport{}, fmt.Errorf("promotion interrupted: %w", ctx.Err())
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	recorded := make(chan string, 1)
	go func() {
		runJournalMaintenance(ctx, maintainer, func(_ string, outcome, reason string, _ map[string]string) {
			recorded <- outcome + ":" + reason
		}, journalMaintenanceSchedule{
			promotionInterval: time.Hour,
			retentionInterval: time.Hour,
			passBudget:        time.Hour,
		})
		close(done)
	}()

	select {
	case call := <-maintainer.calls:
		if call != "promote" {
			t.Fatalf("first maintenance call = %q, want promote", call)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup maintenance did not begin")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance pass ignored lifecycle cancellation")
	}
	select {
	case record := <-recorded:
		t.Fatalf("lifecycle cancellation recorded a maintenance outcome: %q", record)
	default:
	}
}
