package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// doctorScreenPatternHealth builds a live-daemon health response carrying the
// given screen-pattern load result.
func doctorScreenPatternHealth(patterns *protocol.ScreenPatternsSnapshot) ipc.Health {
	build := doctorTestBuild
	return ipc.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute,
		Build:          &build,
		ScreenPatterns: patterns,
	}
}

func TestDoctorReportsEmbeddedScreenPatternSourcesWithoutFinding(t *testing.T) {
	health := doctorScreenPatternHealth(&protocol.ScreenPatternsSnapshot{
		Sources: map[string]string{
			"claude":       screenstate.SourceEmbedded,
			"codex":        screenstate.SourceEmbedded,
			"contextlimit": screenstate.SourceEmbedded,
		},
	})
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"screen patterns:",
		"- claude: embedded",
		"- codex: embedded",
		"- contextlimit: embedded",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "screenstate_override_invalid") {
		t.Errorf("clean pattern load raised the override finding:\n%s", got)
	}
}

// A valid override is not a health problem, but it must be visible: it is the
// most likely explanation for a classifier that stopped matching, so the active
// path is printed and no finding is raised.
func TestDoctorReportsAppliedScreenPatternOverrideAsSourceOnly(t *testing.T) {
	overridePath := "/home/op/.config/agent-mux/screenstate/codex.toml"
	health := doctorScreenPatternHealth(&protocol.ScreenPatternsSnapshot{
		Sources: map[string]string{
			"claude": screenstate.SourceEmbedded,
			"codex":  overridePath,
		},
		Warnings: []protocol.ScreenPatternWarning{{
			Kind:    string(screenstate.WarnOverrideApplied),
			Harness: "codex",
			Path:    overridePath,
			Detail:  "embedded patterns replaced by override",
		}},
	})
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("strict doctor with a valid override: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"- claude: embedded",
		"- codex: override (" + overridePath + ")",
		"overall: clean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "screenstate_override_invalid") {
		t.Errorf("an applied override raised the invalid-override finding:\n%s", got)
	}
}

func TestDoctorBrokenScreenPatternOverrideWarnsWithoutFailingStrict(t *testing.T) {
	overridePath := "/home/op/.config/agent-mux/screenstate/claude.toml"
	health := doctorScreenPatternHealth(&protocol.ScreenPatternsSnapshot{
		Sources: map[string]string{"claude": screenstate.SourceEmbedded},
		Warnings: []protocol.ScreenPatternWarning{{
			Kind:    string(screenstate.WarnOverrideInvalid),
			Harness: "claude",
			Path:    overridePath,
			Detail:  `regex "working_status_line": missing closing )`,
		}},
	})
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorStrict = true
	doctorJSON = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	// The whole point of warning severity: a bad override is
	// degraded-but-working, so `--strict` must still exit zero.
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("strict doctor with a broken override: %v", err)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor report: %v\n%s", err, out.String())
	}
	// v3 was bumped for screenstate_persistent_decline, a FAILURE identifier.
	// This warning-severity finding is not what moved it and must never move it
	// again.
	if report.SchemaVersion != 3 {
		t.Errorf("schema_version = %d; want 3", report.SchemaVersion)
	}
	if !report.Healthy {
		t.Errorf("a warning-severity finding must not make the report unhealthy: %+v", report.Findings)
	}
	var found *doctorFinding
	for i := range report.Findings {
		if report.Findings[i].Check == "screenstate_override_invalid" {
			found = &report.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no screenstate_override_invalid finding: %+v", report.Findings)
	}
	if found.Severity != severityWarning {
		t.Errorf("severity = %q; want %q", found.Severity, severityWarning)
	}
	if !strings.Contains(found.Message, overridePath) || !strings.Contains(found.Message, "override_invalid") {
		t.Errorf("finding message does not identify the file or the reason: %q", found.Message)
	}
	if report.Daemon.ScreenPatterns == nil || report.Daemon.ScreenPatterns.Sources["claude"] != screenstate.SourceEmbedded {
		t.Errorf("JSON report does not carry the active source: %+v", report.Daemon.ScreenPatterns)
	}
}

// An unreadable override and an unresolvable config directory are the other
// degrade-to-embedded kinds; they share the finding with an invalid file
// because the operator's next move is the same.
func TestDoctorScreenPatternWarningKindsAllRaiseTheOverrideFinding(t *testing.T) {
	for _, kind := range []screenstate.LoadWarningKind{
		screenstate.WarnOverrideUnreadable,
		screenstate.WarnOverrideDirUnresolved,
		screenstate.WarnEmbeddedInvalid,
	} {
		t.Run(string(kind), func(t *testing.T) {
			health := doctorScreenPatternHealth(&protocol.ScreenPatternsSnapshot{
				Sources:  map[string]string{"claude": screenstate.SourceEmbedded},
				Warnings: []protocol.ScreenPatternWarning{{Kind: string(kind), Detail: "boom"}},
			})
			setUpDoctorFixture(t, health, protocol.EventsExport{})

			var out bytes.Buffer
			doctorCmd.SetOut(&out)
			if err := runDoctor(doctorCmd, nil); err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if !strings.Contains(out.String(), "screenstate_override_invalid") {
				t.Errorf("%s did not raise the finding:\n%s", kind, out.String())
			}
		})
	}
}

// A daemon that predates the block reports nothing rather than claiming the
// patterns are embedded — the distinction an operator needs after an upgrade.
func TestDoctorReportsUnknownScreenPatternsForOlderDaemon(t *testing.T) {
	build := doctorTestBuild
	health := ipc.Health{Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build}
	setUpDoctorFixture(t, health, protocol.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "screen patterns: unreported (older daemon; restart required)") {
		t.Errorf("doctor output missing the unreported line:\n%s", out.String())
	}
}
