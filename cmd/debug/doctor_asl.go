package debug

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
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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
	Reconcile *protocol.ASLReconcileSnapshot
	Delivery  *protocol.ASLHookDeliverySnapshot
	// BridgeFailure is the newest bridge-exit error from the daemon's private
	// log, unsanitized, or "" when there is none. It is read only when the
	// daemon reports a launched executable that is not connected.
	BridgeFailure string
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
func gatherASLIntegrationInputs(health ipc.Health) aslIntegrationInputs {
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
	if health.ASL != nil && !health.ASL.Available && health.ASL.Executable != "" {
		in.BridgeFailure = doctorLastASLBridgeFailure()
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
