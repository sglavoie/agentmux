package debug

// The six dimensions the ASL integration section of `amx doctor` answers
// separately. Each returns its own status, detail, and the exact command that
// addresses it; doctor_asl.go decides what their combination means.

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/cli"
)

// aslShellResolutionDimension answers "would this command resolve without an
// environment?" — which is the question, because the daemon does not have the
// operator's.
//
// A bare name that resolves perfectly from this terminal is still a warning.
// That is the entire failure mode: the shell finds it, the login agent that
// spawns the daemon does not, and nothing about the successful lookup here
// predicts the failing one there.
func aslShellResolutionDimension(in aslIntegrationInputs) doctorASLDimension {
	dim := doctorASLDimension{Name: aslDimensionShellResolution}
	if in.ConfigError != "" {
		dim.Status = aslStatusUnknown
		dim.Detail = "config could not be read: " + in.ConfigError
		dim.Remediation = "run `amx config validate`"
		return dim
	}
	command := strings.TrimSpace(in.ConfiguredCommand)
	if command == "" {
		command = "asl"
	}
	if strings.ContainsRune(command, '/') {
		if err := aslDoctorStat(command); err != nil {
			dim.Status = aslSeverityStatus(in.Mode)
			dim.Detail = fmt.Sprintf("defaults.asl.command is %s, which does not exist", command)
			dim.Remediation = "run `amx config asl-sync` to re-resolve and persist the current path"
			return dim
		}
		dim.Status = aslStatusOK
		dim.Detail = fmt.Sprintf("defaults.asl.command is the absolute path %s, so it resolves without an environment", command)
		return dim
	}
	found, err := aslDoctorLookPath(command)
	if err != nil {
		if !in.Configured {
			// Nothing is configured and nothing is installed. That is not a
			// broken pairing, it is the absence of one, and it is what most
			// machines running doctor look like.
			dim.Status = aslStatusNotApplicable
			dim.Detail = "no agent-session-loop integration is configured and no `asl` is installed"
			return dim
		}
		dim.Status = aslSeverityStatus(in.Mode)
		dim.Detail = fmt.Sprintf("defaults.asl.command is the bare name %q and it is not on this shell's PATH either", command)
		dim.Remediation = "install agent-session-loop, then run `amx config asl-sync`"
		return dim
	}
	dim.Status = aslStatusWarn
	dim.Detail = fmt.Sprintf(
		"defaults.asl.command is the bare name %q; this shell resolves it to %s, but the daemon is "+
			"spawned detached and does not inherit this PATH", command, found)
	dim.Remediation = "run `amx config asl-sync` to persist the absolute path"
	return dim
}

// aslDaemonResolutionDimension answers what the daemon itself found, which is
// the only resolution that has ever mattered.
func aslDaemonResolutionDimension(in aslIntegrationInputs) doctorASLDimension {
	dim := doctorASLDimension{Name: aslDimensionDaemonResolution}
	switch {
	case !in.DaemonAlive:
		dim.Status = aslStatusUnknown
		dim.Detail = "the daemon is unreachable, so its resolution is unknown"
		dim.Remediation = "run `amx daemon restart`"
		return dim
	case in.Status == nil:
		dim.Status = aslStatusUnknown
		dim.Detail = "the running daemon does not report an agent-session-loop block"
		dim.Remediation = "run `amx daemon restart` to pick up this build"
		return dim
	case in.Status.Mode == string(aslbridge.ModeDisabled):
		dim.Status = aslStatusNotApplicable
		dim.Detail = "the integration is disabled by defaults.asl.enabled"
		return dim
	case !in.Status.Available && in.Status.Reason == aslbridge.ReasonStarting:
		// Neither a resolution success nor a failure yet: the first child is
		// still handshaking. asl_starting carries the advice to look again.
		dim.Status = aslStatusUnknown
		dim.Detail = "the daemon has started the bridge and is waiting for its first handshake"
		return dim
	case !in.Status.Available && !in.Configured:
		dim.Status = aslStatusNotApplicable
		dim.Detail = "no agent-session-loop integration is configured and the daemon resolved none"
		return dim
	case !in.Status.Available && in.Status.Executable != "":
		// Resolution worked; the child it started is what fails. Pointing at
		// asl-sync here would persist the same path and change nothing. The
		// usual cause is an interpreter the daemon's inherited PATH resolves
		// differently from the operator's shell.
		dim.Status = aslSeverityStatus(in.Mode)
		dim.Detail = fmt.Sprintf("the daemon launched %s but the bridge is not running: %s",
			in.Status.Executable, reasonOrUnstated(in.Status.Reason))
		if in.BridgeFailure != "" {
			dim.Detail += " (last failure in the daemon log: " + in.BridgeFailure + ")"
		}
		dim.Remediation = fmt.Sprintf(
			"make %s run under the daemon's environment (for a script, check that its interpreter resolves on the daemon's PATH, not only your shell's), then run `amx daemon restart`",
			in.Status.Executable)
		return dim
	case !in.Status.Available:
		dim.Status = aslSeverityStatus(in.Mode)
		dim.Detail = "the daemon has not resolved an executable: " + reasonOrUnstated(in.Status.Reason)
		dim.Remediation = "run `amx config asl-sync` to resolve and persist an absolute path"
		return dim
	case in.Status.ExecutableSource == aslbridge.SourcePath:
		// Working today, and working by accident. The daemon inherited a PATH
		// from whatever spawned it; a reboot, a login-agent change, or a
		// restart from a different parent can all take it away.
		dim.Status = aslStatusWarn
		dim.Detail = fmt.Sprintf(
			"the daemon found %s on its inherited PATH rather than at a configured path",
			in.Status.Executable)
		dim.Remediation = "run `amx config asl-sync` to persist the absolute path"
		return dim
	}
	dim.Status = aslStatusOK
	dim.Detail = fmt.Sprintf("the daemon is running %s (%s)", in.Status.Executable, in.Status.ExecutableSource)
	return dim
}

// aslContractDimension compares identity on both channels. The bridge and the
// state root can disagree — an upgraded ASL binary talking to a corpus written
// by the previous one — and only checking the connected peer would miss it.
func aslContractDimension(in aslIntegrationInputs) doctorASLDimension {
	dim := doctorASLDimension{Name: aslDimensionContract}
	if !in.DaemonAlive {
		dim.Status = aslStatusUnknown
		dim.Detail = "the daemon is unreachable, so no contract identity was observed"
		dim.Remediation = "run `amx daemon restart`"
		return dim
	}
	var mismatches []string
	if in.Status != nil && in.Status.Available && in.Status.ContractSetID != "" &&
		in.Status.ContractSetID != aslcontract.ContractSetID {
		mismatches = append(mismatches, "the bridge speaks "+in.Status.ContractSetID)
	}
	// The state root is judged by the rule the reconciler applies to it, not by
	// the bridge's exact match: a reviewed predecessor whose projection and
	// lifecycle-hook components are identical is read normally, and calling it a
	// mismatch would fail a pairing that is observing correctly.
	if in.Reconcile != nil && in.Reconcile.ContractSetID != "" &&
		!aslcontract.SupportsLoopContract(in.Reconcile.ContractSetID) {
		mismatches = append(mismatches, "the state root publishes "+in.Reconcile.ContractSetID)
	}
	if in.Reconcile != nil && in.Reconcile.UnsupportedContracts > 0 {
		mismatches = append(mismatches, fmt.Sprintf(
			"%s named a contract set this build does not implement",
			cli.CountOf(in.Reconcile.UnsupportedContracts, "projection", "projections")))
	}
	if len(mismatches) > 0 {
		// A mismatch is terminal by construction: the daemon refuses the peer
		// every time, so it is a failure in every mode rather than a warning
		// that might clear on its own.
		dim.Status = aslStatusFail
		dim.Detail = fmt.Sprintf("this build implements %s, but %s",
			aslcontract.ContractSetID, strings.Join(mismatches, "; and "))
		dim.Remediation = "upgrade whichever half is older so both report the same contract set"
		return dim
	}
	if in.Status == nil || !in.Status.Available {
		dim.Status = aslStatusUnknown
		dim.Detail = "no peer has announced a contract set; this build implements " + aslcontract.ContractSetID
		dim.Remediation = "run `amx config asl-sync --check`"
		return dim
	}
	dim.Status = aslStatusOK
	dim.Detail = "both halves implement " + aslcontract.ContractSetID
	return dim
}

// aslProjectionDimension reports what the sweep could not read. These are
// warnings rather than failures even in required mode: a single corrupt plan
// does not break the pairing, and treating it as a strict-doctor failure would
// make one unreadable directory block a release check forever.
func aslProjectionDimension(in aslIntegrationInputs) doctorASLDimension {
	dim := doctorASLDimension{Name: aslDimensionProjection}
	if !in.DaemonAlive || in.Reconcile == nil {
		dim.Status = aslStatusUnknown
		dim.Detail = "the daemon reports no reconciliation metrics"
		dim.Remediation = "run `amx daemon restart` to pick up this build"
		return dim
	}
	r := in.Reconcile
	var problems []string
	if r.SweepErrors > 0 {
		problems = append(problems, fmt.Sprintf("%s could not read the state root", cli.CountOf(r.SweepErrors, "sweep", "sweeps")))
	}
	// These two counters accumulate across every sweep since the daemon
	// started, so one bad file counted every ten seconds reads as hundreds of
	// documents unless the wording says what is being counted.
	if r.InvalidProjections > 0 {
		problems = append(problems, fmt.Sprintf("%s across %s", cli.CountOf(r.InvalidProjections, "invalid projection reading", "invalid projection readings"), cli.CountOf(r.Sweeps, "sweep", "sweeps")))
	}
	if r.UnreadableStates > 0 {
		problems = append(problems, fmt.Sprintf("%s across %s", cli.CountOf(r.UnreadableStates, "unreadable state document reading", "unreadable state document readings"), cli.CountOf(r.Sweeps, "sweep", "sweeps")))
	}
	if len(problems) > 0 {
		dim.Status = aslStatusWarn
		dim.Detail = fmt.Sprintf("under %s: %s", r.StateRoot, strings.Join(problems, ", "))
		dim.Remediation = "run `asl agent-mux pairing` to list the affected plans"
		return dim
	}
	if r.Sweeps == 0 {
		dim.Status = aslStatusUnknown
		dim.Detail = "no reconciliation sweep has completed yet under " + r.StateRoot
		dim.Remediation = "wait one reconciliation interval and re-run"
		return dim
	}
	dim.Status = aslStatusOK
	dim.Detail = fmt.Sprintf("%s projected cleanly under %s", cli.CountOf(r.PlansPresent, "plan", "plans"), r.StateRoot)
	return dim
}

// aslFreshnessDimension answers whether what the daemon is showing is current.
// A stale bridge still serves reads, so nothing looks broken — the dashboard
// simply describes a world that has moved on.
func aslFreshnessDimension(in aslIntegrationInputs) doctorASLDimension {
	dim := doctorASLDimension{Name: aslDimensionFreshness}
	if !in.DaemonAlive {
		dim.Status = aslStatusUnknown
		dim.Detail = "the daemon is unreachable"
		dim.Remediation = "run `amx daemon restart`"
		return dim
	}
	var problems []string
	if in.Status != nil && in.Status.Available && in.Status.SnapshotStale {
		problems = append(problems, "the bridge snapshot is stale: "+reasonOrUnstated(in.Status.Reason))
	}
	if r := in.Reconcile; r != nil && r.Sweeps > 0 && r.IntervalSeconds > 0 &&
		r.LagSeconds > r.IntervalSeconds*aslStaleReconcileFactor {
		problems = append(problems, fmt.Sprintf(
			"the last reconciliation sweep completed %.0fs ago against a %.0fs interval",
			r.LagSeconds, r.IntervalSeconds))
	}
	if len(problems) > 0 {
		dim.Status = aslStatusWarn
		dim.Detail = strings.Join(problems, "; ")
		dim.Remediation = "run `amx daemon restart` if this persists"
		return dim
	}
	if in.Status == nil || !in.Status.Available {
		dim.Status = aslStatusNotApplicable
		dim.Detail = "no bridge is connected, so there is no snapshot to age"
		return dim
	}
	dim.Status = aslStatusOK
	dim.Detail = "the bridge snapshot and the reconciliation sweep are both current"
	return dim
}

// aslDeliveryDimension reports the inbound direction. It is the one that hides
// best: the hook path answers OK to every producer, including the ones whose
// payloads it has just refused, so a pairing can quarantine every event without
// a single visible error anywhere.
func aslDeliveryDimension(in aslIntegrationInputs) doctorASLDimension {
	dim := doctorASLDimension{Name: aslDimensionDelivery}
	if !in.DaemonAlive || in.Delivery == nil {
		dim.Status = aslStatusUnknown
		dim.Detail = "the daemon reports no lifecycle-hook delivery counters"
		dim.Remediation = "run `amx daemon restart` to pick up this build"
		return dim
	}
	d := in.Delivery
	if d.Quarantined > 0 {
		// A refusal is non-retryable by construction, so every quarantined
		// event is one the operator has already lost. That makes this a
		// failure in required mode rather than a running tally.
		dim.Status = aslSeverityStatus(in.Mode)
		dim.Detail = fmt.Sprintf("%s %s quarantined (%s); %d accepted",
			cli.CountOf(d.Quarantined, "lifecycle hook", "lifecycle hooks"), cli.ByCount(d.Quarantined, "was", "were"),
			formatQuarantineReasons(d.QuarantinedByReason), d.Accepted)
		dim.Remediation = "run `amx config asl-sync --check` to compare contract sets"
		return dim
	}
	if d.Accepted == 0 {
		dim.Status = aslStatusUnknown
		dim.Detail = "no lifecycle hook has been delivered since this daemon started"
		dim.Remediation = "start an `asl run` and re-run, or check the harness hook configuration"
		return dim
	}
	dim.Status = aslStatusOK
	dim.Detail = fmt.Sprintf("%s accepted, none quarantined", cli.CountOf(d.Accepted, "lifecycle hook", "lifecycle hooks"))
	return dim
}
