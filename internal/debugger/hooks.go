package debugger

import (
	"context"
	"fmt"
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
	ContractAhead      bool                    `json:"contract_ahead,omitempty"`
	LastSeen           *time.Time              `json:"last_seen,omitempty"`
	LastSeenAgeSeconds int64                   `json:"last_seen_age_seconds,omitempty"`
	HookSilenceSeconds int64                   `json:"hook_silence_seconds"`
	Silent             bool                    `json:"silent"`
	Issues             []HookIssue             `json:"issues,omitempty"`
	CheckError         string                  `json:"check_error,omitempty"`
	// Unused marks an installed profile that nothing has exercised: no
	// configured project launches it and no hook event from its home dir has
	// ever been observed. Its activation stays unverified, but there is nothing
	// to verify yet, so the verdict is healthy and surfaces report it as a note.
	Unused  bool    `json:"unused,omitempty"`
	Verdict Verdict `json:"verdict"`
}

func (p HookProfile) Detail() string {
	if p.CheckError != "" {
		return p.CheckError
	}
	if p.Unused {
		return UnusedHookProfileDetail
	}
	if len(p.Issues) == 0 {
		return p.Path
	}
	// One untrusted Codex install produces an issue per project per hook event,
	// which ran to hundreds of clauses on a single doctor line and buried every
	// other finding. A code that repeats past hookDetailRepeatLimit is shown by
	// its first messages plus a count; Issues keeps every one for JSON.
	byCode := make(map[string]int, len(p.Issues))
	for _, issue := range p.Issues {
		byCode[issue.Code]++
	}
	shown := make(map[string]int, len(byCode))
	parts := make([]string, 0, len(p.Issues))
	for _, issue := range p.Issues {
		shown[issue.Code]++
		switch n := shown[issue.Code]; {
		case n <= hookDetailRepeatLimit:
			parts = append(parts, issue.Message)
		case n == hookDetailRepeatLimit+1:
			parts = append(parts, fmt.Sprintf("and %d more like these", byCode[issue.Code]-hookDetailRepeatLimit))
		}
	}
	return strings.Join(parts, "; ")
}

// UnusedHookProfileDetail is the sentence every surface prints for an Unused
// profile in place of its evidence gaps.
const UnusedHookProfileDetail = "not in use: no configured project launches this profile and no hook event from it has been observed"

// hookDetailRepeatLimit is how many issues sharing one code Detail spells out
// before summarizing the rest as a count.
const hookDetailRepeatLimit = 3

// HookDeliveryFailures summarizes terminally dropped hook deliveries in the
// trailing hour — one count per request whose terminal record reports failure,
// not per intermediate transport attempt. They are warning-level and
// time-bounded: the hook client stays prompt-free, so a delivery that no retry
// resolves within a daemon restart is lost by design.
type HookDeliveryFailures struct {
	Count    int        `json:"count"`
	LatestAt *time.Time `json:"latest_at,omitempty"`
}

// PredateDaemonStart reports whether every counted failure happened before the
// running daemon started — the restart gap the hook client drops by design, and
// already over. A zero startedAt (no daemon answered) never qualifies, and
// neither does a count without a latest time, which cannot be placed.
func (f *HookDeliveryFailures) PredateDaemonStart(startedAt time.Time) bool {
	return f != nil && f.LatestAt != nil && !startedAt.IsZero() && !f.LatestAt.After(startedAt)
}

// Summary is the one sentence doctor and status both print about the failures.
// It places them in time against the running daemon, because "2 failures in
// the last hour" reads the same whether the daemon has been flapping all along
// or was restarted once a minute ago, and only the first needs attention.
func (f *HookDeliveryFailures) Summary(now, daemonStartedAt time.Time) string {
	if f == nil || f.Count == 0 {
		return ""
	}
	noun := "deliveries"
	if f.Count == 1 {
		noun = "delivery"
	}
	if f.PredateDaemonStart(daemonStartedAt) {
		return fmt.Sprintf("%d hook %s dropped before the daemon started %s; none since", f.Count, noun, deliveryAge(now.Sub(daemonStartedAt)))
	}
	message := fmt.Sprintf("%d hook %s failed in the trailing hour", f.Count, noun)
	if f.LatestAt != nil {
		message += fmt.Sprintf(", the latest %s", deliveryAge(now.Sub(*f.LatestAt)))
	}
	if daemonStartedAt.IsZero() {
		return message + "; deliveries that cannot reach a daemon are dropped"
	}
	return message + ", while the daemon was running"
}

// deliveryAge renders an elapsed time coarsely, as "just now" or "Nm ago".
func deliveryAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
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
			CLIVersion: status.CLIVersion, VerifiedCLIVersion: status.VerifiedCLIVersion, ContractAhead: status.ContractAhead,
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
		case status.Activation == hooks.ActivationUnverified && unusedProfile(cfg, status):
			profile.Unused = true
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

// unusedProfile reports whether an in-sync profile's only activation gaps are
// the ones an idle profile always has — no hook evidence yet, and a CLI newer
// than the contract that no event has confirmed — while no configured project
// launches it. A real activation problem, any hook event ever seen, or a
// project that names the profile keeps the warning, because then the operator
// is relying on these hooks.
func unusedProfile(cfg *config.Config, status hooks.AccountStatus) bool {
	if !status.LastSeen.IsZero() {
		return false
	}
	for _, issue := range status.ActivationIssues {
		switch issue.Code {
		case "last_seen_missing", "last_seen_horizon", "contract_stale":
		default:
			return false
		}
	}
	for _, project := range cfg.Projects {
		if project.EffectiveProfile() == status.Account {
			return false
		}
	}
	return true
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
