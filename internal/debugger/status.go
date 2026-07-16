package debugger

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const StatusSchemaVersion = 1

type DaemonHealthClient interface {
	Health() daemon.Health
}

type KittyWindowLister interface {
	ListWindows() ([]kitty.OSWindow, error)
}

type StatusOptions struct {
	Config      *config.Config
	ConfigError error
	Paths       diagnostics.Paths
	PathsError  error
	Journal     *diagnostics.Journal
	Daemon      DaemonHealthClient
	Kitty       KittyWindowLister
	AMXBin      string
	CLIBuild    buildinfo.Info
	Now         time.Time
	Selector    Selector
}

type DaemonReport struct {
	Verdict        Verdict         `json:"verdict"`
	Alive          bool            `json:"alive"`
	Ready          bool            `json:"ready"`
	DegradedReason string          `json:"degraded_reason,omitempty"`
	PID            int             `json:"pid,omitempty"`
	UptimeSeconds  int64           `json:"uptime_seconds,omitempty"`
	Build          *buildinfo.Info `json:"build,omitempty"`
	CLIBuild       buildinfo.Info  `json:"cli_build"`
	BuildMatches   *bool           `json:"build_matches,omitempty"`
}

type JournalReport struct {
	Verdict  Verdict                   `json:"verdict"`
	Enabled  bool                      `json:"enabled"`
	Path     string                    `json:"path,omitempty"`
	Health   diagnostics.JournalHealth `json:"health"`
	MaxBytes int64                     `json:"max_bytes"`
	Error    string                    `json:"error,omitempty"`
}

type StatusReport struct {
	SchemaVersion  int                         `json:"schema_version"`
	GeneratedAt    time.Time                   `json:"generated_at"`
	Selector       Selector                    `json:"selector"`
	Verdict        Verdict                     `json:"verdict"`
	Healthy        bool                        `json:"healthy"`
	Daemon         DaemonReport                `json:"daemon"`
	Hooks          HookReport                  `json:"hooks"`
	Journal        JournalReport               `json:"journal"`
	Native         NativeReport                `json:"native"`
	LastSeenEvents []diagnostics.TraceEnvelope `json:"last_seen_events"`
	Findings       []Finding                   `json:"findings"`
}

// CollectStatus assembles offline evidence first and treats daemon/Kitty
// availability as report data rather than a prerequisite for useful output.
func CollectStatus(ctx context.Context, options StatusOptions) (StatusReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	if err := options.Selector.Normalize(); err != nil {
		return StatusReport{}, err
	}
	if options.CLIBuild.Version == "" {
		options.CLIBuild = buildinfo.Current()
	}
	report := StatusReport{
		SchemaVersion: StatusSchemaVersion, GeneratedAt: options.Now, Selector: options.Selector,
		Verdict: VerdictHealthy, Findings: make([]Finding, 0), LastSeenEvents: make([]diagnostics.TraceEnvelope, 0),
	}

	if options.Daemon == nil {
		options.Daemon = daemon.NewClient()
	}
	health := options.Daemon.Health()
	report.Daemon = DaemonReport{
		Verdict: VerdictHealthy, Alive: health.Alive, Ready: health.Ready,
		DegradedReason: health.DegradedReason, PID: health.PID,
		UptimeSeconds: int64(health.Uptime / time.Second), Build: health.Build, CLIBuild: options.CLIBuild,
	}
	if !health.Alive {
		report.Daemon.Verdict = VerdictWarning
		report.add(VerdictWarning, "daemon_unavailable", "daemon is unavailable; persisted diagnostics remain queryable")
	} else {
		if !health.Ready {
			report.Daemon.Verdict = VerdictWarning
			report.add(VerdictWarning, "daemon_degraded", "daemon is not ready: "+health.DegradedReason)
		}
		if health.Build != nil {
			matches := options.CLIBuild.SameArtifact(*health.Build)
			report.Daemon.BuildMatches = &matches
			if !matches {
				report.Daemon.Verdict = VerdictFailure
				report.add(VerdictFailure, "daemon_build_mismatch", "daemon and CLI are different build artifacts")
			}
		}
	}

	if options.ConfigError != nil {
		report.Hooks = HookReport{Verdict: VerdictUnavailable, Profiles: []HookProfile{}, Error: options.ConfigError.Error()}
		report.add(VerdictUnavailable, "config_unavailable", options.ConfigError.Error())
	} else {
		if options.AMXBin == "" {
			options.AMXBin, _ = os.Executable()
		}
		report.Hooks = InspectHooks(options.Config, options.AMXBin, options.Now)
		filterHookProfiles(&report.Hooks, options.Selector)
		for _, profile := range report.Hooks.Profiles {
			report.addHookProfileFinding(profile)
		}
	}

	paths := options.Paths
	if options.PathsError != nil {
		report.Journal = JournalReport{Verdict: VerdictUnavailable, Error: options.PathsError.Error()}
		report.Native = NativeReport{Verdict: VerdictUnavailable, Sessions: []NativeSession{}, Error: options.PathsError.Error()}
		report.add(VerdictUnavailable, "diagnostic_paths", options.PathsError.Error())
	} else {
		policy := config.DiagnosticsConfig{}
		if options.Config != nil {
			policy = options.Config.Defaults.Diagnostics
		}
		report.Journal = inspectJournal(ctx, paths, policy, options.Journal, options.Selector, &report)

		var windows []kitty.OSWindow
		kittyAvailable := false
		if options.Kitty != nil {
			var err error
			windows, err = options.Kitty.ListWindows()
			if err == nil {
				kittyAvailable = true
			} else {
				report.add(VerdictWarning, "kitty_unavailable", err.Error())
			}
		}
		report.Native = InspectNative(paths, policy, windows, kittyAvailable, options.Selector, options.Now)
		if report.Native.Error != "" {
			report.add(report.Native.Verdict, "native_storage", report.Native.Error)
		}
		if report.Native.Coverage.Degraded > 0 {
			report.add(VerdictWarning, "native_degraded", fmt.Sprintf("%d session(s) have degraded native-log coverage", report.Native.Coverage.Degraded))
		}
		if report.Native.Coverage.Unmanaged > 0 {
			report.add(VerdictWarning, "unmanaged_sessions", fmt.Sprintf("%d harness session(s) are unmanaged", report.Native.Coverage.Unmanaged))
		}
	}

	report.Verdict = worseVerdict(report.Verdict, report.Daemon.Verdict)
	report.Verdict = worseVerdict(report.Verdict, report.Hooks.Verdict)
	report.Verdict = worseVerdict(report.Verdict, report.Journal.Verdict)
	report.Verdict = worseVerdict(report.Verdict, report.Native.Verdict)
	report.Healthy = report.Verdict == VerdictHealthy
	return report, nil
}

func inspectJournal(ctx context.Context, paths diagnostics.Paths, policy config.DiagnosticsConfig, journal *diagnostics.Journal, selector Selector, report *StatusReport) JournalReport {
	result := JournalReport{
		Verdict: VerdictHealthy, Enabled: policy.JournalEnabledValue(), Path: paths.JournalDir,
		MaxBytes: int64(policy.JournalMaxMiB) << 20,
	}
	if !result.Enabled {
		return result
	}
	if journal == nil {
		var err error
		journal, err = diagnostics.NewJournal(paths, diagnostics.JournalOptions{
			KeepDays: policy.JournalKeepDays, MaxBytes: result.MaxBytes,
		})
		if err != nil {
			result.Verdict = VerdictUnavailable
			result.Error = err.Error()
			report.add(VerdictUnavailable, "journal_unavailable", err.Error())
			return result
		}
	}
	health, err := journal.Inspect(ctx)
	if err != nil {
		result.Verdict = VerdictUnavailable
		result.Error = err.Error()
		report.add(VerdictUnavailable, "journal_unavailable", err.Error())
		return result
	}
	result.Health = health
	if health.CorruptLines > 0 || health.QuarantinedFiles > 0 {
		result.Verdict = VerdictWarning
		report.add(VerdictWarning, "journal_corruption", fmt.Sprintf("journal has %d corrupt line(s) and %d quarantined file(s)", health.CorruptLines, health.QuarantinedFiles))
	}
	if health.SpoolFiles > 0 {
		result.Verdict = VerdictWarning
		report.add(VerdictWarning, "journal_spool_pending", fmt.Sprintf("%d emergency spool file(s) await reconciliation", health.SpoolFiles))
	}
	if result.MaxBytes > 0 && health.Bytes > result.MaxBytes {
		result.Verdict = VerdictWarning
		report.add(VerdictWarning, "journal_quota", fmt.Sprintf("journal exceeds its quota by %d bytes", health.Bytes-result.MaxBytes))
	}
	for _, schema := range health.ObservedSchemas {
		if schema != diagnostics.SchemaVersion {
			result.Verdict = VerdictWarning
			report.add(VerdictWarning, "journal_schema", fmt.Sprintf("journal contains unsupported schema version %d", schema))
		}
	}
	read, err := journal.ReadTolerant(ctx, selector.JournalSelector(20))
	if err != nil {
		result.Verdict = VerdictUnavailable
		result.Error = err.Error()
		report.add(VerdictUnavailable, "journal_read", err.Error())
		return result
	}
	report.LastSeenEvents = read.Records
	return result
}

func filterHookProfiles(report *HookReport, selector Selector) {
	if report == nil || selector.Profile == "" && selector.Harness == "" {
		return
	}
	profiles := report.Profiles[:0]
	for _, profile := range report.Profiles {
		if selector.Profile != "" && profile.Profile != selector.Profile || selector.Harness != "" && profile.Harness != selector.Harness {
			continue
		}
		profiles = append(profiles, profile)
	}
	report.Profiles = profiles
	report.Verdict = VerdictHealthy
	for _, profile := range profiles {
		report.Verdict = worseVerdict(report.Verdict, profile.Verdict)
	}
	if len(profiles) == 0 && report.Error != "" {
		report.Verdict = VerdictUnavailable
	}
}

func (r *StatusReport) add(verdict Verdict, check, message string) {
	if r == nil || message == "" {
		return
	}
	finding := Finding{Verdict: verdict, Check: check, Message: message}
	if slices.Contains(r.Findings, finding) {
		return
	}
	r.Findings = append(r.Findings, finding)
	r.Verdict = worseVerdict(r.Verdict, verdict)
}

func (r *StatusReport) addHookProfileFinding(profile HookProfile) {
	switch profile.Verdict {
	case VerdictFailure:
		r.add(VerdictFailure, "hooks_profile", profile.Detail())
	case VerdictWarning:
		r.add(VerdictWarning, "hooks_profile", profile.Detail())
	}
}
