package cmd

// The six pairing dimensions. One "asl: unavailable" line was true for all of
// them and useful for none, so what these tests hold is that each condition is
// reported separately, under its own stable finding identifier, at a severity
// that follows the mode the operator declared and nothing else.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
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
		Reconcile: &daemon.ASLReconcileSnapshot{
			StateRoot: "/srv/asl-state", Sweeps: 12, IntervalSeconds: 5, LagSeconds: 3,
			PlansPresent: 4, ContractSetID: aslcontract.ContractSetID,
		},
		Delivery: &daemon.ASLHookDeliverySnapshot{Accepted: 31},
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

// A mismatch is terminal by construction — the daemon refuses the peer every
// time — so it is a failure in every mode rather than a warning that clears.
func TestAContractMismatchFailsInEveryMode(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	for _, mode := range []string{string(aslbridge.ModeAuto), string(aslbridge.ModeRequired)} {
		in := pairedInputs()
		in.Mode = mode
		in.Status.ContractSetID = "asl-amx-1.0000000000000000"
		dim := dimension(t, evaluateASLIntegration(in), aslDimensionContract)
		if dim.Status != aslStatusFail {
			t.Errorf("contract = %s in %s mode, want fail", dim.Status, mode)
		}
		if !strings.Contains(dim.Detail, aslcontract.ContractSetID) ||
			!strings.Contains(dim.Detail, "asl-amx-1.0000000000000000") {
			t.Errorf("detail does not name both sets: %s", dim.Detail)
		}
	}
}

// The bridge and the state root can disagree — an upgraded binary talking to a
// corpus the previous one wrote — and checking only the connected peer misses it.
func TestAStateRootThatPublishesAnotherContractSetIsCaught(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.Reconcile.ContractSetID = "asl-amx-1.1111111111111111"
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionContract)
	if dim.Status != aslStatusFail || !strings.Contains(dim.Detail, "the state root publishes") {
		t.Errorf("contract = %s (%s), want a state-root mismatch", dim.Status, dim.Detail)
	}
}

// A single corrupt plan does not break the pairing, and treating it as a
// strict-doctor failure would make one unreadable directory block a release
// check forever.
func TestProjectionProblemsAreWarningsEvenInRequiredMode(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.Reconcile.InvalidProjections = 2
	in.Reconcile.UnreadableStates = 1
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionProjection)
	if dim.Status != aslStatusWarn {
		t.Fatalf("projection = %s (%s), want warn", dim.Status, dim.Detail)
	}
	for _, want := range []string{"2 invalid projection", "1 unreadable state", "/srv/asl-state"} {
		if !strings.Contains(dim.Detail, want) {
			t.Errorf("detail is missing %q: %s", want, dim.Detail)
		}
	}
	if !strings.Contains(dim.Remediation, "agent-mux pairing") {
		t.Errorf("remediation = %q, want the command that lists the affected plans", dim.Remediation)
	}
}

// A stale bridge still serves reads, so nothing looks broken — the dashboard
// simply describes a world that has moved on.
func TestAStaleSnapshotOrALaggingSweepIsReportedAsStale(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	stale := pairedInputs()
	stale.Status.SnapshotStale = true
	stale.Status.Reason = "the last snapshot predates the current generation"
	if got := dimension(t, evaluateASLIntegration(stale), aslDimensionFreshness); got.Status != aslStatusWarn {
		t.Errorf("freshness = %s (%s), want warn for a stale snapshot", got.Status, got.Detail)
	}

	lagging := pairedInputs()
	lagging.Reconcile.IntervalSeconds = 5
	lagging.Reconcile.LagSeconds = 5 * aslStaleReconcileFactor
	if got := dimension(t, evaluateASLIntegration(lagging), aslDimensionFreshness); got.Status != aslStatusOK {
		t.Errorf("freshness = %s at exactly the tolerance, want ok", got.Status)
	}
	lagging.Reconcile.LagSeconds = 5*aslStaleReconcileFactor + 1
	got := dimension(t, evaluateASLIntegration(lagging), aslDimensionFreshness)
	if got.Status != aslStatusWarn || !strings.Contains(got.Detail, "reconciliation sweep") {
		t.Errorf("freshness = %s (%s), want a lagging-sweep warning", got.Status, got.Detail)
	}
}

// The inbound direction hides best: the hook path answers OK to every producer,
// including the ones whose payloads it has just refused.
func TestQuarantinedHooksFailInRequiredModeAndWarnInAuto(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	for mode, want := range map[string]string{
		string(aslbridge.ModeRequired): aslStatusFail,
		string(aslbridge.ModeAuto):     aslStatusWarn,
	} {
		in := pairedInputs()
		in.Mode = mode
		in.Delivery = &daemon.ASLHookDeliverySnapshot{
			Accepted:            9,
			Quarantined:         4,
			QuarantinedByReason: map[string]uint64{"contract_set_mismatch": 3, "schema_unsupported": 1},
		}
		dim := dimension(t, evaluateASLIntegration(in), aslDimensionDelivery)
		if dim.Status != want {
			t.Errorf("delivery = %s in %s mode, want %s", dim.Status, mode, want)
		}
		// A stable order, so two runs against the same daemon read the same.
		if !strings.Contains(dim.Detail, "contract_set_mismatch=3, schema_unsupported=1") {
			t.Errorf("detail does not break the reasons down in order: %s", dim.Detail)
		}
	}
}

// No hook since startup is not evidence of anything, in either direction.
func TestNoDeliveredHookIsUnknownRatherThanHealthy(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.Delivery = &daemon.ASLHookDeliverySnapshot{}
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionDelivery)
	if dim.Status != aslStatusUnknown {
		t.Errorf("delivery = %s (%s), want unknown", dim.Status, dim.Detail)
	}
}

// A daemon that predates the counters omits them, which is a different fact
// from a daemon reporting zero.
func TestADaemonWithoutTheNewCountersIsUnknownNotZero(t *testing.T) {
	resetDoctorTestState(t)
	aslDoctorStat = func(string) error { return nil }

	in := pairedInputs()
	in.Reconcile, in.Delivery = nil, nil
	integration := evaluateASLIntegration(in)
	for _, name := range []string{aslDimensionProjection, aslDimensionDelivery} {
		dim := dimension(t, integration, name)
		if dim.Status != aslStatusUnknown {
			t.Errorf("%s = %s, want unknown", name, dim.Status)
		}
		if !strings.Contains(dim.Remediation, "daemon restart") {
			t.Errorf("%s remediation = %q, want the restart that picks up this build", name, dim.Remediation)
		}
	}
}

// The mode currently in force is the daemon's; a config edit nobody has
// restarted into is not yet true.
func TestTheRunningDaemonsModeOverridesTheConfiguredOne(t *testing.T) {
	resetDoctorTestState(t)
	doctorLoadConfig = func() (*config.Config, error) {
		enabled := true
		cfg := &config.Config{}
		cfg.Defaults.ASL.Enabled = &enabled
		cfg.Defaults.ASL.Command = "/opt/asl/bin/asl"
		return cfg, nil
	}
	in := gatherASLIntegrationInputs(daemon.Health{
		Alive: true,
		ASL:   &aslbridge.Status{Mode: string(aslbridge.ModeAuto)},
	})
	if in.Mode != string(aslbridge.ModeAuto) {
		t.Errorf("mode = %q, want the daemon's auto", in.Mode)
	}
	if in.ConfiguredCommand != "/opt/asl/bin/asl" {
		t.Errorf("configured command = %q", in.ConfiguredCommand)
	}
	if !in.Configured {
		t.Error("a config that names a command is configured")
	}
}

// A config that cannot be read is its own diagnosis, and not one this section
// should guess around.
func TestAnUnreadableConfigMakesShellResolutionUnknown(t *testing.T) {
	resetDoctorTestState(t)
	doctorLoadConfig = func() (*config.Config, error) { return nil, errors.New("toml: line 4: expected key") }

	in := gatherASLIntegrationInputs(daemon.Health{Alive: true})
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionShellResolution)
	if dim.Status != aslStatusUnknown || !strings.Contains(dim.Detail, "expected key") {
		t.Errorf("shell_resolution = %s (%s), want unknown carrying the parse error", dim.Status, dim.Detail)
	}
}

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
	in.Delivery = &daemon.ASLHookDeliverySnapshot{Accepted: 1, Quarantined: 1}

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
	in.Delivery = &daemon.ASLHookDeliverySnapshot{Quarantined: 2}

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
	}, daemon.EventsExport{})
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
	}, daemon.EventsExport{})
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
