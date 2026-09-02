package cmd

// The six pairing dimensions. One "asl: unavailable" line was true for all of
// them and useful for none, so what these tests hold is that each condition is
// reported separately, under its own stable finding identifier, at a severity
// that follows the mode the operator declared and nothing else.

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// dimension pulls one answer out of a report by name.
func dimension(t *testing.T, integration doctorASLIntegration, name string) doctorASLDimension {
	t.Helper()
	for _, dim := range integration.Dimensions {
		if dim.Name == name {
			return dim
		}
	}
	t.Fatalf("no %s dimension in %+v", name, integration.Dimensions)
	return doctorASLDimension{}
}

// pairedInputs is a healthy pairing: everything resolved, both halves agree,
// the sweep is current, and hooks are arriving.
func pairedInputs() aslIntegrationInputs {
	return aslIntegrationInputs{
		Mode:              string(aslbridge.ModeRequired),
		ConfiguredCommand: "/opt/asl/bin/asl",
		Configured:        true,
		DaemonAlive:       true,
		Status: &aslbridge.Status{
			Mode:             string(aslbridge.ModeRequired),
			Available:        true,
			MutationsEnabled: true,
			Executable:       "/opt/asl/bin/asl",
			ExecutableSource: aslbridge.SourceConfigured,
			ContractSetID:    aslcontract.ContractSetID,
		},
		Reconcile: &protocol.ASLReconcileSnapshot{
			StateRoot: "/srv/asl-state", Sweeps: 12, IntervalSeconds: 5, LagSeconds: 3,
			PlansPresent: 4, ContractSetID: aslcontract.ContractSetID,
		},
		Delivery: &protocol.ASLHookDeliverySnapshot{Accepted: 31},
	}
}

// The healthy case has to be quiet, or none of the warnings below mean anything.
func TestAHealthyPairingReportsSixOKDimensionsAndNoFindings(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	integration := evaluateASLIntegration(pairedInputs())
	if len(integration.Dimensions) != len(aslDimensionOrder) {
		t.Fatalf("got %d dimensions, want %d", len(integration.Dimensions), len(aslDimensionOrder))
	}
	for _, dim := range integration.Dimensions {
		if dim.Status != aslStatusOK {
			t.Errorf("%s = %s (%s), want ok", dim.Name, dim.Status, dim.Detail)
		}
	}
	if findings := renderIntegration(t, integration); len(findings) != 0 {
		t.Errorf("a healthy pairing raised %v", findings)
	}
}

// Dimension names and their order are a JSON consumer contract.
func TestTheDimensionsAreReportedInAStableOrder(t *testing.T) {
	integration := evaluateASLIntegration(pairedInputs())
	for i, dim := range integration.Dimensions {
		if dim.Name != aslDimensionOrder[i] {
			t.Errorf("dimension %d = %s, want %s", i, dim.Name, aslDimensionOrder[i])
		}
	}
}

// This is the whole bug the section exists for: the shell finds `asl`, the
// login agent that spawns the daemon does not, and nothing about the successful
// lookup here predicts the failing one there.
func TestABareNameThatThisShellResolvesIsStillAWarning(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorLookPath = func(string) (string, error) { return "/Users/op/.local/bin/asl", nil }

	in := pairedInputs()
	in.ConfiguredCommand = "asl"
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionShellResolution)
	if dim.Status != aslStatusWarn {
		t.Fatalf("shell_resolution = %s (%s), want warn", dim.Status, dim.Detail)
	}
	if !strings.Contains(dim.Detail, "does not inherit this PATH") {
		t.Errorf("detail does not explain why a successful lookup is a warning: %s", dim.Detail)
	}
	if !strings.Contains(dim.Remediation, "asl-sync") {
		t.Errorf("remediation = %q, want the command that fixes it", dim.Remediation)
	}
}

// An absolute path resolves without an environment, which is the property the
// pairing exists to establish.
func TestAnAbsolutePathThatExistsIsOK(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	dim := dimension(t, evaluateASLIntegration(pairedInputs()), aslDimensionShellResolution)
	if dim.Status != aslStatusOK {
		t.Errorf("shell_resolution = %s (%s), want ok", dim.Status, dim.Detail)
	}
	if dim.Remediation != "" {
		t.Errorf("an ok dimension carries remediation %q", dim.Remediation)
	}
}

// A persisted path that has since been removed — the upgrade that moved it —
// is the operator's own configuration pointing at nothing.
func TestAConfiguredPathThatIsGoneFailsInRequiredMode(t *testing.T) {
	resetDoctorTestState(t)

	dim := dimension(t, evaluateASLIntegration(pairedInputs()), aslDimensionShellResolution)
	if dim.Status != aslStatusFail {
		t.Errorf("shell_resolution = %s (%s), want fail in required mode", dim.Status, dim.Detail)
	}
	in := pairedInputs()
	in.Mode = string(aslbridge.ModeAuto)
	dim = dimension(t, evaluateASLIntegration(in), aslDimensionShellResolution)
	if dim.Status != aslStatusWarn {
		t.Errorf("shell_resolution = %s in auto mode, want warn", dim.Status)
	}
}

// Auto mode is the default on every machine, including the majority that have
// never heard of agent-session-loop. Absence is not a diagnosis there.
func TestAnUnconfiguredMachineWithoutASLReportsNothingToFix(t *testing.T) {
	resetDoctorTestState(t)

	in := aslIntegrationInputs{Mode: string(aslbridge.ModeAuto), DaemonAlive: true}
	integration := evaluateASLIntegration(in)
	dim := dimension(t, integration, aslDimensionShellResolution)
	if dim.Status != aslStatusNotApplicable {
		t.Errorf("shell_resolution = %s (%s), want not_applicable", dim.Status, dim.Detail)
	}
	if findings := renderIntegration(t, integration); len(findings) != 0 {
		t.Errorf("an unconfigured machine raised %v", findings)
	}
}

// Required mode means the operator declared it mandatory, so the same absence
// is a strict-doctor failure.
func TestRequiredModeWithoutASLFails(t *testing.T) {
	resetDoctorTestState(t)

	in := aslIntegrationInputs{Mode: string(aslbridge.ModeRequired), DaemonAlive: true, Configured: true}
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionShellResolution)
	if dim.Status != aslStatusFail {
		t.Fatalf("shell_resolution = %s (%s), want fail", dim.Status, dim.Detail)
	}
	if !strings.Contains(dim.Remediation, "install agent-session-loop") {
		t.Errorf("remediation = %q, want the exact remediation", dim.Remediation)
	}
}

// The operator said no. Telling them their PATH would not have worked anyway is
// noise in a check they run before every release.
func TestDisabledModeAnswersEveryDimensionNotApplicable(t *testing.T) {
	resetDoctorTestState(t)

	in := pairedInputs()
	in.Mode = string(aslbridge.ModeDisabled)
	integration := evaluateASLIntegration(in)
	for _, dim := range integration.Dimensions {
		if dim.Status != aslStatusNotApplicable {
			t.Errorf("%s = %s in disabled mode, want not_applicable", dim.Name, dim.Status)
		}
	}
	if findings := renderIntegration(t, integration); len(findings) != 0 {
		t.Errorf("a disabled integration raised %v", findings)
	}
}

// Shell resolution ok plus daemon resolution failed *is* the diagnosis, and
// neither half says it alone.
func TestTheDaemonsOwnResolutionIsReportedSeparately(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.Status = &aslbridge.Status{
		Mode:      string(aslbridge.ModeRequired),
		Available: false,
		Reason:    `asl bridge: exec: "asl": executable file not found in $PATH`,
	}
	integration := evaluateASLIntegration(in)
	if got := dimension(t, integration, aslDimensionShellResolution).Status; got != aslStatusOK {
		t.Errorf("shell_resolution = %s, want ok", got)
	}
	daemonDim := dimension(t, integration, aslDimensionDaemonResolution)
	if daemonDim.Status != aslStatusFail {
		t.Fatalf("daemon_resolution = %s (%s), want fail", daemonDim.Status, daemonDim.Detail)
	}
	if !strings.Contains(daemonDim.Detail, "not found in $PATH") {
		t.Errorf("detail does not carry the daemon's reason: %s", daemonDim.Detail)
	}
}

// Working today and working by accident: a reboot, a login-agent change, or a
// restart from a different parent can all take an inherited PATH away.
func TestADaemonThatFoundASLOnItsInheritedPathIsAWarning(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.Status.ExecutableSource = aslbridge.SourcePath
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionDaemonResolution)
	if dim.Status != aslStatusWarn {
		t.Fatalf("daemon_resolution = %s (%s), want warn even in required mode", dim.Status, dim.Detail)
	}
	if !strings.Contains(dim.Remediation, "asl-sync") {
		t.Errorf("remediation = %q", dim.Remediation)
	}
}

// A daemon that is not running has not failed to resolve anything.
func TestADeadDaemonMakesItsDimensionsUnknownRatherThanFailing(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.DaemonAlive, in.Status, in.Reconcile, in.Delivery = false, nil, nil, nil
	integration := evaluateASLIntegration(in)
	for _, name := range []string{
		aslDimensionDaemonResolution, aslDimensionContract,
		aslDimensionProjection, aslDimensionFreshness, aslDimensionDelivery,
	} {
		if got := dimension(t, integration, name).Status; got != aslStatusUnknown {
			t.Errorf("%s = %s against a dead daemon, want unknown", name, got)
		}
	}
	// An unknown raises nothing: the dead daemon already has its own failure,
	// and five more findings would bury it.
	if findings := renderIntegration(t, integration); len(findings) != 0 {
		t.Errorf("a dead daemon raised %v from the integration section", findings)
	}
}
