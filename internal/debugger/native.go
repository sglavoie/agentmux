package debugger

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type NativeCoverage struct {
	Managed   int `json:"managed"`
	External  int `json:"external"`
	Degraded  int `json:"degraded"`
	Disabled  int `json:"disabled"`
	Unmanaged int `json:"unmanaged"`
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
	Path           string                   `json:"path,omitempty"`
	Coverage       NativeCoverage           `json:"coverage"`
	Sessions       []NativeSession          `json:"sessions"`
	Storage        diagnostics.NativeReport `json:"storage"`
	MaxBytes       int64                    `json:"max_bytes"`
	LaunchMaxBytes int64                    `json:"launch_max_bytes"`
	Error          string                   `json:"error,omitempty"`
}

// InspectNative reports native log storage without changing it.
func InspectNative(paths diagnostics.Paths, policy config.DiagnosticsConfig, now time.Time) NativeReport {
	report := NativeReport{
		Verdict: VerdictHealthy, Enabled: policy.NativeLoggingValue(),
		Path: paths.NativeDir, Sessions: make([]NativeSession, 0),
		MaxBytes: int64(policy.NativeMaxMiB) << 20, LaunchMaxBytes: int64(policy.NativeSessionMaxMiB) << 20,
	}
	if !report.Enabled {
		return report
	}
	storage, err := diagnostics.InspectNative(paths, diagnostics.NativeOptions{
		KeepDays: policy.NativeKeepDays, MaxBytes: report.MaxBytes,
		LaunchMaxBytes: report.LaunchMaxBytes, Now: func() time.Time { return now },
	}, nil)
	if err != nil {
		report.Error = err.Error()
		report.Verdict = VerdictUnavailable
		return report
	}
	report.Storage = storage
	if storage.ActiveExcessBytes > 0 || storage.ExcessBytes > 0 || len(storage.Errors) > 0 {
		report.Verdict = VerdictWarning
	}
	return report
}

func (r NativeReport) Summary() string {
	return fmt.Sprintf("launches=%d active=%d", r.Storage.Launches, r.Storage.ActiveLaunches)
}

func nativeSessionMatches(session NativeSession, selector Selector) bool {
	return (selector.Harness == "" || session.Harness == selector.Harness) &&
		(selector.Profile == "" || session.Profile == selector.Profile) &&
		(selector.WindowID == 0 || session.WindowID == selector.WindowID) &&
		(selector.SessionID == "" || session.HarnessSession == selector.SessionID) &&
		(selector.ASLRunID == "" || session.ASLRunID == selector.ASLRunID)
}
