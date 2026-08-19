package hooks

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

const defaultLastSeenWindow = 4 * time.Hour

var (
	activationVersionProbe      = probeHarnessVersion
	activationClaudeManagedRoot = claudeManagedSettingsRoot
)

type versionEvidence struct {
	version string
	err     error
}

type activationEvaluator struct {
	cfg         *config.Config
	now         time.Time
	versions    map[string]versionEvidence
	evidence    *StatusEvidence
	evidenceErr error
}

type activationContext struct {
	name string
	cwd  string
	args []string
}

func newActivationEvaluator(cfg *config.Config, evidence *StatusEvidence, evidenceErr error) *activationEvaluator {
	return &activationEvaluator{
		cfg:         cfg,
		now:         time.Now().UTC(),
		versions:    make(map[string]versionEvidence),
		evidence:    evidence,
		evidenceErr: evidenceErr,
	}
}

func (e *activationEvaluator) evaluate(account config.Account, override config.AccountHookOverride, status *AccountStatus) {
	if status == nil {
		return
	}
	status.Activation = ActivationHealthy
	if status.Err != nil {
		addActivationIssue(status, ActivationFailure, "status_error", status.Err.Error(), status.Path)
		return
	}
	if status.Status != SyncInSync {
		addActivationIssue(status, ActivationFailure, "hook_file_"+string(status.Status), "installed hook file is "+string(status.Status), status.Path)
	}
	if len(override.DisabledEvents) > 0 {
		addActivationIssue(status, ActivationFailure, "events_disabled", "Agent Mux events disabled by profile: "+strings.Join(override.DisabledEvents, ", "), "")
	}

	verified := verifiedCLIVersion(account.EffectiveHarness())
	status.VerifiedCLIVersion = verified
	version := e.versionFor(account.EffectiveHarness())
	if version.err != nil {
		addActivationIssue(status, ActivationFailure, "cli_unavailable", version.err.Error(), "")
	} else {
		status.CLIVersion = version.version
		switch comparison, ok := compareVersions(version.version, verified); {
		case !ok:
			addActivationIssue(status, ActivationUnverified, "cli_version_unparsed", fmt.Sprintf("cannot compare CLI version %q with verified contract %q", version.version, verified), "")
		case comparison > 0:
			addActivationIssue(status, ActivationUnverified, "contract_stale", fmt.Sprintf("CLI %s is newer than verified hook contract %s", version.version, verified), "")
		case comparison < 0:
			addActivationIssue(status, ActivationUnverified, "cli_older_than_contract", fmt.Sprintf("CLI %s is older than verified hook contract %s", version.version, verified), "")
		}
	}

	contexts := activationContexts(e.cfg, account)
	switch account.EffectiveHarness() {
	case harness.Codex:
		e.inspectCodex(account, override, contexts, status)
	case harness.Claude:
		e.inspectClaude(account, contexts, status)
	case harness.Copilot:
		e.inspectCopilot(account, contexts, status)
	}

	if e.evidenceErr != nil {
		addActivationIssue(status, ActivationUnverified, "last_seen_unavailable", "recent hook evidence unavailable: "+e.evidenceErr.Error(), "")
		return
	}
	evidenceHome := config.ExpandTilde(account.EffectiveHomeDir())
	lastSeen := e.evidence.latestForHome(account.EffectiveHarness(), evidenceHome)
	if lastSeen.IsZero() {
		window := lastSeenWindow(e.cfg, account)
		// A journal whose oldest record is younger than the verification window
		// cannot prove silence: older hook evidence may simply have been pruned.
		// Only an empty journal or one that covers the window justifies the
		// stronger "missing" claim.
		if horizon, journalNonEmpty := e.evidence.Horizon(e.now); journalNonEmpty && horizon < window {
			addActivationIssue(status, ActivationUnverified, "last_seen_horizon", fmt.Sprintf(
				"journal horizon (%s) is shorter than the hook verification window (%s); older hook evidence for home dir %s may have been pruned",
				formatActivationWindow(horizon), formatActivationWindow(window), evidenceHome), "")
		} else {
			addActivationIssue(status, ActivationUnverified, "last_seen_missing", fmt.Sprintf("no hook event has been observed in the diagnostic journal for home dir %s", evidenceHome), "")
		}
	} else {
		status.LastSeen = lastSeen
		if age := e.now.Sub(lastSeen); age > lastSeenWindow(e.cfg, account) {
			addActivationIssue(status, ActivationUnverified, "last_seen_stale", fmt.Sprintf("last hook evidence for home dir %s is stale (%s ago)", evidenceHome, age.Round(time.Second)), "")
		}
	}
}

func (e *activationEvaluator) versionFor(harnessName string) versionEvidence {
	harnessName = harness.Normalize(harnessName)
	if evidence, ok := e.versions[harnessName]; ok {
		return evidence
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	version, err := activationVersionProbe(ctx, harness.Lookup(harnessName).Executable())
	evidence := versionEvidence{version: version, err: err}
	e.versions[harnessName] = evidence
	return evidence
}

func addActivationIssue(status *AccountStatus, verdict ActivationVerdict, code, message, path string) {
	for _, issue := range status.ActivationIssues {
		if issue.Verdict == verdict && issue.Code == code && issue.Message == message && issue.Path == path {
			return
		}
	}
	status.ActivationIssues = append(status.ActivationIssues, ActivationIssue{
		Verdict: verdict,
		Code:    code,
		Message: message,
		Path:    path,
	})
	if verdict == ActivationFailure || status.Activation == ActivationHealthy {
		status.Activation = verdict
	}
}

func verifiedCLIVersion(harnessName string) string {
	matrix := harness.HookEventMatrix(harnessName)
	if len(matrix) == 0 {
		return ""
	}
	return matrix[0].VerifiedCLIVersion
}

func activationContexts(cfg *config.Config, account config.Account) []activationContext {
	var contexts []activationContext
	if cfg != nil {
		for _, project := range cfg.Projects {
			if !strings.EqualFold(project.EffectiveProfile(), account.Name) {
				continue
			}
			contexts = append(contexts, activationContext{
				name: project.Title,
				cwd:  config.ExpandTilde(project.Directory),
				args: effectiveLaunchArgs(cfg.Defaults, account, project),
			})
		}
	}
	if len(contexts) == 0 {
		var defaults config.Defaults
		if cfg != nil {
			defaults = cfg.Defaults
		}
		contexts = append(contexts, activationContext{name: account.Name, args: effectiveLaunchArgs(defaults, account, config.Session{})})
	}
	return contexts
}

func effectiveLaunchArgs(defaults config.Defaults, account config.Account, project config.Session) []string {
	if args := project.EffectiveArgs(); len(args) > 0 {
		return append([]string(nil), args...)
	}
	if len(account.Args) > 0 {
		return append([]string(nil), account.Args...)
	}
	return append([]string(nil), defaults.EffectiveArgs()...)
}

func lastSeenWindow(cfg *config.Config, account config.Account) time.Duration {
	seconds := account.Watchdog.HookSilence
	if seconds <= 0 && cfg != nil {
		seconds = cfg.Defaults.Watchdog.HookSilence
	}
	if seconds <= 0 {
		return defaultLastSeenWindow
	}
	return time.Duration(seconds) * time.Second
}

// formatActivationWindow trims whole hours/minutes ("4h", "12m") so horizon
// messages stay readable; uneven durations fall back to Duration.String.
func formatActivationWindow(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return d.String()
	}
}

func probeHarnessVersion(ctx context.Context, executable string) (string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", fmt.Errorf("%s CLI is not on PATH", executable)
	}
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%s --version timed out", executable)
		}
		return "", fmt.Errorf("%s --version failed: %w", executable, err)
	}
	version := extractVersion(string(out))
	if version == "" {
		return "", fmt.Errorf("%s --version returned no numeric version", executable)
	}
	return version, nil
}

func extractVersion(output string) string {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, "vV.,;()[]")
		parts := strings.Split(candidate, ".")
		if len(parts) < 2 {
			continue
		}
		valid := true
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return candidate
		}
	}
	return ""
}

func compareVersions(left, right string) (int, bool) {
	if left == "" || right == "" {
		return 0, false
	}
	lparts := strings.Split(left, ".")
	rparts := strings.Split(right, ".")
	count := len(lparts)
	if len(rparts) > count {
		count = len(rparts)
	}
	for i := 0; i < count; i++ {
		var lvalue, rvalue int
		var err error
		if i < len(lparts) {
			lvalue, err = strconv.Atoi(lparts[i])
			if err != nil {
				return 0, false
			}
		}
		if i < len(rparts) {
			rvalue, err = strconv.Atoi(rparts[i])
			if err != nil {
				return 0, false
			}
		}
		if lvalue < rvalue {
			return -1, true
		}
		if lvalue > rvalue {
			return 1, true
		}
	}
	return 0, true
}

// Codex activation ----------------------------------------------------------
