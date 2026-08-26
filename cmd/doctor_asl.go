package cmd

// The agent-session-loop integration section of `amx doctor`.
//
// A single "asl: unavailable" line was true and useless. Six conditions produce
// it, they have nothing in common, and the fix for each is different:
//
//   - the operator's shell can run `asl` but the daemon's environment cannot;
//   - the daemon found one, on a PATH that will not survive the next reboot;
//   - the two halves were built from different corpora;
//   - the state root is there but its documents cannot be projected;
//   - the bridge is connected and its view of the world has gone stale;
//   - lifecycle hooks are arriving and being quarantined.
//
// So the report names six dimensions and answers each one separately, and every
// non-ok answer carries the exact command that addresses it. Reporting them
// together is also what makes the difference visible: shell-resolution ok plus
// daemon-resolution failed *is* the diagnosis, and neither half says it alone.
//
// Severity follows the mode and nothing else. In required mode an unavailable
// integration is a strict-doctor failure, because the operator declared it
// mandatory. In auto mode it is at most a warning: a machine without ASL is a
// supported configuration, and the core daemon's readiness never depends on
// any of this.

import (
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon"
)

// Dimension names. They are JSON consumer contracts.
const (
	aslDimensionShellResolution  = "shell_resolution"
	aslDimensionDaemonResolution = "daemon_resolution"
	aslDimensionContract         = "contract"
	aslDimensionProjection       = "projection"
	aslDimensionFreshness        = "freshness"
	aslDimensionDelivery         = "delivery"
)

// aslDimensionOrder is the reporting order: outward from what the operator
// controls (their config and PATH) to what only the running system can answer.
var aslDimensionOrder = []string{
	aslDimensionShellResolution,
	aslDimensionDaemonResolution,
	aslDimensionContract,
	aslDimensionProjection,
	aslDimensionFreshness,
	aslDimensionDelivery,
}

// Dimension statuses.
const (
	aslStatusOK      = "ok"
	aslStatusWarn    = "warn"
	aslStatusFail    = "fail"
	aslStatusUnknown = "unknown"
	// aslStatusNotApplicable is for a dimension that cannot have an answer,
	// such as freshness with no bridge. It is deliberately distinct from
	// "unknown", which means the answer exists and could not be obtained.
	aslStatusNotApplicable = "not_applicable"
)

// Stable finding identifiers for the integration section. The two that predate
// schema v2 keep their names: they are what existing consumers key on, and the
// conditions they report have not changed.
const (
	findingASLUnavailable      = "asl_unavailable"
	findingASLReadOnly         = "asl_read_only"
	findingASLShellResolution  = "asl_shell_resolution"
	findingASLDaemonResolution = "asl_daemon_resolution"
	findingASLContractMismatch = "asl_contract_mismatch"
	findingASLProjection       = "asl_projection_errors"
	findingASLFreshness        = "asl_bridge_stale"
	findingASLDelivery         = "asl_hook_delivery"
)

// aslStaleReconcileFactor is how many reconciliation intervals may elapse
// before the sweep is called behind. Two intervals absorbs one missed tick,
// which a busy machine produces routinely and which nothing is wrong with.
const aslStaleReconcileFactor = 2

// doctorASLIntegration is the schema-v2 integration block.
type doctorASLIntegration struct {
	// Mode restates the tri-state the severities below were derived from, so a
	// consumer reading a warning can tell whether it would have been a failure
	// under a different configuration.
	Mode       string               `json:"mode"`
	Dimensions []doctorASLDimension `json:"dimensions"`
}

// doctorASLDimension is one answerable question about the pairing.
type doctorASLDimension struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	// Remediation is the exact command or edit that addresses this dimension.
	// It is empty only when the status is ok or not_applicable.
	Remediation string `json:"remediation,omitempty"`
}

// aslIntegrationInputs is everything the section reads, gathered so the whole
// evaluation is a pure function of it.
type aslIntegrationInputs struct {
	Mode string
	// ConfiguredCommand is defaults.asl.command as written, before resolution.
	ConfiguredCommand string
	// ConfigError is why the config could not be read, if it could not be.
	ConfigError string

	// Configured is whether the operator asked for the integration at all.
	// Auto mode is the default on every machine, including the majority that
	// have never heard of agent-session-loop, so "auto and absent" has to be
	// distinguishable from "auto and asked for but broken".
	Configured bool

	Status    *aslbridge.Status
	Reconcile *daemon.ASLReconcileSnapshot
	Delivery  *daemon.ASLHookDeliverySnapshot
	// DaemonAlive gates every daemon-sourced dimension: against a dead daemon
	// they are unknown, not failing.
	DaemonAlive bool
}

// gatherASLIntegrationInputs collects the section's inputs from one health
// reading and the config.
//
// The mode is taken from the running daemon when it publishes one and from
// config otherwise, in that order: the daemon's mode is the one currently in
// force, and a config edit that has not been restarted into is not yet true.
func gatherASLIntegrationInputs(health daemon.Health) aslIntegrationInputs {
	in := aslIntegrationInputs{
		DaemonAlive: health.Alive,
		Status:      health.ASL,
		Reconcile:   health.ASLReconcile,
		Delivery:    health.ASLHookDelivery,
	}
	cfg, err := doctorLoadConfig()
	if err != nil {
		in.ConfigError = err.Error()
	} else {
		in.ConfiguredCommand = cfg.Defaults.ASL.Command
		in.Mode = cfg.Defaults.ASL.ASLModeValue()
	}
	if health.ASL != nil && health.ASL.Mode != "" {
		in.Mode = health.ASL.Mode
	}
	if in.Mode == "" {
		in.Mode = string(aslbridge.ModeAuto)
	}
	// Any of these is the operator having asked: a command they wrote, a mode
	// they declared, or a bridge that actually connected.
	in.Configured = strings.TrimSpace(in.ConfiguredCommand) != "" ||
		in.Mode == string(aslbridge.ModeRequired) ||
		(health.ASL != nil && health.ASL.Available)
	return in
}

// evaluateASLIntegration answers all six dimensions.
//
// An explicitly disabled integration short-circuits the whole section. The
// operator said no; reporting that their PATH would not have worked anyway is
// not a diagnosis, it is noise in a check they run before every release.
func evaluateASLIntegration(in aslIntegrationInputs) doctorASLIntegration {
	if in.Mode == string(aslbridge.ModeDisabled) {
		dimensions := make([]doctorASLDimension, 0, len(aslDimensionOrder))
		for _, name := range aslDimensionOrder {
			dimensions = append(dimensions, doctorASLDimension{
				Name:   name,
				Status: aslStatusNotApplicable,
				Detail: "the integration is disabled by defaults.asl.enabled",
			})
		}
		return doctorASLIntegration{Mode: in.Mode, Dimensions: dimensions}
	}
	return doctorASLIntegration{
		Mode: in.Mode,
		Dimensions: []doctorASLDimension{
			aslShellResolutionDimension(in),
			aslDaemonResolutionDimension(in),
			aslContractDimension(in),
			aslProjectionDimension(in),
			aslFreshnessDimension(in),
			aslDeliveryDimension(in),
		},
	}
}

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
	case !in.Status.Available && !in.Configured:
		dim.Status = aslStatusNotApplicable
		dim.Detail = "no agent-session-loop integration is configured and the daemon resolved none"
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
	if in.Reconcile != nil && in.Reconcile.ContractSetID != "" &&
		in.Reconcile.ContractSetID != aslcontract.ContractSetID {
		mismatches = append(mismatches, "the state root publishes "+in.Reconcile.ContractSetID)
	}
	if in.Reconcile != nil && in.Reconcile.UnsupportedContracts > 0 {
		mismatches = append(mismatches, fmt.Sprintf(
			"%d projection(s) named a contract set this build does not implement",
			in.Reconcile.UnsupportedContracts))
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
		problems = append(problems, fmt.Sprintf("%d sweep(s) could not read the state root", r.SweepErrors))
	}
	if r.InvalidProjections > 0 {
		problems = append(problems, fmt.Sprintf("%d invalid projection(s)", r.InvalidProjections))
	}
	if r.UnreadableStates > 0 {
		problems = append(problems, fmt.Sprintf("%d unreadable state document(s)", r.UnreadableStates))
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
	dim.Detail = fmt.Sprintf("%d plan(s) projected cleanly under %s", r.PlansPresent, r.StateRoot)
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
		dim.Detail = fmt.Sprintf("%d lifecycle hook(s) were quarantined (%s); %d accepted",
			d.Quarantined, formatQuarantineReasons(d.QuarantinedByReason), d.Accepted)
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
	dim.Detail = fmt.Sprintf("%d lifecycle hook(s) accepted, none quarantined", d.Accepted)
	return dim
}

// formatQuarantineReasons renders the reason breakdown in a stable order, so
// two runs of doctor against the same daemon produce the same line.
func formatQuarantineReasons(byReason map[string]uint64) string {
	if len(byReason) == 0 {
		return "reason unrecorded"
	}
	reasons := make([]string, 0, len(byReason))
	for reason := range byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", reason, byReason[reason]))
	}
	return strings.Join(parts, ", ")
}

// aslSeverityStatus maps the tri-state onto a dimension status for conditions
// that are only as serious as the operator declared the integration to be.
func aslSeverityStatus(mode string) string {
	if mode == string(aslbridge.ModeRequired) {
		return aslStatusFail
	}
	return aslStatusWarn
}

func reasonOrUnstated(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no reason was published"
	}
	return reason
}

// aslDimensionFinding maps a dimension onto the stable finding identifier that
// carries it, or "" for dimensions that produce no finding.
var aslDimensionFinding = map[string]string{
	aslDimensionShellResolution:  findingASLShellResolution,
	aslDimensionDaemonResolution: findingASLDaemonResolution,
	aslDimensionContract:         findingASLContractMismatch,
	aslDimensionProjection:       findingASLProjection,
	aslDimensionFreshness:        findingASLFreshness,
	aslDimensionDelivery:         findingASLDelivery,
}

// writeDoctorASLIntegration renders the section and raises one finding per
// non-ok dimension.
//
// An "unknown" raises nothing. It means doctor could not obtain an answer —
// usually because the daemon is down, which already has its own failure — and
// turning every unknown into a finding would bury the one that says why.
func writeDoctorASLIntegration(out io.Writer, integration doctorASLIntegration, addFinding func(string, string, string, ...any)) error {
	if _, err := fmt.Fprintf(out, "asl integration: mode %s\n", integration.Mode); err != nil {
		return err
	}
	for _, dim := range integration.Dimensions {
		if _, err := fmt.Fprintf(out, "  %-19s %-4s %s\n", dim.Name, dim.Status, dim.Detail); err != nil {
			return err
		}
		if dim.Remediation != "" && dim.Status != aslStatusUnknown {
			if _, err := fmt.Fprintf(out, "  %-19s      remediation: %s\n", "", dim.Remediation); err != nil {
				return err
			}
		}
		check := aslDimensionFinding[dim.Name]
		if check == "" {
			continue
		}
		switch dim.Status {
		case aslStatusFail:
			addFinding(severityFailure, check, "%s; remediation: %s", dim.Detail, dim.Remediation)
		case aslStatusWarn:
			addFinding(severityWarning, check, "%s; remediation: %s", dim.Detail, dim.Remediation)
		}
	}
	return nil
}

// Seams for the two environment reads this section makes. They are separate
// from the daemon's own resolution on purpose: the whole point of the
// shell-resolution dimension is to observe the environment doctor was started
// in, which is the operator's and not the daemon's.
var (
	aslDoctorLookPath = exec.LookPath
	aslDoctorStat     = func(path string) error {
		_, err := doctorStat(path)
		return err
	}
)
