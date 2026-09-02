package config

// `amx config asl-sync` — pair this Agent Mux with the ASL beside it.
//
// The failure this command exists to end is a quiet one. Both halves install
// into ~/.local/bin, both work perfectly from a terminal, and the daemon still
// reports `executable file not found in $PATH` — because it was never started
// from a terminal. Nothing is misconfigured, nothing is broken, and the
// integration is simply off. So pairing persists an absolute path once, and
// after that no part of the running system depends on anyone's shell.
//
// Everything that can be wrong is checked before anything is written: which
// executable, whether it speaks this build's contract set, where its state root
// is, whether any live run still carries state neither side can pair with,
// whether the rest of the corpus can be migrated, and whether the daemon can be
// reached at all. `--check` stops exactly there, so the whole preflight is
// available without consenting to a mutation.
//
// Past that point the sequence is: migrate, replace config atomically, restart,
// wait for readiness, and verify the daemon-side executable and handshake. If
// any of those fails, the previous config bytes go back and the daemon is
// restarted onto them. The promise is narrow and total — pairing either ends
// with a verified live bridge, or with the configuration the operator had
// before they ran it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/spf13/cobra"
)

// aslSyncProbeTimeout bounds one pairing survey. The survey walks every plan in
// the state root, so it is not instant on a large corpus, but it is pure
// filesystem work with no network and no locks held — a survey still running
// after this long is stuck rather than slow.
const aslSyncProbeTimeout = 90 * time.Second

// aslSyncMigrateTimeout bounds the migration pass. It is more generous than the
// survey because it writes.
const aslSyncMigrateTimeout = 5 * time.Minute

// aslSyncReadyTimeout bounds the wait for the restarted daemon to come back,
// connect to the bridge, and complete a handshake. Failing to converge inside it
// is a rollback, not a longer wait: an operator left staring at a hung pairing
// command has no way to tell it from a hung daemon.
const aslSyncReadyTimeout = 45 * time.Second

// Structured error codes. They are the JSON contract for this command, so each
// one names a distinct condition with a distinct remediation.
const (
	codeASLNotFound         = "asl_not_found"
	codeASLProbeFailed      = "asl_probe_failed"
	codeASLContractMismatch = "asl_contract_mismatch"
	codeASLStateRoot        = "asl_state_root_unusable"
	codeASLActiveRuns       = "asl_active_incompatible_runs"
	codeASLMigrationFailed  = "asl_migration_failed"
	codeASLConfigAmbiguous  = "asl_config_ambiguous"
	codeASLConfigWrite      = "asl_config_write_failed"
	codeASLDaemonRestart    = "asl_daemon_restart_failed"
	codeASLVerifyFailed     = "asl_verification_failed"
)

// aslPairingReport is the ASL side's survey, decoded from
// `asl agent-mux pairing --json`. Only the fields pairing acts on are named;
// the survey is free to grow others.
type aslPairingReport struct {
	OK             bool   `json:"ok"`
	Reason         string `json:"reason"`
	Error          string `json:"error"`
	Command        string `json:"command"`
	Executable     string `json:"executable"`
	ASLVersion     string `json:"asl_version"`
	ContractSetID  string `json:"contract_set_id"`
	BridgeProtocol int    `json:"bridge_protocol"`

	StateRoot          string `json:"state_root"`
	StateRootPresent   bool   `json:"state_root_present"`
	StateSchemaVersion int    `json:"state_schema_version"`

	Plans                  map[string]int   `json:"plans"`
	ActiveIncompatibleRuns []aslPairingRun  `json:"active_incompatible_runs"`
	Migrations             []aslPairingPlan `json:"migrations"`
	Issues                 []aslPairingPlan `json:"issues"`

	MigrateRequested bool `json:"migrate_requested"`
	Migrated         bool `json:"migrated"`
}

// aslPairingRun identifies one live run whose state this build cannot pair
// with. The identities are reported verbatim so an operator can find and stop
// the process rather than being told that "something" is in the way.
type aslPairingRun struct {
	PlanDir       string `json:"plan_dir"`
	StateDir      string `json:"state_dir"`
	RunID         string `json:"run_id"`
	PID           int    `json:"pid"`
	SchemaVersion int    `json:"schema_version"`
	Detail        string `json:"detail"`
}

type aslPairingPlan struct {
	PlanDir        string `json:"plan_dir"`
	StateDir       string `json:"state_dir"`
	Classification string `json:"classification"`
	Detail         string `json:"detail"`
}

// aslSyncReport is this command's own output.
type aslSyncReport struct {
	// Mode is "check" or "apply". A check reports exactly what an apply would
	// have found, which is the point of having it.
	Mode       string `json:"mode"`
	Applied    bool   `json:"applied"`
	RolledBack bool   `json:"rolled_back"`

	ConfigPath       string `json:"config_path"`
	Command          string `json:"command"`
	Executable       string `json:"executable"`
	ExecutableSource string `json:"executable_source"`
	PreviousCommand  string `json:"previous_command,omitempty"`

	ASLVersion     string `json:"asl_version,omitempty"`
	ContractSetID  string `json:"contract_set_id,omitempty"`
	BridgeProtocol int    `json:"bridge_protocol,omitempty"`
	StateRoot      string `json:"state_root,omitempty"`

	Plans                  map[string]int  `json:"plans,omitempty"`
	ActiveIncompatibleRuns []aslPairingRun `json:"active_incompatible_runs,omitempty"`
	Migratable             int             `json:"migratable"`
	Migrated               int             `json:"migrated"`

	DaemonReachable bool                 `json:"daemon_reachable"`
	Verification    *aslSyncVerification `json:"verification,omitempty"`
}

// aslSyncVerification is what the daemon itself reports after the restart. It
// is the only evidence that counts: a config file says what should happen, and
// this says what did.
type aslSyncVerification struct {
	Ready            bool   `json:"daemon_ready"`
	Available        bool   `json:"bridge_available"`
	MutationsEnabled bool   `json:"handshake_complete"`
	Executable       string `json:"daemon_executable,omitempty"`
	ExecutableSource string `json:"daemon_executable_source,omitempty"`
	ContractSetID    string `json:"daemon_contract_set_id,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// Seams. Filesystem, daemon, and subprocess work all go through these so the
// whole sequence — including rollback — is exercisable without a real ASL, a
// real daemon, or a real config.
var (
	aslSyncConfigPath = config.DefaultConfigPath
	aslSyncResolve    = aslbridge.ResolveCommand
	aslSyncProbe      = runASLPairingProbe
	newASLSyncDaemon  = func() aslSyncDaemon { return ipc.NewClient() }
	aslSyncExecutable = os.Executable
	aslSyncSleep      = time.Sleep
	aslSyncNow        = time.Now
)

type aslSyncDaemon interface {
	Health() ipc.Health
	RestartDetached(amxBin string) error
}

var (
	configASLSyncCommand string
	configASLSyncCheck   bool
	configASLSyncJSON    bool
)

var configASLSyncCmd = &cobra.Command{
	Use:   "asl-sync",
	Short: "Pair the daemon with agent-session-loop and persist an absolute path",
	Long: "Resolve the agent-session-loop executable, verify it speaks this build's " +
		"contract set, migrate compatible inactive run state, persist the absolute " +
		"path and state root into config.toml, restart the daemon, and verify the " +
		"live bridge. Any failure after the write restores the previous configuration.\n\n" +
		"--check performs the entire preflight and writes nothing.",
	Args: cobra.NoArgs,
	// Every failure this command produces is a structured refusal with its own
	// remediation. Printing the flag list under one would bury the line the
	// operator actually needs.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runConfigASLSync(cmd)
	},
}

func init() {
	configASLSyncCmd.Flags().StringVar(&configASLSyncCommand, "command", "",
		"executable to pair with; defaults to defaults.asl.command, then `asl`")
	configASLSyncCmd.Flags().BoolVar(&configASLSyncCheck, "check", false,
		"run the full preflight and report what would change, without writing")
	configASLSyncCmd.Flags().BoolVar(&configASLSyncJSON, "json", false, "emit JSON")
	configCmd.AddCommand(configASLSyncCmd)
}

func runConfigASLSync(cmd *cobra.Command) error {
	jsonOut := configASLSyncJSON
	report := aslSyncReport{Mode: "apply", ConfigPath: aslSyncConfigPath()}
	if configASLSyncCheck {
		report.Mode = "check"
	}

	// Preflight step 1: which executable. A config that already names one is
	// honoured over the search, because re-resolving would silently repoint a
	// deliberate split install.
	cfg, cfgErr := config.Load(report.ConfigPath)
	candidate := strings.TrimSpace(configASLSyncCommand)
	if candidate == "" && cfgErr == nil {
		candidate = strings.TrimSpace(cfg.Defaults.ASL.Command)
		report.PreviousCommand = candidate
	}
	if candidate == "" {
		candidate = "asl"
	}
	report.Command = candidate

	resolution, err := aslSyncResolve(candidate)
	if err != nil {
		return cli.CommandFailure(cmd, jsonOut, codeASLNotFound, fmt.Errorf(
			"%w\nremediation: install agent-session-loop, or pass --command with the path to its executable", err))
	}
	report.Executable = resolution.Executable
	report.ExecutableSource = resolution.Source

	// Preflight steps 2-5: contract metadata, state root, active incompatible
	// runs, migration readiness. All four come from one survey, because they are
	// four readings of the same corpus and taking them separately would let it
	// move between them.
	survey, err := aslSyncProbe(context.Background(), resolution.Executable, false, aslSyncProbeTimeout)
	if err != nil {
		return cli.CommandFailure(cmd, jsonOut, codeASLProbeFailed, fmt.Errorf(
			"%s could not be surveyed: %w\nremediation: run `%s agent-mux pairing` directly to see why",
			resolution.Executable, err, resolution.Executable))
	}
	applySurveyToReport(&report, survey)

	if err := checkASLContract(survey); err != nil {
		return cli.CommandFailure(cmd, jsonOut, codeASLContractMismatch, err)
	}
	if !survey.StateRootPresent && survey.Reason == "state-root-error" {
		return cli.CommandFailure(cmd, jsonOut, codeASLStateRoot, fmt.Errorf(
			"the agent-session-loop state root %s is unusable: %s\nremediation: fix its permissions, or point ASL at a readable root, then re-run",
			survey.StateRoot, survey.Error))
	}
	// Refusal is whole-root, not per-plan. A live run holds its state open, and
	// migrating the corpus around it would leave the one process that is
	// actually writing on the old schema — so nothing is migrated and nothing
	// is stopped. Stopping a run to complete a pairing is never this command's
	// decision.
	if len(survey.ActiveIncompatibleRuns) > 0 {
		return cli.CommandFailure(cmd, jsonOut, codeASLActiveRuns, errors.New(activeRunRefusal(survey)))
	}

	// Preflight step 6: the daemon. Reachability is recorded rather than
	// required — pairing a machine whose daemon is down is legitimate, and the
	// restart below starts one.
	client := newASLSyncDaemon()
	health := client.Health()
	report.DaemonReachable = health.Alive

	if configASLSyncCheck {
		return emitASLSyncReport(cmd, jsonOut, report, nil)
	}

	// Past here the command mutates. Everything it changes is captured first.
	previous, previousExisted, err := readConfigBytes(report.ConfigPath)
	if err != nil {
		return cli.CommandFailure(cmd, jsonOut, codeASLConfigWrite, err)
	}
	updated, err := config.ApplyASLPairing(previous, config.ASLPairing{
		Command:   resolution.Executable,
		StateRoot: survey.StateRoot,
		Required:  true,
	})
	if err != nil {
		var ambiguous *config.ErrASLTableAmbiguous
		if errors.As(err, &ambiguous) {
			return cli.CommandFailure(cmd, jsonOut, codeASLConfigAmbiguous, fmt.Errorf(
				"%w\nremediation: %s", err, ambiguous.Remediation))
		}
		return cli.CommandFailure(cmd, jsonOut, codeASLConfigWrite, err)
	}

	if report.Migratable > 0 {
		migrated, err := aslSyncProbe(context.Background(), resolution.Executable, true, aslSyncMigrateTimeout)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOut, codeASLMigrationFailed, fmt.Errorf(
				"migrating agent-session-loop run state failed: %w\nremediation: config.toml is unchanged; re-run once the state root is writable", err))
		}
		if !migrated.OK {
			return cli.CommandFailure(cmd, jsonOut, codeASLMigrationFailed, fmt.Errorf(
				"migrating agent-session-loop run state failed (%s): %s\nremediation: config.toml is unchanged",
				migrated.Reason, migrated.Error))
		}
		// The migration pass is also a fresh survey, so a run that started
		// during it is caught here rather than after the config is replaced.
		if len(migrated.ActiveIncompatibleRuns) > 0 {
			return cli.CommandFailure(cmd, jsonOut, codeASLActiveRuns, errors.New(activeRunRefusal(migrated)))
		}
		applySurveyToReport(&report, migrated)
		report.Migrated = len(migrated.Migrations)
	}

	if err := atomicfile.Replace(report.ConfigPath, updated, 0o644); err != nil {
		return cli.CommandFailure(cmd, jsonOut, codeASLConfigWrite, fmt.Errorf(
			"replacing %s: %w", report.ConfigPath, err))
	}
	report.Applied = true

	// restore puts the operator back exactly where they were: the previous
	// bytes, and a daemon running on them. A rollback that skipped the restart
	// would leave a live daemon holding a configuration that no longer exists
	// on disk, which is worse than the failure it is recovering from.
	restore := func() {
		if previousExisted {
			_ = atomicfile.Replace(report.ConfigPath, previous, 0o644)
		} else {
			_ = os.Remove(report.ConfigPath)
		}
		report.Applied = false
		report.RolledBack = true
		if amxBin, err := aslSyncExecutable(); err == nil {
			_ = client.RestartDetached(amxBin)
		}
	}

	amxBin, err := aslSyncExecutable()
	if err != nil {
		amxBin = "amx"
	}
	if err := client.RestartDetached(amxBin); err != nil {
		restore()
		return emitASLSyncReport(cmd, jsonOut, report, &aslSyncFailure{
			code: codeASLDaemonRestart,
			err:  fmt.Errorf("restarting the daemon failed: %w\nremediation: the previous configuration was restored; run `amx daemon restart` and re-run", err),
		})
	}

	verification, err := waitForPairedDaemon(client, resolution.Executable)
	report.Verification = &verification
	if err != nil {
		restore()
		return emitASLSyncReport(cmd, jsonOut, report, &aslSyncFailure{
			code: codeASLVerifyFailed,
			err:  fmt.Errorf("%w\nremediation: the previous configuration was restored and the daemon restarted onto it; run `amx doctor --strict` for the current state", err),
		})
	}
	return emitASLSyncReport(cmd, jsonOut, report, nil)
}
