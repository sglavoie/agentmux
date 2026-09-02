package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Contract, projection, freshness, and delivery: the four dimensions that read
// the running daemon rather than the operator's configuration.

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
		in.Delivery = &protocol.ASLHookDeliverySnapshot{
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
	in.Delivery = &protocol.ASLHookDeliverySnapshot{}
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
