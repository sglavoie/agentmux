package debugger

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/hooks"
)

// A profile nothing has exercised has nothing to verify, so it reports healthy
// and Unused instead of keeping every health surface on a permanent warning.
// Anything that shows the hooks are relied on — a project that launches the
// profile, a hook event ever seen, or an activation gap other than missing
// evidence — keeps the warning.
func TestHookReportMarksAProfileNothingUsesAsUnused(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	gaps := []hooks.ActivationIssue{
		{Verdict: hooks.ActivationUnverified, Code: "last_seen_missing", Message: "no hook event"},
		{Verdict: hooks.ActivationUnverified, Code: "contract_stale", Message: "CLI is newer"},
	}
	idle := func() hooks.AccountStatus {
		return hooks.AccountStatus{Account: "copilot", Harness: "copilot", Status: hooks.SyncInSync,
			Activation: hooks.ActivationUnverified, ActivationIssues: append([]hooks.ActivationIssue(nil), gaps...)}
	}
	withProject := &config.Config{Projects: []config.Session{{Title: "x", Profile: "copilot"}}}
	withLegacyProject := &config.Config{Projects: []config.Session{{Title: "x", Account: "copilot"}}}
	seen := idle()
	seen.LastSeen = now.Add(-48 * time.Hour)
	untrusted := idle()
	untrusted.ActivationIssues = append(untrusted.ActivationIssues, hooks.ActivationIssue{Verdict: hooks.ActivationUnverified, Code: "codex_hook_untrusted"})
	drifted := idle()
	drifted.Status = hooks.SyncDrifted

	for _, tc := range []struct {
		name    string
		cfg     *config.Config
		status  hooks.AccountStatus
		unused  bool
		verdict Verdict
	}{
		{"no project and no evidence", &config.Config{}, idle(), true, VerdictHealthy},
		{"a project launches it", withProject, idle(), false, VerdictWarning},
		{"a legacy project launches it", withLegacyProject, idle(), false, VerdictWarning},
		{"a hook event was once seen", &config.Config{}, seen, false, VerdictWarning},
		{"another activation gap", &config.Config{}, untrusted, false, VerdictWarning},
		{"drifted", &config.Config{}, drifted, false, VerdictFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := hookReportFromStatuses(tc.cfg, now, []hooks.AccountStatus{tc.status}, nil)
			profile := report.Profiles[0]
			if profile.Unused != tc.unused || profile.Verdict != tc.verdict || report.Verdict != tc.verdict {
				t.Fatalf("unused=%t verdict=%s report=%s, want unused=%t verdict=%s", profile.Unused, profile.Verdict, report.Verdict, tc.unused, tc.verdict)
			}
			if tc.unused {
				if got := profile.Detail(); got != UnusedHookProfileDetail {
					t.Fatalf("Detail = %q, want %q", got, UnusedHookProfileDetail)
				}
				if len(profile.Issues) != len(gaps) {
					t.Fatalf("issues = %v, want the evidence gaps kept for JSON", profile.Issues)
				}
			}
		})
	}
}
