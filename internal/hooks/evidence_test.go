package hooks

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestStatusEvidenceCreditsProducerHomeWithLegacyFallback(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	evidence := NewStatusEvidence()
	personal := "/Users/dev/.claude-personal"
	codexHome := "/Users/dev/.codex"
	for _, envelope := range []diagnostics.TraceEnvelope{
		// The routed cross-harness profile on a delivery record must not decide
		// crediting; the producer-stamped config_dir does. Trailing separators
		// are cleaned into the same bucket.
		{Stage: "hook.delivery", Harness: " CLAUDE ", Profile: "personal-codex", Evidence: map[string]string{"config_dir": personal + "/"}, ObservedAt: base},
		{Stage: "daemon.decision", Harness: "claude", Evidence: map[string]string{"config_dir": personal}, ObservedAt: base.Add(3 * time.Hour)},
		{Stage: "hook.acknowledgement", Harness: "claude", Evidence: map[string]string{"config_dir": personal}, ObservedAt: base.Add(2 * time.Hour)},
		{Stage: "hook.received", Harness: "CODEX", Evidence: map[string]string{"config_dir": codexHome}, ObservedAt: base.Add(time.Hour)},
		// Pre-upgrade client record without config_dir lands in the legacy bucket.
		{Stage: "hook.received", Harness: "claude", Profile: "personal", ObservedAt: base.Add(30 * time.Minute)},
	} {
		if err := evidence.Visit(envelope); err != nil {
			t.Fatal(err)
		}
	}

	if got := evidence.latestForHome("Claude", personal); !got.Equal(base.Add(2 * time.Hour)) {
		t.Fatalf("claude/%s last seen = %s, want %s (non-hook stages ignored)", personal, got, base.Add(2*time.Hour))
	}
	if got := evidence.latestForHome("codex", codexHome+"/"); !got.Equal(base.Add(time.Hour)) {
		t.Fatalf("codex/%s last seen = %s, want %s", codexHome, got, base.Add(time.Hour))
	}
	// The claude record routed to a codex profile never credits the codex harness.
	if got := evidence.latestForHome("codex", personal); !got.IsZero() {
		t.Fatalf("codex/%s last seen = %s, want zero (cross-harness routed profile must not credit)", personal, got)
	}
	// A home with no exact records falls back to the harness's legacy bucket.
	if got := evidence.latestForHome("claude", "/Users/dev/.claude-other"); !got.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("claude legacy fallback = %s, want %s", got, base.Add(30*time.Minute))
	}

	// A newer legacy record also wins over older exact-home evidence so a
	// mixed-build client can never look "missing" or stale-er than reality.
	if err := evidence.Visit(diagnostics.TraceEnvelope{Stage: "hook.received", Harness: "claude", ObservedAt: base.Add(5 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := evidence.latestForHome("claude", personal); !got.Equal(base.Add(5 * time.Hour)) {
		t.Fatalf("claude newer legacy fallback = %s, want %s", got, base.Add(5*time.Hour))
	}
}

func TestStatusEvidenceHorizonDistinguishesShallowEmptyAndDeepJournals(t *testing.T) {
	stubActivationRuntimeWithoutEvidence(t, map[string]string{"claude": "2.1.195"})
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: t.TempDir()}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	// An empty journal has pruned nothing, so the "missing" claim is honest.
	statuses, err := StatusAllWithEvidence(cfg, testBin, NewStatusEvidence(), nil)
	if err != nil {
		t.Fatalf("empty journal status: %v", err)
	}
	if !hasActivationIssue(statuses[0], "last_seen_missing") || hasActivationIssue(statuses[0], "last_seen_horizon") {
		t.Fatalf("empty journal issues = %+v, want last_seen_missing only", statuses[0].ActivationIssues)
	}

	// A shallow journal cannot prove silence: history older than its earliest
	// record may have been pruned. Non-hook stages also anchor the horizon.
	shallow := NewStatusEvidence()
	if err := shallow.Visit(diagnostics.TraceEnvelope{Stage: "daemon.decision", ObservedAt: time.Now().UTC().Add(-12 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	statuses, err = StatusAllWithEvidence(cfg, testBin, shallow, nil)
	if err != nil {
		t.Fatalf("shallow journal status: %v", err)
	}
	if !hasActivationIssue(statuses[0], "last_seen_horizon") || hasActivationIssue(statuses[0], "last_seen_missing") {
		t.Fatalf("shallow journal issues = %+v, want last_seen_horizon only", statuses[0].ActivationIssues)
	}
	for _, issue := range statuses[0].ActivationIssues {
		if issue.Code == "last_seen_horizon" {
			if issue.Verdict != ActivationUnverified || !strings.Contains(issue.Message, "shorter than the hook verification window (4h)") {
				t.Fatalf("last_seen_horizon issue = %+v", issue)
			}
		}
	}

	// A journal that covers the whole verification window proves silence.
	deep := NewStatusEvidence()
	if err := deep.Visit(diagnostics.TraceEnvelope{Stage: "daemon.decision", ObservedAt: time.Now().UTC().Add(-5 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	statuses, err = StatusAllWithEvidence(cfg, testBin, deep, nil)
	if err != nil {
		t.Fatalf("deep journal status: %v", err)
	}
	if !hasActivationIssue(statuses[0], "last_seen_missing") || hasActivationIssue(statuses[0], "last_seen_horizon") {
		t.Fatalf("deep journal issues = %+v, want last_seen_missing only", statuses[0].ActivationIssues)
	}
}

func TestStatusEvidenceDeliveryFailuresTerminalizePerRequest(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	evidence := NewStatusEvidence()
	// Records are intentionally interleaved across requests and out of stage
	// order to prove terminalization is order-independent.
	for _, envelope := range []diagnostics.TraceEnvelope{
		// req "ok": a failed IPC attempt that a retry resolves — never counts.
		{Stage: "hook.ipc_attempt", RequestID: "ok", Outcome: "error", ObservedAt: now.Add(-12 * time.Minute)},
		{Stage: "hook.ipc_attempt", RequestID: "ok", Outcome: "success", ObservedAt: now.Add(-11 * time.Minute)},
		{Stage: "hook.delivery", RequestID: "ok", Outcome: "accepted", ObservedAt: now.Add(-11 * time.Minute)},
		// req "exhausted": many failed attempts plus a terminal failure delivery
		// — counts exactly once, even though hook.failure also fires.
		{Stage: "hook.delivery", RequestID: "exhausted", Outcome: "retryable_failure", ObservedAt: now.Add(-9 * time.Minute)},
		{Stage: "hook.ipc_attempt", RequestID: "exhausted", Outcome: "error", ObservedAt: now.Add(-10 * time.Minute)},
		{Stage: "hook.ipc_attempt", RequestID: "exhausted", Outcome: "error", ObservedAt: now.Add(-9 * time.Minute)},
		{Stage: "hook.failure", RequestID: "exhausted", Outcome: "error", ObservedAt: now.Add(-9 * time.Minute)},
		// req "legacy": a hook.failure error with no correlated hook.delivery —
		// the fallback terminal counts once.
		{Stage: "hook.failure", RequestID: "legacy", Outcome: "error", ObservedAt: now.Add(-20 * time.Minute)},
		// req "dup": a duplicate acknowledgement is a success-class terminal.
		{Stage: "hook.delivery", RequestID: "dup", Outcome: "duplicate", ObservedAt: now.Add(-5 * time.Minute)},
		// req "old": a terminal failure older than the trailing hour is excluded.
		{Stage: "hook.delivery", RequestID: "old", Outcome: "permanent_failure", ObservedAt: now.Add(-2 * time.Hour)},
		// A non-hook stage never contributes a delivery verdict.
		{Stage: "daemon.decision", RequestID: "exhausted", Outcome: "error", ObservedAt: now.Add(-5 * time.Minute)},
	} {
		if err := evidence.Visit(envelope); err != nil {
			t.Fatal(err)
		}
	}
	count, latest := evidence.DeliveryFailures(now)
	if count != 2 || !latest.Equal(now.Add(-9*time.Minute)) {
		t.Fatalf("delivery failures = %d latest %s, want 2 latest %s", count, latest, now.Add(-9*time.Minute))
	}
	// The horizon spans every visited record, including the out-of-window one.
	if horizon, nonEmpty := evidence.Horizon(now); !nonEmpty || horizon != 2*time.Hour {
		t.Fatalf("horizon = %s nonEmpty %t, want 2h true", horizon, nonEmpty)
	}
	if _, nonEmpty := NewStatusEvidence().Horizon(now); nonEmpty {
		t.Fatal("empty evidence reported a non-empty horizon")
	}
}

// TestStatusEvidenceDeliveryFailuresIgnoresTransientAttemptOnlyRequest proves a
// request whose only failure evidence is an intermediate transport error — with
// no terminal hook.delivery or hook.failure — is never counted as dropped.
func TestStatusEvidenceDeliveryFailuresIgnoresTransientAttemptOnlyRequest(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	evidence := NewStatusEvidence()
	for _, envelope := range []diagnostics.TraceEnvelope{
		{Stage: "hook.ipc_attempt", RequestID: "pending", Outcome: "error", ObservedAt: now.Add(-3 * time.Minute)},
		{Stage: "hook.ipc_attempt", RequestID: "pending", Outcome: "error", ObservedAt: now.Add(-2 * time.Minute)},
	} {
		if err := evidence.Visit(envelope); err != nil {
			t.Fatal(err)
		}
	}
	if count, latest := evidence.DeliveryFailures(now); count != 0 || !latest.IsZero() {
		t.Fatalf("attempt-only delivery failures = %d latest %s, want 0 and zero time", count, latest)
	}
}

// A harness session started outside Kitty still runs the hooks its home
// installs, and the client declines them as outside_kitty. Nothing was lost, so
// neither the terminal record nor a legacy hook.failure counts as a dropped
// delivery, while a real failure beside them still does.
func TestStatusEvidenceDeliveryFailuresExcludeHooksOutsideKitty(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	evidence := NewStatusEvidence()
	for _, envelope := range []diagnostics.TraceEnvelope{
		{Stage: "hook.delivery", RequestID: "ide", Outcome: "permanent_failure", Reason: "outside_kitty", ObservedAt: now.Add(-3 * time.Minute)},
		{Stage: "hook.failure", RequestID: "legacy-ide", Outcome: "error", Reason: "outside_kitty", ObservedAt: now.Add(-2 * time.Minute)},
		{Stage: "hook.delivery", RequestID: "lost", Outcome: "retryable_failure", Reason: "daemon_unavailable", ObservedAt: now.Add(-5 * time.Minute)},
	} {
		if err := evidence.Visit(envelope); err != nil {
			t.Fatal(err)
		}
	}
	if count, latest := evidence.DeliveryFailures(now); count != 1 || !latest.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("delivery failures = %d latest %s, want only the daemon_unavailable drop", count, latest)
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

	workHome, personalHome := t.TempDir(), t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{
		{Name: "work", Harness: harness.Claude, HomeDir: workHome},
		{Name: "personal", Harness: harness.Claude, HomeDir: personalHome},
	}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	base := time.Now().UTC().Add(-time.Minute)
	for sequence, envelope := range []diagnostics.TraceEnvelope{
		{Stage: "hook.received", Harness: " CLAUDE ", Evidence: map[string]string{"config_dir": workHome}, ObservedAt: base},
		{Stage: "daemon.applied", Harness: "claude", Evidence: map[string]string{"config_dir": workHome}, ObservedAt: base.Add(45 * time.Second)},
		{Stage: "hook.completed", Harness: "claude", Evidence: map[string]string{"config_dir": workHome}, ObservedAt: base.Add(30 * time.Second)},
		{Stage: "hook.received", Harness: "Claude", Evidence: map[string]string{"config_dir": personalHome}, ObservedAt: base.Add(15 * time.Second)},
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

func TestStatusAllSharedHomeProfilesShareProducerEvidence(t *testing.T) {
	stubActivationRuntimeWithoutEvidence(t, map[string]string{"claude": "2.1.195"})
	sharedHome := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{
		{Name: "personal-claude", Harness: harness.Claude, HomeDir: sharedHome},
		{Name: "vta-claude", Harness: harness.Claude, HomeDir: sharedHome},
		{Name: "isolated-claude", Harness: harness.Claude, HomeDir: t.TempDir()},
	}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	seen := time.Now().UTC().Add(-time.Minute)
	evidence := NewStatusEvidence()
	// A non-hook record deeper than the 4h verification window makes the
	// journal horizon cover the window, so absent evidence means "missing"
	// rather than "possibly pruned".
	if err := evidence.Visit(diagnostics.TraceEnvelope{
		Stage: "daemon.decision", Harness: harness.Claude, ObservedAt: seen.Add(-5 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// One delivery record whose routing stamped a codex profile: the producer
	// home still credits every claude profile launching from that home.
	if err := evidence.Visit(diagnostics.TraceEnvelope{
		Stage: "hook.delivery", Harness: harness.Claude, Profile: "personal-codex",
		Evidence: map[string]string{"config_dir": sharedHome}, ObservedAt: seen,
	}); err != nil {
		t.Fatal(err)
	}

	statuses, err := StatusAllWithEvidence(cfg, testBin, evidence, nil)
	if err != nil {
		t.Fatalf("StatusAllWithEvidence: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("statuses = %d, want 3", len(statuses))
	}
	for _, status := range statuses[:2] {
		if !status.LastSeen.Equal(seen) || hasActivationIssue(status, "last_seen_missing") {
			t.Errorf("%s = last seen %s, issues %+v; want shared-home evidence %s", status.Account, status.LastSeen, status.ActivationIssues, seen)
		}
	}
	if !statuses[2].LastSeen.IsZero() || !hasActivationIssue(statuses[2], "last_seen_missing") {
		t.Errorf("isolated-claude = last seen %s, issues %+v; want last_seen_missing", statuses[2].LastSeen, statuses[2].ActivationIssues)
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
