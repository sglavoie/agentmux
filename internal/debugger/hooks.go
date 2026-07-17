package debugger

import (
	"context"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/hooks"
)

// Verdict is the shared health decision used by status, doctor, and
// hooks-status. Consumers render it differently but do not recompute it.
type Verdict string

const (
	VerdictHealthy     Verdict = "healthy"
	VerdictWarning     Verdict = "warning"
	VerdictFailure     Verdict = "failure"
	VerdictUnavailable Verdict = "unavailable"
)

// Finding is one stable, machine-readable status conclusion.
type Finding struct {
	Verdict Verdict `json:"verdict"`
	Check   string  `json:"check"`
	Message string  `json:"message"`
}

type HookIssue struct {
	Verdict hooks.ActivationVerdict `json:"verdict"`
	Code    string                  `json:"code"`
	Message string                  `json:"message"`
	Path    string                  `json:"path,omitempty"`
}

// HookProfile is contract, activation/trust, version, and silence evidence for
// one configured profile.
type HookProfile struct {
	Profile            string                  `json:"profile"`
	Harness            string                  `json:"harness"`
	Path               string                  `json:"path,omitempty"`
	Sync               hooks.SyncStatus        `json:"sync,omitempty"`
	Activation         hooks.ActivationVerdict `json:"activation"`
	CLIVersion         string                  `json:"cli_version,omitempty"`
	VerifiedCLIVersion string                  `json:"verified_cli_version,omitempty"`
	LastSeen           *time.Time              `json:"last_seen,omitempty"`
	LastSeenAgeSeconds int64                   `json:"last_seen_age_seconds,omitempty"`
	HookSilenceSeconds int64                   `json:"hook_silence_seconds"`
	Silent             bool                    `json:"silent"`
	Issues             []HookIssue             `json:"issues,omitempty"`
	CheckError         string                  `json:"check_error,omitempty"`
	Verdict            Verdict                 `json:"verdict"`
}

func (p HookProfile) Detail() string {
	if p.CheckError != "" {
		return p.CheckError
	}
	if len(p.Issues) == 0 {
		return p.Path
	}
	parts := make([]string, 0, len(p.Issues))
	for _, issue := range p.Issues {
		parts = append(parts, issue.Message)
	}
	return strings.Join(parts, "; ")
}

// HookDeliveryFailures summarizes terminally dropped hook deliveries in the
// trailing hour — one count per request whose terminal record reports failure,
// not per intermediate transport attempt. They are warning-level and
// time-bounded: the hook client stays prompt-free, so a delivery that no retry
// resolves within a daemon restart is lost by design.
type HookDeliveryFailures struct {
	Count    int        `json:"count"`
	LatestAt *time.Time `json:"latest_at,omitempty"`
}

type HookReport struct {
	Verdict          Verdict               `json:"verdict"`
	Profiles         []HookProfile         `json:"profiles"`
	DeliveryFailures *HookDeliveryFailures `json:"delivery_failures,omitempty"`
	Error            string                `json:"error,omitempty"`
}

// InspectHooks is the single verdict engine for hook contracts, activation,
// trust, CLI compatibility, last-seen evidence, and hook silence. It probes
// the journal once and shares the evidence with the per-profile evaluation,
// mirroring hooks.StatusAll.
func InspectHooks(cfg *config.Config, amxBin string, now time.Time) HookReport {
	if cfg == nil {
		return unavailableHookReport()
	}
	evidence, evidenceErr := hooks.ProbeStatusEvidence(context.Background(), cfg)
	return InspectHooksWithEvidence(cfg, amxBin, now, evidence, evidenceErr)
}

// InspectHooksWithEvidence evaluates hooks using evidence gathered by another
// journal consumer, such as CollectStatus's displayed-record snapshot.
func InspectHooksWithEvidence(cfg *config.Config, amxBin string, now time.Time, evidence *hooks.StatusEvidence, evidenceErr error) HookReport {
	if cfg == nil {
		return unavailableHookReport()
	}
	statuses, err := hooks.StatusAllWithEvidence(cfg, amxBin, evidence, evidenceErr)
	report := hookReportFromStatuses(cfg, now, statuses, err)
	if evidence != nil && evidenceErr == nil {
		count, latest := evidence.DeliveryFailures(now)
		failures := &HookDeliveryFailures{Count: count}
		if !latest.IsZero() {
			latestAt := latest.UTC()
			failures.LatestAt = &latestAt
		}
		report.DeliveryFailures = failures
	}
	return report
}

func unavailableHookReport() HookReport {
	report := HookReport{Verdict: VerdictHealthy, Profiles: make([]HookProfile, 0)}
	report.Verdict = VerdictUnavailable
	report.Error = "configuration is unavailable"
	return report
}

func hookReportFromStatuses(cfg *config.Config, now time.Time, statuses []hooks.AccountStatus, err error) HookReport {
	report := HookReport{Verdict: VerdictHealthy, Profiles: make([]HookProfile, 0)}
	if err != nil {
		report.Error = err.Error()
	}
	for _, status := range statuses {
		profile := HookProfile{
			Profile: status.Account, Harness: status.Harness, Path: status.Path,
			Sync: status.Status, Activation: status.Activation,
			CLIVersion: status.CLIVersion, VerifiedCLIVersion: status.VerifiedCLIVersion,
			HookSilenceSeconds: hookSilenceSeconds(cfg, status.Account), Verdict: VerdictHealthy,
		}
		if !status.LastSeen.IsZero() {
			lastSeen := status.LastSeen.UTC()
			profile.LastSeen = &lastSeen
			age := now.UTC().Sub(lastSeen)
			if age < 0 {
				age = 0
			}
			profile.LastSeenAgeSeconds = int64(age / time.Second)
			profile.Silent = profile.HookSilenceSeconds > 0 && profile.LastSeenAgeSeconds > profile.HookSilenceSeconds
		}
		for _, issue := range status.ActivationIssues {
			profile.Issues = append(profile.Issues, HookIssue{
				Verdict: issue.Verdict, Code: issue.Code, Message: issue.Message, Path: issue.Path,
			})
		}
		switch {
		case status.Err != nil:
			profile.CheckError = status.Err.Error()
			profile.Verdict = VerdictFailure
		case status.Status != hooks.SyncInSync || status.Activation == hooks.ActivationFailure:
			profile.Verdict = VerdictFailure
		case status.Activation == hooks.ActivationUnverified || profile.Silent:
			profile.Verdict = VerdictWarning
		}
		report.Verdict = worseVerdict(report.Verdict, profile.Verdict)
		report.Profiles = append(report.Profiles, profile)
	}
	if err != nil && len(statuses) == 0 {
		report.Verdict = VerdictFailure
	}
	return report
}

func hookSilenceSeconds(cfg *config.Config, profile string) int64 {
	seconds := 0
	for _, account := range cfg.Accounts {
		if account.Name == profile {
			seconds = account.Watchdog.HookSilence
			break
		}
	}
	if seconds <= 0 {
		seconds = cfg.Defaults.Watchdog.HookSilence
	}
	if seconds <= 0 {
		seconds = int((4 * time.Hour) / time.Second)
	}
	return int64(seconds)
}

func worseVerdict(left, right Verdict) Verdict {
	rank := map[Verdict]int{VerdictHealthy: 0, VerdictWarning: 1, VerdictUnavailable: 2, VerdictFailure: 3}
	if rank[right] > rank[left] {
		return right
	}
	return left
}
