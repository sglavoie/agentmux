package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// SyncStatus describes whether an account's hooks are in sync with the config.
type SyncStatus string

const (
	// SyncInSync means the hooks file has the expected amx hooks installed.
	SyncInSync SyncStatus = "in_sync"
	// SyncDrifted means amx hooks exist but differ from what amx would write.
	SyncDrifted SyncStatus = "drifted"
	// SyncMissing means the hooks file is absent or has no amx hooks.
	SyncMissing SyncStatus = "missing"
)

// ActivationVerdict describes whether installed hooks can actually run for a
// profile. It is intentionally separate from file synchronization: a byte-for-
// byte current file can still be disabled by settings, launch flags, trust, or
// managed policy.
type ActivationVerdict string

const (
	ActivationHealthy    ActivationVerdict = "healthy"
	ActivationFailure    ActivationVerdict = "failure"
	ActivationUnverified ActivationVerdict = "unverified"
)

// ActivationIssue is one piece of evidence contributing to a profile's
// activation verdict. Failure means verified breakage; unverified means the
// available local evidence cannot prove the hooks will execute.
type ActivationIssue struct {
	Verdict ActivationVerdict
	Code    string
	Message string
	Path    string
}

// AccountStatus holds the sync status for one account. Err is non-nil when
// the per-account check itself failed (e.g. permission denied reading the
// settings file); Status is undefined in that case.
type AccountStatus struct {
	Account            string
	Harness            string
	Path               string
	Status             SyncStatus
	Activation         ActivationVerdict
	ActivationIssues   []ActivationIssue
	CLIVersion         string
	VerifiedCLIVersion string
	LastSeen           time.Time
	Err                error
}

// ActivationSummary returns a compact rendering of the evidence that prevents
// a healthy verdict.
func (s AccountStatus) ActivationSummary() string {
	if len(s.ActivationIssues) == 0 {
		return "active and recently observed"
	}
	parts := make([]string, 0, len(s.ActivationIssues))
	for _, issue := range s.ActivationIssues {
		parts = append(parts, issue.Message)
	}
	return strings.Join(parts, "; ")
}

// HookHealthSummary aggregates hook sync status across all profiles,
// bucketing profile names by their current state. Each slice holds the
// profile names whose hooks are in the matching state; absent profiles
// are not represented.
type HookHealthSummary struct {
	InSync  []string
	Drifted []string
	Missing []string
}

// Healthy reports whether every profile is in sync (i.e. no drift or
// missing hooks). An empty summary (no profiles) is considered healthy.
func (s HookHealthSummary) Healthy() bool {
	return len(s.Drifted) == 0 && len(s.Missing) == 0
}

// OverallHookHealth returns the aggregated hook sync status across every
// profile in the config. amxBin is the absolute path to the amx binary.
// When StatusAll reports per-account failures, the corresponding accounts
// are omitted from the buckets and the joined error is returned alongside
// the partial summary so callers can decide whether to degrade or surface.
func OverallHookHealth(cfg *config.Config, amxBin string) (HookHealthSummary, error) {
	statuses, err := StatusAll(cfg, amxBin)
	var summary HookHealthSummary
	for _, s := range statuses {
		if s.Err != nil {
			continue
		}
		switch s.Status {
		case SyncInSync:
			summary.InSync = append(summary.InSync, s.Account)
		case SyncDrifted:
			summary.Drifted = append(summary.Drifted, s.Account)
		case SyncMissing:
			summary.Missing = append(summary.Missing, s.Account)
		}
	}
	return summary, err
}

// StatusAll returns hook-file synchronization and activation evidence for every
// configured profile. amxBin is the absolute path to the amx binary (used to
// determine expected commands).
//
// Per-account failures do not short-circuit the loop: the failing entry is
// returned with Err populated, and the joined error covers every account
// that errored. This lets the hooks-status command and the TUI health pip
// surface every reachable profile even when one account's settings file
// is unreadable.
func StatusAll(cfg *config.Config, amxBin string) ([]AccountStatus, error) {
	evidence, evidenceErr := activationEvidenceProbe(context.Background(), cfg)
	return StatusAllWithEvidence(cfg, amxBin, evidence, evidenceErr)
}

// StatusAllWithEvidence evaluates every configured profile using hook records
// gathered by a caller-owned journal traversal. evidenceErr is shared by all
// profiles so an unavailable journal does not trigger per-account retries.
func StatusAllWithEvidence(cfg *config.Config, amxBin string, evidence *StatusEvidence, evidenceErr error) ([]AccountStatus, error) {
	if cfg == nil {
		return nil, errors.New("hooks: configuration is unavailable")
	}
	results := make([]AccountStatus, 0, len(cfg.Accounts))
	var errs []error
	evaluator := newActivationEvaluator(cfg, evidence, evidenceErr)
	for _, account := range cfg.Accounts {
		override := accountOverride(cfg, account.Name)
		status, err := checkStatus(account, override, amxBin)
		if err != nil {
			harnessName := account.EffectiveHarness()
			if harnessName == "" {
				harnessName = "claude"
			}
			if status.Account == "" {
				status.Account = account.Name
			}
			if status.Harness == "" {
				status.Harness = harnessName
			}
			status.Err = err
			errs = append(errs, fmt.Errorf("%s: %w", account.Name, err))
		}
		evaluator.evaluate(account, override, &status)
		results = append(results, status)
	}
	return results, errors.Join(errs...)
}

func checkStatus(account config.Account, override config.AccountHookOverride, amxBin string) (AccountStatus, error) {
	h := harness.Lookup(account.EffectiveHarness())
	contract := h.HookContract()
	homeDir := config.ExpandTilde(account.EffectiveHomeDir())
	path := contract.InstallPath(homeDir)

	harnessName := account.EffectiveHarness()
	if harnessName == "" {
		harnessName = "claude"
	}

	existingData, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncMissing}, nil
		}
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path}, err
	}

	installed, err := contract.ParseInstalledEvents(path)
	if err != nil {
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path}, err
	}

	newData, err := contract.MergeData(existingData, override.DisabledEvents, amxBin)
	if err != nil {
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path}, err
	}
	if bytes.Equal(existingData, newData) {
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncInSync}, nil
	}
	if len(installed) == 0 {
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncMissing}, nil
	}
	return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncDrifted}, nil
}
