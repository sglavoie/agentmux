package debugger

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type NativeCoverage struct {
	Managed   int `json:"managed"`
	External  int `json:"external"`
	Degraded  int `json:"degraded"`
	Disabled  int `json:"disabled"`
	Unmanaged int `json:"unmanaged"`
}

func (c *NativeCoverage) add(value string) {
	switch value {
	case "managed":
		c.Managed++
	case "external":
		c.External++
	case "degraded":
		c.Degraded++
	case "disabled":
		c.Disabled++
	default:
		c.Unmanaged++
	}
}

type NativeSession struct {
	WindowID       int    `json:"window_id,omitempty"`
	Profile        string `json:"profile,omitempty"`
	Harness        string `json:"harness,omitempty"`
	HarnessSession string `json:"harness_session_id,omitempty"`
	ASLRunID       string `json:"asl_run_id,omitempty"`
	LaunchID       string `json:"launch_id,omitempty"`
	Coverage       string `json:"coverage"`
	LogPath        string `json:"log_path,omitempty"`
	LogLevel       string `json:"log_level,omitempty"`
	Override       string `json:"override,omitempty"`
}

type NativeReport struct {
	Verdict        Verdict                  `json:"verdict"`
	Enabled        bool                     `json:"enabled"`
	KittyAvailable bool                     `json:"kitty_available"`
	Path           string                   `json:"path,omitempty"`
	Coverage       NativeCoverage           `json:"coverage"`
	Sessions       []NativeSession          `json:"sessions"`
	Storage        diagnostics.NativeReport `json:"storage"`
	MaxBytes       int64                    `json:"max_bytes"`
	LaunchMaxBytes int64                    `json:"launch_max_bytes"`
	Error          string                   `json:"error,omitempty"`
}

// InspectNative evaluates coverage and quotas from the same evidence used by
// doctor. It never mutates native log storage.
func InspectNative(paths diagnostics.Paths, policy config.DiagnosticsConfig, windows []kitty.OSWindow, kittyAvailable bool, selector Selector, now time.Time) NativeReport {
	report := NativeReport{
		Verdict: VerdictHealthy, Enabled: policy.NativeLoggingValue(), KittyAvailable: kittyAvailable,
		Path: paths.NativeDir, Sessions: make([]NativeSession, 0),
		MaxBytes: int64(policy.NativeMaxMiB) << 20, LaunchMaxBytes: int64(policy.NativeSessionMaxMiB) << 20,
	}
	if !report.Enabled {
		return report
	}
	if kittyAvailable {
		report.Sessions, report.Coverage = nativeSessions(windows, selector)
	} else {
		report.Verdict = VerdictWarning
	}
	storage, err := diagnostics.InspectNative(paths, diagnostics.NativeOptions{
		KeepDays: policy.NativeKeepDays, MaxBytes: report.MaxBytes,
		LaunchMaxBytes: report.LaunchMaxBytes, Now: func() time.Time { return now },
	}, ActiveLaunchIDs(windows))
	if err != nil {
		report.Error = err.Error()
		report.Verdict = VerdictUnavailable
		return report
	}
	report.Storage = storage
	if report.Coverage.Degraded > 0 || report.Coverage.Unmanaged > 0 ||
		storage.ActiveExcessBytes > 0 || storage.ExcessBytes > 0 || len(storage.Errors) > 0 {
		report.Verdict = VerdictWarning
	}
	return report
}

// ActiveLaunchIDs returns every launch currently visible in Kitty, regardless
// of an investigation selector, so active writers are never misclassified.
func ActiveLaunchIDs(windows []kitty.OSWindow) map[string]struct{} {
	active := make(map[string]struct{})
	for _, osWindow := range windows {
		for _, tab := range osWindow.Tabs {
			for _, window := range tab.Windows {
				if window.UserVars.LaunchID != "" {
					active[window.UserVars.LaunchID] = struct{}{}
				}
			}
		}
	}
	return active
}

func nativeSessions(windows []kitty.OSWindow, selector Selector) ([]NativeSession, NativeCoverage) {
	var coverage NativeCoverage
	sessions := make([]NativeSession, 0)
	for _, osWindow := range windows {
		for _, tab := range osWindow.Tabs {
			managed := false
			for _, window := range tab.Windows {
				vars := window.UserVars
				if vars.EffectiveProfile() == "" {
					continue
				}
				managed = true
				value := strings.ToLower(strings.TrimSpace(vars.NativeCoverage))
				if vars.LaunchID == "" || (value != "managed" && value != "external" && value != "degraded" && value != "disabled") {
					value = "unmanaged"
				}
				session := NativeSession{
					WindowID: window.ID, Profile: vars.EffectiveProfile(), Harness: vars.EffectiveHarness(),
					HarnessSession: vars.HarnessSessionID, ASLRunID: vars.ASLRunID,
					LaunchID: vars.LaunchID, Coverage: value, LogPath: vars.NativeLogPath,
					LogLevel: vars.NativeLogLevel, Override: vars.NativeOverride,
				}
				if nativeSessionMatches(session, selector) {
					coverage.add(value)
					sessions = append(sessions, session)
				}
				break
			}
			if managed {
				continue
			}
			for _, window := range tab.Windows {
				if harnessName := harnessProcess(window); harnessName != "" {
					session := NativeSession{WindowID: window.ID, Harness: harnessName, Coverage: "unmanaged"}
					if nativeSessionMatches(session, selector) {
						coverage.add("unmanaged")
						sessions = append(sessions, session)
					}
					break
				}
			}
		}
	}
	return sessions, coverage
}

func nativeSessionMatches(session NativeSession, selector Selector) bool {
	return (selector.Harness == "" || session.Harness == selector.Harness) &&
		(selector.Profile == "" || session.Profile == selector.Profile) &&
		(selector.WindowID == 0 || session.WindowID == selector.WindowID) &&
		(selector.SessionID == "" || session.HarnessSession == selector.SessionID) &&
		(selector.ASLRunID == "" || session.ASLRunID == selector.ASLRunID)
}

func harnessProcess(window kitty.Window) string {
	for _, process := range window.ForegroundProcesses {
		if len(process.Cmdline) == 0 {
			continue
		}
		if name := harness.NormalizeKnown(filepath.Base(process.Cmdline[0])); name != "" {
			return name
		}
	}
	return ""
}

func (r NativeReport) Summary() string {
	return fmt.Sprintf("managed=%d external=%d degraded=%d disabled=%d unmanaged=%d",
		r.Coverage.Managed, r.Coverage.External, r.Coverage.Degraded, r.Coverage.Disabled, r.Coverage.Unmanaged)
}
