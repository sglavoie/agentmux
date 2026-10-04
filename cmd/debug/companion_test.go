package debug

// What doctor says about connected companion apps: an informational finding
// per app with its version, a warning when the amx an app bundles is not the
// daemon's artifact, and silence for `amx debug subscribe`, which is this CLI
// rather than an app.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func runDoctorJSONForCompanion(t *testing.T, companion *protocol.CompanionHealth) doctorReport {
	t.Helper()
	build := doctorTestBuild
	health := ipc.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build, Companion: companion}
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorJSON = true
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("a companion finding must not fail --strict: %v\n%s", err, out.String())
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v\n%s", err, out.String())
	}
	return report
}

func findingsFor(report doctorReport, check string) []doctorFinding {
	var out []doctorFinding
	for _, finding := range report.Findings {
		if finding.Check == check {
			out = append(out, finding)
		}
	}
	return out
}

func TestDoctorCompanionAppIsInformational(t *testing.T) {
	same := doctorTestBuild
	report := runDoctorJSONForCompanion(t, &protocol.CompanionHealth{
		Subscribers: 2,
		Clients: []protocol.CompanionClient{
			{SubscriberID: "sub-1", Client: "AgentMux.app", ClientVersion: "1.4.0", AMXBuild: &same},
			{SubscriberID: "sub-2", Client: subscribeClientName, ClientVersion: "0.0.1", AMXBuild: &buildinfo.Info{Version: "other", BinaryHash: "hash-z"}},
		},
	})
	apps := findingsFor(report, checkCompanionApp)
	if len(apps) != 1 || apps[0].Severity != severityInfo || apps[0].Message != "companion app connected: AgentMux.app 1.4.0" {
		t.Fatalf("companion_app findings = %+v; want one info finding for the app alone", apps)
	}
	if got := findingsFor(report, checkCompanionBuildMismatch); len(got) != 0 {
		t.Fatalf("a same-artifact app and the debug subscriber raised %+v", got)
	}
	if !report.Healthy {
		t.Fatal("an informational finding made the report unhealthy")
	}
	if report.Daemon.Companion == nil || report.Daemon.Companion.Subscribers != 2 {
		t.Fatalf("daemon.companion = %+v; want the health block carried through", report.Daemon.Companion)
	}
}

func TestDoctorCompanionBuildMismatchWarns(t *testing.T) {
	report := runDoctorJSONForCompanion(t, &protocol.CompanionHealth{
		Subscribers: 1,
		Clients: []protocol.CompanionClient{{
			SubscriberID: "sub-1", Client: "AgentMux.app", ClientVersion: "1.4.0",
			AMXBuild: &buildinfo.Info{Version: "1.2.2", Revision: "beef", BinaryHash: "hash-b"},
		}},
	})
	mismatches := findingsFor(report, checkCompanionBuildMismatch)
	if len(mismatches) != 1 || mismatches[0].Severity != severityWarning {
		t.Fatalf("companion_build_mismatch findings = %+v; want one warning", mismatches)
	}
	for _, want := range []string{"AgentMux.app 1.4.0", "1.2.2", "1.2.3"} {
		if !strings.Contains(mismatches[0].Message, want) {
			t.Errorf("mismatch message %q missing %q", mismatches[0].Message, want)
		}
	}
	if !report.Healthy {
		t.Fatal("a warning-severity mismatch made the report unhealthy")
	}
}

// An app that reports no amx build cannot be shown to differ, so it raises
// only the informational finding.
func TestDoctorCompanionWithoutBuildIsNotCompared(t *testing.T) {
	report := runDoctorJSONForCompanion(t, &protocol.CompanionHealth{
		Subscribers: 1,
		Clients:     []protocol.CompanionClient{{SubscriberID: "sub-3"}},
	})
	apps := findingsFor(report, checkCompanionApp)
	if len(apps) != 1 || apps[0].Message != "companion app connected: sub-3" {
		t.Fatalf("companion_app findings = %+v", apps)
	}
	if got := findingsFor(report, checkCompanionBuildMismatch); len(got) != 0 {
		t.Fatalf("an app with no amx build raised %+v", got)
	}
}

func TestDoctorCompanionTextLines(t *testing.T) {
	cases := map[string]struct {
		companion *protocol.CompanionHealth
		want      string
	}{
		"older daemon": {nil, "companion: unknown (daemon predates the companion protocol)"},
		"none":         {&protocol.CompanionHealth{}, "companion: no app connected"},
		"debug only": {&protocol.CompanionHealth{Subscribers: 1, Clients: []protocol.CompanionClient{
			{SubscriberID: "sub-1", Client: subscribeClientName},
		}}, "companion: no app connected"},
		"app": {&protocol.CompanionHealth{Subscribers: 1, Clients: []protocol.CompanionClient{
			{SubscriberID: "sub-1", Client: "AgentMux.app", ClientVersion: "1.4.0"},
		}}, "companion: AgentMux.app 1.4.0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			build := doctorTestBuild
			setUpDoctorFixture(t, ipc.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build, Companion: tc.companion}, protocol.EventsExport{})
			var out bytes.Buffer
			doctorCmd.SetOut(&out)
			if err := runDoctor(doctorCmd, nil); err != nil {
				t.Fatalf("runDoctor: %v", err)
			}
			if !strings.Contains(out.String(), tc.want+"\n") {
				t.Fatalf("doctor output missing %q:\n%s", tc.want, out.String())
			}
		})
	}
}
