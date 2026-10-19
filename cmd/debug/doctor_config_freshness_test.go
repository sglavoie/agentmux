package debug

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestDoctorConfigFreshness(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	uptime := time.Hour
	cases := []struct {
		name    string
		modTime time.Time
		uptime  time.Duration
		want    bool
	}{
		{"edited before the daemon started", now.Add(-2 * time.Hour), uptime, false},
		{"written during the restart itself", now.Add(-uptime).Add(time.Second), uptime, false},
		{"edited after the daemon started", now.Add(-10 * time.Minute), uptime, true},
		{"no uptime reported", now.Add(-time.Minute), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDoctorTestState(t)
			doctorNow = func() time.Time { return now }
			modTime := tc.modTime
			doctorConfigModTime = func() (time.Time, error) { return modTime, nil }
			var checks []string
			checkDoctorConfigFreshness(tc.uptime, func(severity, check, format string, args ...any) {
				if severity != severityWarning {
					t.Fatalf("severity = %q, want warning", severity)
				}
				checks = append(checks, check+": "+fmt.Sprintf(format, args...))
			})
			if got := len(checks) == 1; got != tc.want {
				t.Fatalf("findings = %v, want warning=%v", checks, tc.want)
			}
		})
	}
}

func TestDoctorDaemonSettingsUsesEffectiveComparisonInsteadOfFileAge(t *testing.T) {
	resetDoctorTestState(t)
	cfg := &config.Config{}
	running := cfg.DaemonSettings()
	doctorLoadConfig = func() (*config.Config, error) { return cfg, nil }
	doctorConfigModTime = func() (time.Time, error) {
		t.Fatal("known settings must not consult file age")
		return time.Time{}, nil
	}
	var messages []string
	add := func(severity, check, format string, args ...any) {
		messages = append(messages, check+": "+fmt.Sprintf(format, args...))
	}
	cfg.Projects = []config.Session{{Title: "New project", Directory: "/elsewhere"}}
	checkDoctorDaemonSettings(running, time.Hour, add)
	if len(messages) != 0 {
		t.Fatalf("project edit warned: %v", messages)
	}
	cfg.Defaults.Notifications.Stop = true
	checkDoctorDaemonSettings(running, time.Hour, add)
	if len(messages) != 1 || !strings.Contains(messages[0], "Notifications") || !strings.Contains(messages[0], "daemon-status") {
		t.Fatalf("warning = %v", messages)
	}
	messages = nil
	checkDoctorDaemonSettings(cfg.DaemonSettings(), time.Second, add)
	if len(messages) != 0 {
		t.Fatalf("applied settings warned: %v", messages)
	}
}
