package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// What the report renders: the findings each dimension raises, their
// remediations, and the text section an operator actually reads.

// Every non-ok dimension has to reach the findings list under its own stable
// identifier, because that is what a JSON consumer keys on.
func TestEachDimensionRaisesItsOwnStableFinding(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorLookPath = func(string) (string, error) { return "/Users/op/.local/bin/asl", nil }

	in := pairedInputs()
	in.ConfiguredCommand = "asl"
	in.Status.ExecutableSource = aslbridge.SourcePath
	in.Status.ContractSetID = "asl-amx-1.2222222222222222"
	in.Status.SnapshotStale = true
	in.Reconcile.InvalidProjections = 1
	in.Delivery = &protocol.ASLHookDeliverySnapshot{Accepted: 1, Quarantined: 1}

	findings := renderIntegration(t, evaluateASLIntegration(in))
	for _, want := range []string{
		findingASLShellResolution, findingASLDaemonResolution, findingASLContractMismatch,
		findingASLProjection, findingASLFreshness, findingASLDelivery,
	} {
		if _, ok := findings[want]; !ok {
			t.Errorf("no %s finding in %v", want, findings)
		}
	}
	if findings[findingASLShellResolution] != severityWarning {
		t.Errorf("%s = %s, want a warning", findingASLShellResolution, findings[findingASLShellResolution])
	}
	if findings[findingASLContractMismatch] != severityFailure {
		t.Errorf("%s = %s, want a failure", findingASLContractMismatch, findings[findingASLContractMismatch])
	}
}

// Every finding carries the exact command that addresses it; a diagnosis
// without a remedy is what the old single line already was.
func TestEveryRaisedFindingCarriesARemediation(t *testing.T) {
	resetDoctorTestState(t)

	in := pairedInputs()
	in.Status.Available = false
	in.Status.Reason = "connection refused"
	in.Reconcile.SweepErrors = 3
	in.Delivery = &protocol.ASLHookDeliverySnapshot{Quarantined: 2}

	var out bytes.Buffer
	var messages []string
	err := writeDoctorASLIntegration(&out, evaluateASLIntegration(in),
		func(severity, check, format string, args ...any) {
			messages = append(messages, check+": "+fmt.Sprintf(format, args...))
		})
	if err != nil {
		t.Fatalf("writeDoctorASLIntegration() = %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("nothing was raised")
	}
	for _, message := range messages {
		if !strings.Contains(message, "remediation:") {
			t.Errorf("finding carries no remediation: %s", message)
		}
	}
}

// The section is rendered for humans too, and a report that only exists in JSON
// is not one an operator reads.
func TestTheTextSectionNamesEveryDimensionAndItsRemediation(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorLookPath = func(string) (string, error) { return "/Users/op/.local/bin/asl", nil }

	in := pairedInputs()
	in.ConfiguredCommand = "asl"
	var out bytes.Buffer
	if err := writeDoctorASLIntegration(&out, evaluateASLIntegration(in), func(string, string, string, ...any) {}); err != nil {
		t.Fatalf("writeDoctorASLIntegration() = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "asl integration: mode required") {
		t.Errorf("the section does not restate the mode:\n%s", text)
	}
	for _, name := range aslDimensionOrder {
		if !strings.Contains(text, name) {
			t.Errorf("the section omits %s:\n%s", name, text)
		}
	}
	if !strings.Contains(text, "remediation: run `amx config asl-sync` to persist the absolute path") {
		t.Errorf("the section does not print the remediation:\n%s", text)
	}
}

// End to end: the report is schema v2 and carries the block, and a required
// integration the daemon cannot reach fails `--strict`.
func TestStrictDoctorFailsForAnUnavailableRequiredIntegration(t *testing.T) {
	setUpDoctorFixture(t, daemon.Health{
		Alive: true, Ready: true, PID: 1,
		ASL: &aslbridge.Status{
			Mode:      string(aslbridge.ModeRequired),
			Available: false,
			Reason:    `asl bridge: exec: "asl": executable file not found in $PATH`,
		},
	}, protocol.EventsExport{})
	doctorJSON, doctorStrict = true, true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)
	if err == nil {
		t.Fatal("runDoctor() = nil, want a strict failure")
	}
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != exitHealthFailure {
		t.Fatalf("err = %v, want exit %d", err, exitHealthFailure)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != 3 {
		t.Errorf("schema_version = %d, want 3", report.SchemaVersion)
	}
	if report.Healthy {
		t.Error("healthy = true despite an unavailable required integration")
	}
	if report.ASLIntegration.Mode != string(aslbridge.ModeRequired) {
		t.Errorf("mode = %q", report.ASLIntegration.Mode)
	}
	if got := dimension(t, report.ASLIntegration, aslDimensionDaemonResolution); got.Status != aslStatusFail {
		t.Errorf("daemon_resolution = %s (%s), want fail", got.Status, got.Detail)
	}
	// The identifier that predates schema v2 keeps reporting this condition.
	if !hasFinding(report.Findings, findingASLUnavailable, severityFailure) {
		t.Errorf("the pre-existing %s finding was lost: %+v", findingASLUnavailable, report.Findings)
	}
	if !hasFinding(report.Findings, findingASLDaemonResolution, severityFailure) {
		t.Errorf("no %s finding: %+v", findingASLDaemonResolution, report.Findings)
	}
}

// A read-only bridge keeps its own identifier and its own meaning.
func TestTheReadOnlyFindingIsPreserved(t *testing.T) {
	setUpDoctorFixture(t, daemon.Health{
		Alive: true, Ready: true, PID: 1,
		ASL: &aslbridge.Status{
			Mode:             string(aslbridge.ModeAuto),
			Available:        true,
			MutationsEnabled: false,
			Executable:       "/opt/asl/bin/asl",
			ExecutableSource: aslbridge.SourceConfigured,
			ContractSetID:    aslcontract.ContractSetID,
		},
	}, protocol.EventsExport{})
	doctorJSON = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor() = %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !hasFinding(report.Findings, findingASLReadOnly, severityWarning) {
		t.Errorf("the %s finding was lost: %+v", findingASLReadOnly, report.Findings)
	}
}

// renderIntegration collects the findings the section raises, keyed by
// identifier, so a test can assert on both the set and the severities.
func renderIntegration(t *testing.T, integration doctorASLIntegration) map[string]string {
	t.Helper()
	findings := map[string]string{}
	var discard bytes.Buffer
	err := writeDoctorASLIntegration(&discard, integration, func(severity, check, _ string, _ ...any) {
		findings[check] = severity
	})
	if err != nil {
		t.Fatalf("writeDoctorASLIntegration() = %v", err)
	}
	return findings
}

func hasFinding(findings []doctorFinding, check, severity string) bool {
	for _, finding := range findings {
		if finding.Check == check && finding.Severity == severity {
			return true
		}
	}
	return false
}
