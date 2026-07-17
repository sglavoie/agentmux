package hooks

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestStatusEvidenceKeepsNewestHookRecordByNormalizedIdentity(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	evidence := NewStatusEvidence()
	for _, envelope := range []diagnostics.TraceEnvelope{
		{Stage: "hook.received", Harness: " CLAUDE ", Profile: " Work ", ObservedAt: base},
		{Stage: "daemon.applied", Harness: "claude", Profile: "work", ObservedAt: base.Add(3 * time.Hour)},
		{Stage: "hook.completed", Harness: "claude", Profile: "WORK", ObservedAt: base.Add(2 * time.Hour)},
		{Stage: "hook.received", Harness: "CODEX", Profile: "work", ObservedAt: base.Add(time.Hour)},
		{Stage: "hook.received", Harness: "claude", Profile: "personal", ObservedAt: base.Add(90 * time.Minute)},
	} {
		if err := evidence.Visit(envelope); err != nil {
			t.Fatal(err)
		}
	}

	if got := evidence.latest("Claude", "work"); !got.Equal(base.Add(2 * time.Hour)) {
		t.Fatalf("claude/work last seen = %s, want %s", got, base.Add(2*time.Hour))
	}
	if got := evidence.latest("codex", "WORK"); !got.Equal(base.Add(time.Hour)) {
		t.Fatalf("codex/work last seen = %s, want %s", got, base.Add(time.Hour))
	}
	if got := evidence.latest("claude", "personal"); !got.Equal(base.Add(90 * time.Minute)) {
		t.Fatalf("claude/personal last seen = %s, want %s", got, base.Add(90*time.Minute))
	}
}

func TestStatusAllWithEvidencePreservesMissingUnavailableAndDisabledFindings(t *testing.T) {
	stubActivationRuntimeWithoutEvidence(t, map[string]string{"claude": "2.1.195"})
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	statuses, err := StatusAllWithEvidence(cfg, testBin, NewStatusEvidence(), nil)
	if err != nil {
		t.Fatalf("missing evidence status: %v", err)
	}
	if len(statuses) != 1 || !hasActivationIssue(statuses[0], "last_seen_missing") || hasActivationIssue(statuses[0], "last_seen_unavailable") {
		t.Fatalf("missing evidence status = %+v", statuses)
	}

	evidenceErr := errors.New("journal unavailable")
	statuses, err = StatusAllWithEvidence(cfg, testBin, NewStatusEvidence(), evidenceErr)
	if err != nil {
		t.Fatalf("unavailable evidence status: %v", err)
	}
	if len(statuses) != 1 || !hasActivationIssue(statuses[0], "last_seen_unavailable") || hasActivationIssue(statuses[0], "last_seen_missing") {
		t.Fatalf("unavailable evidence status = %+v", statuses)
	}

	disabled := false
	cfg.Defaults.Diagnostics.JournalEnabled = &disabled
	statuses, err = StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("disabled journal status: %v", err)
	}
	if len(statuses) != 1 || !hasActivationIssue(statuses[0], "last_seen_unavailable") {
		t.Fatalf("disabled journal status = %+v", statuses)
	}

	enabled := true
	cfg.Defaults.Diagnostics.JournalEnabled = &enabled
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.JournalDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	statuses, err = StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("unavailable journal status: %v", err)
	}
	if len(statuses) != 1 || !hasActivationIssue(statuses[0], "last_seen_unavailable") {
		t.Fatalf("unavailable journal status = %+v", statuses)
	}
}

func TestStatusAllWithEvidenceNilEvidenceReportsMissing(t *testing.T) {
	stubActivationRuntimeWithoutEvidence(t, map[string]string{"claude": "2.1.195"})
	cfg := &config.Config{Accounts: []config.Account{
		{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()},
		{Name: "personal", Harness: harness.Claude, HomeDir: t.TempDir()},
	}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	statuses, err := StatusAllWithEvidence(cfg, testBin, nil, nil)
	if err != nil {
		t.Fatalf("StatusAllWithEvidence: %v", err)
	}
	if len(statuses) != len(cfg.Accounts) {
		t.Fatalf("statuses = %d, want %d", len(statuses), len(cfg.Accounts))
	}
	for _, status := range statuses {
		if !hasActivationIssue(status, "last_seen_missing") {
			t.Errorf("%s activation issues = %+v, want last_seen_missing", status.Account, status.ActivationIssues)
		}
		if hasActivationIssue(status, "last_seen_unavailable") {
			t.Errorf("%s activation issues = %+v, do not want last_seen_unavailable", status.Account, status.ActivationIssues)
		}
	}
}

func TestStatusAllTraversesJournalOnceForMixedProfilesAndCorruptRecords(t *testing.T) {
	stubActivationRuntimeWithoutEvidence(t, map[string]string{"claude": "2.1.195"})
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Accounts: []config.Account{
		{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()},
		{Name: "personal", Harness: harness.Claude, HomeDir: t.TempDir()},
	}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	base := time.Now().UTC().Add(-time.Minute)
	for sequence, envelope := range []diagnostics.TraceEnvelope{
		{Stage: "hook.received", Harness: " CLAUDE ", Profile: " WORK ", ObservedAt: base},
		{Stage: "daemon.applied", Harness: "claude", Profile: "work", ObservedAt: base.Add(45 * time.Second)},
		{Stage: "hook.completed", Harness: "claude", Profile: "work", ObservedAt: base.Add(30 * time.Second)},
		{Stage: "hook.received", Harness: "Claude", Profile: "PERSONAL", ObservedAt: base.Add(15 * time.Second)},
	} {
		envelope.SourceID = "status-evidence"
		envelope.SourceSequence = uint64(sequence + 1)
		if err := journal.Append(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	active, err := os.OpenFile(paths.JournalFile, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := active.WriteString("{corrupt}\n"); err != nil {
		_ = active.Close()
		t.Fatal(err)
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}

	oldSnapshot := activationEvidenceSnapshot
	traversals := 0
	activationEvidenceSnapshot = func(ctx context.Context, journal *diagnostics.Journal, visitor diagnostics.SnapshotVisitor) error {
		traversals++
		return oldSnapshot(ctx, journal, visitor)
	}
	t.Cleanup(func() { activationEvidenceSnapshot = oldSnapshot })

	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if traversals != 1 {
		t.Fatalf("journal traversals = %d, want 1", traversals)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %d, want 2", len(statuses))
	}
	if !statuses[0].LastSeen.Equal(base.Add(30 * time.Second)) {
		t.Fatalf("work last seen = %s, want %s", statuses[0].LastSeen, base.Add(30*time.Second))
	}
	if !statuses[1].LastSeen.Equal(base.Add(15 * time.Second)) {
		t.Fatalf("personal last seen = %s, want %s", statuses[1].LastSeen, base.Add(15*time.Second))
	}
}

func stubActivationRuntimeWithoutEvidence(t *testing.T, versions map[string]string) {
	t.Helper()
	oldVersionProbe := activationVersionProbe
	oldClaudeRoot := activationClaudeManagedRoot
	activationVersionProbe = func(_ context.Context, executable string) (string, error) {
		return versions[executable], nil
	}
	claudeRoot := t.TempDir()
	activationClaudeManagedRoot = func() string { return claudeRoot }
	t.Cleanup(func() {
		activationVersionProbe = oldVersionProbe
		activationClaudeManagedRoot = oldClaudeRoot
	})
}
