package debugger

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestCollectStatusUsesOneSnapshotForHealthAndLatestEvents(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	segment := snapshotConsumerRecord("segment", now.Add(-time.Minute), 1)
	if err := journal.Append(t.Context(), segment); err != nil {
		t.Fatal(err)
	}
	spool := snapshotConsumerRecord("spool", now.Add(-30*time.Second), 2)
	spoolPath := filepath.Join(paths.SpoolDir, "journal-123.jsonl")
	writeSnapshotConsumerRecords(t, spoolPath, spool)
	if err := os.Chtimes(spoolPath, now.Add(-30*time.Second), now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	report, err := CollectStatus(t.Context(), StatusOptions{
		Config: enabledDiagnosticsConfig(256), Paths: paths, Journal: journal,
		Daemon: testHealthClient{}, Now: now, CLIBuild: buildinfo.Info{Version: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != StatusSchemaVersion || StatusSchemaVersion != 2 {
		t.Fatalf("status schema = %d", report.SchemaVersion)
	}
	if report.Journal.Health.Records != 2 || report.Journal.Health.SpoolFiles != 1 || report.Journal.Health.SpoolBytes == 0 {
		t.Fatalf("journal health does not include spool evidence: %+v", report.Journal.Health)
	}
	if report.JournalSnapshot.SegmentFilesRead != 1 || report.JournalSnapshot.SpoolFilesRead != 1 ||
		report.JournalSnapshot.RecordsScanned != 2 || report.JournalSnapshot.RecordsMatched != 2 {
		t.Fatalf("journal snapshot = %+v", report.JournalSnapshot)
	}
	if len(report.LastSeenEvents) != 2 || report.LastSeenEvents[0].TraceID != "segment" || report.LastSeenEvents[1].TraceID != "spool" {
		t.Fatalf("last seen events = %+v", report.LastSeenEvents)
	}
	if report.Journal.Verdict != VerdictHealthy || hasFinding(report.Findings, "journal_spool_stale") {
		t.Fatalf("young spool evidence was treated as stale: journal=%+v findings=%+v", report.Journal, report.Findings)
	}
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("status mutated spool evidence: %v", err)
	}
}

func TestCollectStatusUsesOneTraversalForSelectedDisplayAndAllHookEvidence(t *testing.T) {
	now := time.Now().UTC()
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	workObserved := now.Add(-time.Minute)
	personalObserved := now.Add(-2 * time.Minute)
	work := snapshotConsumerRecord("work-hook", workObserved, 1)
	work.Stage, work.Harness, work.Profile = "hook.received", " CLAUDE ", "work"
	personal := snapshotConsumerRecord("personal-hook", personalObserved, 2)
	personal.Stage, personal.Harness, personal.Profile = "hook.completed", "claude", "personal"
	for _, record := range []diagnostics.TraceEnvelope{work, personal} {
		if err := journal.Append(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	oldSnapshot := statusJournalSnapshot
	traversals := 0
	visitedProfiles := make(map[string]bool)
	statusJournalSnapshot = func(ctx context.Context, journal *diagnostics.Journal, selector diagnostics.Selector, visitor diagnostics.SnapshotVisitor) (diagnostics.JournalSnapshot, error) {
		traversals++
		return journal.SnapshotWithVisitor(ctx, selector, nil, func(envelope diagnostics.TraceEnvelope) error {
			if strings.HasPrefix(envelope.Stage, "hook.") {
				visitedProfiles[envelope.Profile] = true
			}
			return visitor(envelope)
		})
	}
	t.Cleanup(func() { statusJournalSnapshot = oldSnapshot })

	cfg := enabledDiagnosticsConfig(256)
	cfg.Accounts = []config.Account{
		{Name: "work", Harness: "claude", HomeDir: t.TempDir()},
		{Name: "personal", Harness: "claude", HomeDir: t.TempDir()},
	}
	report, err := CollectStatus(t.Context(), StatusOptions{
		Config: cfg, Paths: paths, Journal: journal, Daemon: testHealthClient{},
		AMXBin: "/tmp/amx", Now: now, CLIBuild: buildinfo.Info{Version: "test"}, Selector: Selector{Profile: "work"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if traversals != 1 {
		t.Fatalf("journal traversals = %d, want 1", traversals)
	}
	if !visitedProfiles["work"] || !visitedProfiles["personal"] {
		t.Fatalf("visitor profiles = %v, want work and personal", visitedProfiles)
	}
	if report.JournalSnapshot.RecordsScanned != 2 || report.JournalSnapshot.RecordsMatched != 1 || len(report.LastSeenEvents) != 1 || report.LastSeenEvents[0].Profile != "work" {
		t.Fatalf("selected journal display = snapshot %+v records %+v", report.JournalSnapshot, report.LastSeenEvents)
	}
	if len(report.Hooks.Profiles) != 1 || report.Hooks.Profiles[0].Profile != "work" || report.Hooks.Profiles[0].LastSeen == nil || !report.Hooks.Profiles[0].LastSeen.Equal(workObserved) {
		t.Fatalf("selected hook profiles = %+v", report.Hooks.Profiles)
	}
}

func TestAcceptanceSinglePassStatusSurvivesTinySegmentCompaction(t *testing.T) {
	stateBase := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateBase)
	paths := diagnostics.PathsForRoot(filepath.Join(stateBase, "agent-mux"))
	// A lowered per-commit bound gives a few dozen tiny segments the same
	// multi-pass compaction that thousands meet against the production default.
	const compactionBound = 8
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{CompactionInputFiles: compactionBound})
	if err != nil {
		t.Fatal(err)
	}

	const tinySegmentCount = 5 * compactionBound
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	wantLastSeen := make(map[string]time.Time)
	segmentSetupStarted := time.Now()
	for i := 0; i < tinySegmentCount; i++ {
		profile, harnessName := "work", "claude"
		if i%2 == 1 {
			profile, harnessName = "personal", "codex"
		}
		observed := base.Add(time.Duration(i) * time.Millisecond)
		record := snapshotConsumerRecord(fmt.Sprintf("acceptance-%04d", i), observed, uint64(i+1))
		record.Stage, record.Profile, record.Harness = "hook.received", profile, harnessName
		// Use the production immutable name emitted by spool promotion. Creating
		// the already-promoted fixture directly avoids thousands of directory
		// fsyncs while preserving the diagnostic and compaction surface under test.
		writeSnapshotConsumerRecords(t, filepath.Join(paths.JournalDir, fmt.Sprintf("events-spool-acceptance-%04d.jsonl", i)), record)
		wantLastSeen[profile] = observed
	}
	segmentSetupElapsed := time.Since(segmentSetupStarted)
	segmentsBefore := acceptanceSegmentFiles(t, paths)
	if len(segmentsBefore) != tinySegmentCount {
		t.Fatalf("promoted segments = %d, want %d", len(segmentsBefore), tinySegmentCount)
	}

	cfg := enabledDiagnosticsConfig(256)
	cfg.Defaults.Watchdog.HookSilence = 4 * 60 * 60
	cfg.Accounts = []config.Account{
		{Name: "work", Harness: "claude", HomeDir: t.TempDir()},
		{Name: "personal", Harness: "codex", HomeDir: t.TempDir()},
	}
	statusOptions := StatusOptions{
		Config: cfg, Paths: paths, Journal: journal, Daemon: testHealthClient{},
		AMXBin: "/tmp/amx", Now: base.Add(time.Hour), CLIBuild: buildinfo.Info{Version: "test"},
	}
	oldSnapshot := statusJournalSnapshot
	traversals := 0
	statusJournalSnapshot = func(ctx context.Context, journal *diagnostics.Journal, selector diagnostics.Selector, visitor diagnostics.SnapshotVisitor) (diagnostics.JournalSnapshot, error) {
		traversals++
		return journal.SnapshotWithVisitor(ctx, selector, nil, visitor)
	}
	t.Cleanup(func() { statusJournalSnapshot = oldSnapshot })

	statusStarted := time.Now()
	before, err := CollectStatus(t.Context(), statusOptions)
	beforeStatusElapsed := time.Since(statusStarted)
	if err != nil {
		t.Fatal(err)
	}
	if traversals != 1 {
		t.Fatalf("status traversals = %d, want one", traversals)
	}
	if before.JournalSnapshot.SegmentFilesRead != tinySegmentCount || before.JournalSnapshot.RecordsScanned != tinySegmentCount {
		t.Fatalf("pre-compaction status snapshot = %+v", before.JournalSnapshot)
	}
	for _, profile := range before.Hooks.Profiles {
		want := wantLastSeen[profile.Profile]
		if profile.LastSeen == nil || !profile.LastSeen.Equal(want) {
			t.Fatalf("profile %q last seen = %v, want %s", profile.Profile, profile.LastSeen, want)
		}
	}
	beforeHooks := acceptanceHookStatusDigest(before.Hooks)
	if len(beforeHooks) != len(wantLastSeen) {
		t.Fatalf("hook profiles = %v, want %d profiles", beforeHooks, len(wantLastSeen))
	}
	beforeSnapshot, err := journal.Snapshot(t.Context(), diagnostics.Selector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeSnapshot.Records) != tinySegmentCount {
		t.Fatalf("pre-compaction records = %d, want %d", len(beforeSnapshot.Records), tinySegmentCount)
	}

	compactionStarted := time.Now()
	previousFiles := len(segmentsBefore)
	compactionPasses := 0
	for compactionPasses < 10 {
		compactionPasses++
		report, compactErr := journal.Compact(t.Context())
		if compactErr != nil {
			t.Fatal(compactErr)
		}
		currentFiles := len(acceptanceSegmentFiles(t, paths))
		if report.CompactionInputFiles == 0 || currentFiles >= previousFiles {
			break
		}
		previousFiles = currentFiles
	}
	compactionElapsed := time.Since(compactionStarted)
	if compactionPasses < 2 {
		t.Fatalf("compaction finished in %d pass, want the bound to force several", compactionPasses)
	}
	segmentsAfter := acceptanceSegmentFiles(t, paths)
	if len(segmentsAfter) > 2 {
		t.Fatalf("compaction file count = %d -> %d, want at most 2 segments", len(segmentsBefore), len(segmentsAfter))
	}
	afterSnapshot, err := journal.Snapshot(t.Context(), diagnostics.Selector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterSnapshot.Records) != len(beforeSnapshot.Records) || afterSnapshot.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("post-compaction records/stats = %d/%+v, want %d unique records", len(afterSnapshot.Records), afterSnapshot.Statistics, len(beforeSnapshot.Records))
	}

	statusStarted = time.Now()
	after, err := CollectStatus(t.Context(), statusOptions)
	afterStatusElapsed := time.Since(statusStarted)
	if err != nil {
		t.Fatal(err)
	}
	if traversals != 2 {
		t.Fatalf("two status reports used %d traversals, want two", traversals)
	}
	if got := acceptanceHookStatusDigest(after.Hooks); !maps.Equal(got, beforeHooks) {
		t.Fatalf("hook verdict/last-seen digest changed after compaction: before=%v after=%v", beforeHooks, got)
	}
	t.Logf("synthetic diagnostics acceptance: segment_setup=%s status_before=%s status_after=%s compaction=%s passes=%d files=%d->%d scans=%d->%d records=%d",
		segmentSetupElapsed, beforeStatusElapsed, afterStatusElapsed, compactionElapsed, compactionPasses,
		len(segmentsBefore), len(segmentsAfter), before.JournalSnapshot.RecordsScanned, after.JournalSnapshot.RecordsScanned, len(afterSnapshot.Records))
}

func TestCollectStatusWarnsForStaleOrLegacySpoolAndCountsSpoolQuota(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name        string
		fileName    string
		modTime     time.Time
		legacy      int
		wantWarning bool
	}{
		{name: "stale", fileName: "journal-123.jsonl", modTime: now.Add(-2 * time.Minute), wantWarning: true},
		{name: "legacy", fileName: "journal-123-reconciling-1.claim", modTime: now.Add(-time.Second), legacy: 1, wantWarning: true},
		{name: "young-promoting", fileName: "journal-123-promoting-1.claim", modTime: now.Add(-time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
			journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			spoolPath := filepath.Join(paths.SpoolDir, test.fileName)
			writeSnapshotConsumerRecords(t, spoolPath, snapshotConsumerRecord(test.name, now, 1))
			if err := os.Chtimes(spoolPath, test.modTime, test.modTime); err != nil {
				t.Fatal(err)
			}
			report, err := CollectStatus(t.Context(), StatusOptions{
				Config: enabledDiagnosticsConfig(256), Paths: paths, Journal: journal,
				Daemon: testHealthClient{}, Now: now, CLIBuild: buildinfo.Info{Version: "test"},
			})
			if err != nil {
				t.Fatal(err)
			}
			warning := hasFinding(report.Findings, "journal_spool_stale")
			if warning != test.wantWarning || (report.Journal.Verdict == VerdictWarning) != test.wantWarning ||
				report.Journal.Health.LegacyClaimFiles != test.legacy {
				t.Fatalf("spool warning = journal %+v findings %+v", report.Journal, report.Findings)
			}
		})
	}

	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "quota"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-quota.jsonl")
	data := append([]byte(strings.Repeat(" ", 1<<20)), '\n')
	if err := os.WriteFile(spoolPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(spoolPath, now, now); err != nil {
		t.Fatal(err)
	}
	report, err := CollectStatus(t.Context(), StatusOptions{
		Config: enabledDiagnosticsConfig(1), Paths: paths, Journal: journal,
		Daemon: testHealthClient{}, Now: now, CLIBuild: buildinfo.Info{Version: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(report.Findings, "journal_quota") || hasFinding(report.Findings, "journal_spool_stale") {
		t.Fatalf("spool quota findings = %+v", report.Findings)
	}
}
