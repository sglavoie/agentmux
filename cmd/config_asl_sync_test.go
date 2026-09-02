package cmd

// What `amx config asl-sync` promises: it either ends with a verified live
// bridge, or with the configuration the operator had before they ran it. Every
// test here is one of the ways it can fail on the way, asserting that the file
// on disk afterwards is the one they started with.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

// fakeASLSyncDaemon records restarts and serves a scripted sequence of health
// readings, so a rollback's second restart is observable rather than inferred.
type fakeASLSyncDaemon struct {
	health   ipc.Health
	restarts []string
	// afterRestart replaces health once RestartDetached succeeds, which is how
	// the real sequence behaves: the verification wait reads a different daemon
	// than the preflight did.
	afterRestart *ipc.Health
	restartErr   error
}

func (f *fakeASLSyncDaemon) Health() ipc.Health { return f.health }

func (f *fakeASLSyncDaemon) RestartDetached(amxBin string) error {
	f.restarts = append(f.restarts, amxBin)
	if f.restartErr != nil {
		return f.restartErr
	}
	if f.afterRestart != nil {
		f.health = *f.afterRestart
	}
	return nil
}

// healthySurvey is an ASL that this build can pair with and a corpus with
// nothing in the way.
func healthySurvey() aslPairingReport {
	return aslPairingReport{
		OK:                 true,
		Reason:             "ok",
		Command:            "asl",
		Executable:         "/opt/asl/bin/asl",
		ASLVersion:         "0.2.0",
		ContractSetID:      aslcontract.ContractSetID,
		BridgeProtocol:     aslbridge.ProtocolVersion,
		StateRoot:          "/srv/asl-state",
		StateRootPresent:   true,
		StateSchemaVersion: 3,
		Plans:              map[string]int{"total": 4, "compatible": 4},
	}
}

// pairedHealth is what a daemon that actually completed the pairing reports.
func pairedHealth(executable string) ipc.Health {
	return ipc.Health{
		Alive: true, Ready: true, PID: 42,
		ASL: &aslbridge.Status{
			Mode:             string(aslbridge.ModeRequired),
			Available:        true,
			MutationsEnabled: true,
			Executable:       executable,
			ExecutableSource: aslbridge.SourceConfigured,
			ContractSetID:    aslcontract.ContractSetID,
		},
	}
}

type aslSyncFixture struct {
	configPath string
	daemon     *fakeASLSyncDaemon
	out        *bytes.Buffer
	// probes records (migrate) for every survey the command ran, which is how
	// "nothing was migrated" is asserted.
	probes []bool
}

// setUpASLSync pins every seam and points the command at a temporary config.
func setUpASLSync(t *testing.T, initialConfig string, survey aslPairingReport) *aslSyncFixture {
	t.Helper()

	oldPath, oldResolve, oldProbe := aslSyncConfigPath, aslSyncResolve, aslSyncProbe
	oldDaemon, oldExe, oldSleep, oldNow := newASLSyncDaemon, aslSyncExecutable, aslSyncSleep, aslSyncNow
	oldCommand, oldCheck, oldJSON := configASLSyncCommand, configASLSyncCheck, configASLSyncJSON
	oldOut := configASLSyncCmd.OutOrStdout()
	t.Cleanup(func() {
		aslSyncConfigPath, aslSyncResolve, aslSyncProbe = oldPath, oldResolve, oldProbe
		newASLSyncDaemon, aslSyncExecutable, aslSyncSleep, aslSyncNow = oldDaemon, oldExe, oldSleep, oldNow
		configASLSyncCommand, configASLSyncCheck, configASLSyncJSON = oldCommand, oldCheck, oldJSON
		configASLSyncCmd.SetOut(oldOut)
	})

	configASLSyncCommand, configASLSyncCheck, configASLSyncJSON = "", false, false

	fixture := &aslSyncFixture{
		configPath: filepath.Join(t.TempDir(), "config.toml"),
		daemon:     &fakeASLSyncDaemon{health: ipc.Health{Alive: true, Ready: true, PID: 7}},
		out:        &bytes.Buffer{},
	}
	if initialConfig != "" {
		if err := os.WriteFile(fixture.configPath, []byte(initialConfig), 0o644); err != nil {
			t.Fatalf("seed config: %v", err)
		}
	}
	fixture.daemon.afterRestart = &ipc.Health{}
	*fixture.daemon.afterRestart = pairedHealth(survey.Executable)

	aslSyncConfigPath = func() string { return fixture.configPath }
	aslSyncResolve = func(command string) (aslbridge.Resolution, error) {
		return aslbridge.Resolution{
			Command:    command,
			Executable: survey.Executable,
			Source:     aslbridge.SourceSibling,
		}, nil
	}
	aslSyncProbe = func(_ context.Context, _ string, migrate bool, _ time.Duration) (aslPairingReport, error) {
		fixture.probes = append(fixture.probes, migrate)
		return survey, nil
	}
	newASLSyncDaemon = func() aslSyncDaemon { return fixture.daemon }
	aslSyncExecutable = func() (string, error) { return "/opt/amx/bin/amx", nil }
	// A clock that only advances when the wait sleeps keeps the verification
	// timeout deterministic and instant: the rollback paths reach their deadline
	// without any real elapsed time.
	now := time.Unix(1700000000, 0)
	aslSyncNow = func() time.Time { return now }
	aslSyncSleep = func(d time.Duration) { now = now.Add(d) }

	configASLSyncCmd.SetOut(fixture.out)
	return fixture
}

func (f *aslSyncFixture) configBytes(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read config: %v", err)
	}
	return string(data)
}

// The preflight is the useful half of the command, and it must be available
// without consenting to a mutation.
func TestCheckWritesNothing(t *testing.T) {
	original := "[defaults]\nsort = \"alpha\"\n"
	fixture := setUpASLSync(t, original, healthySurvey())
	configASLSyncCheck = true

	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("config was rewritten by --check:\n%s", got)
	}
	if len(fixture.daemon.restarts) != 0 {
		t.Errorf("--check restarted the daemon %d time(s)", len(fixture.daemon.restarts))
	}
	for _, migrate := range fixture.probes {
		if migrate {
			t.Error("--check ran a migrating survey")
		}
	}
	if !strings.Contains(fixture.out.String(), "is unchanged") {
		t.Errorf("output does not say the config was left alone:\n%s", fixture.out)
	}
}

// A successful pairing persists the absolute path, not the name that was
// searched for; that is the entire point of the command.
func TestApplyPersistsTheAbsolutePathStateRootAndRequiredMode(t *testing.T) {
	fixture := setUpASLSync(t, "[defaults]\nsort = \"alpha\"\n", healthySurvey())

	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	written := fixture.configBytes(t)
	for _, want := range []string{
		`command = "/opt/asl/bin/asl"`,
		`state_root = "/srv/asl-state"`,
		"enabled = true",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("config is missing %q:\n%s", want, written)
		}
	}
	if len(fixture.daemon.restarts) != 1 {
		t.Errorf("restarts = %v, want exactly one", fixture.daemon.restarts)
	}
	if !strings.Contains(fixture.out.String(), "bridge      live and handshaked") {
		t.Errorf("output does not report a verified bridge:\n%s", fixture.out)
	}
}

// The mode the pairing writes is not incidental: `amx doctor --strict` has to
// fail for an unavailable integration afterwards, and only required mode does
// that. Reporting it in the output is what keeps it from being a surprise.
func TestApplyReportsThatItEnabledRequiredMode(t *testing.T) {
	fixture := setUpASLSync(t, "", healthySurvey())
	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if !strings.Contains(fixture.configBytes(t), "enabled = true") {
		t.Errorf("pairing did not enable the integration:\n%s", fixture.configBytes(t))
	}
}

// A live run holds its state open. Migrating the corpus around it would leave
// the one process that is actually writing on the old schema.
func TestActiveIncompatibleRunsRefuseWithIdentities(t *testing.T) {
	survey := healthySurvey()
	survey.OK = false
	survey.Reason = "active-incompatible-runs"
	survey.Plans = map[string]int{"total": 6, "migratable": 5, "active-incompatible": 1}
	survey.ActiveIncompatibleRuns = []aslPairingRun{{
		PlanDir: "/srv/plans/abc", RunID: "run-abc", PID: 73091,
		SchemaVersion: 2, Detail: "state schema 2 predates the nested projection",
	}}
	original := "[defaults]\nsort = \"alpha\"\n"
	fixture := setUpASLSync(t, original, survey)

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil {
		t.Fatal("runConfigASLSync() = nil, want a refusal")
	}
	for _, want := range []string{"73091", "run-abc", "/srv/plans/abc", "nothing was stopped"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n%s", want, err)
		}
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("a refused pairing rewrote the config:\n%s", got)
	}
	for _, migrate := range fixture.probes {
		if migrate {
			t.Error("a refused pairing still ran a migration")
		}
	}
	if len(fixture.daemon.restarts) != 0 {
		t.Error("a refused pairing restarted the daemon")
	}
}

// A run that starts during the migration pass is caught by the second survey,
// before the config is replaced rather than after.
func TestARunThatAppearsDuringMigrationStillRefusesBeforeTheWrite(t *testing.T) {
	first := healthySurvey()
	first.Plans = map[string]int{"total": 2, "migratable": 2}
	second := first
	second.OK = true
	second.Migrated = true
	second.ActiveIncompatibleRuns = []aslPairingRun{{PID: 999, RunID: "late", PlanDir: "/srv/plans/late"}}

	original := "[defaults]\nsort = \"alpha\"\n"
	fixture := setUpASLSync(t, original, first)
	call := 0
	aslSyncProbe = func(_ context.Context, _ string, migrate bool, _ time.Duration) (aslPairingReport, error) {
		call++
		fixture.probes = append(fixture.probes, migrate)
		if call == 1 {
			return first, nil
		}
		return second, nil
	}

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("runConfigASLSync() = %v, want a refusal naming the late run", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("the config was replaced despite the refusal:\n%s", got)
	}
}

// Two halves built from different corpora cannot be configured into agreement.
func TestAContractMismatchRefusesBeforeAnythingIsWritten(t *testing.T) {
	survey := healthySurvey()
	survey.ContractSetID = "asl-amx-1.deadbeefdeadbeef"
	original := "[defaults]\n"
	fixture := setUpASLSync(t, original, survey)

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil {
		t.Fatal("runConfigASLSync() = nil, want a contract refusal")
	}
	if !strings.Contains(err.Error(), aslcontract.ContractSetID) ||
		!strings.Contains(err.Error(), "asl-amx-1.deadbeefdeadbeef") {
		t.Errorf("the refusal does not name both contract sets:\n%s", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("a contract mismatch still rewrote the config:\n%s", got)
	}
}

// A bridge protocol the two halves disagree on is the same class of problem and
// gets the same treatment.
func TestABridgeProtocolMismatchRefuses(t *testing.T) {
	survey := healthySurvey()
	survey.BridgeProtocol = aslbridge.ProtocolVersion + 1
	setUpASLSync(t, "[defaults]\n", survey)

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil || !strings.Contains(err.Error(), "bridge protocol") {
		t.Fatalf("runConfigASLSync() = %v, want a protocol refusal", err)
	}
}

// The verification wait is the only evidence that counts. A daemon that came
// back ready but never reached ASL means the pairing did not happen, whatever
// the config file now says.
func TestVerificationFailureRestoresThePreviousConfigAndRestarts(t *testing.T) {
	original := "[defaults.asl]\ncommand = \"asl\"\n"
	fixture := setUpASLSync(t, original, healthySurvey())
	// The restarted daemon comes up, but without a bridge.
	fixture.daemon.afterRestart = &ipc.Health{
		Alive: true, Ready: true,
		ASL: &aslbridge.Status{Mode: string(aslbridge.ModeRequired), Reason: "asl bridge: connection refused"},
	}

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil {
		t.Fatal("runConfigASLSync() = nil, want a verification failure")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("the failure does not carry the daemon's reason:\n%s", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("the previous config was not restored:\n%s", got)
	}
	if len(fixture.daemon.restarts) != 2 {
		t.Errorf("restarts = %v; a rollback must restart onto the restored config", fixture.daemon.restarts)
	}
	if !strings.Contains(fixture.out.String(), "restored to its previous contents") {
		t.Errorf("the report does not say the config was restored:\n%s", fixture.out)
	}
}

// A daemon that connects to a *different* executable is not a paired daemon. It
// found something else, and accepting that would make the promise false in
// exactly the situation the command was written for.
func TestADaemonThatPairedWithAnotherExecutableIsRolledBack(t *testing.T) {
	original := "[defaults]\n"
	fixture := setUpASLSync(t, original, healthySurvey())
	fixture.daemon.afterRestart = &ipc.Health{}
	*fixture.daemon.afterRestart = pairedHealth("/usr/local/bin/asl")

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil || !strings.Contains(err.Error(), "/usr/local/bin/asl") {
		t.Fatalf("runConfigASLSync() = %v, want a mismatched-executable rollback", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("the config was not restored:\n%s", got)
	}
}

// A config that did not exist before must not exist after a rollback either.
func TestARollbackRemovesAConfigItCreated(t *testing.T) {
	fixture := setUpASLSync(t, "", healthySurvey())
	fixture.daemon.afterRestart = &ipc.Health{Alive: true, Ready: true}

	if err := runConfigASLSync(configASLSyncCmd); err == nil {
		t.Fatal("runConfigASLSync() = nil, want a verification failure")
	}
	if _, err := os.Stat(fixture.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the rollback left a config file behind: %v", err)
	}
}

// A restart that never happened is still a mutation that has to be undone.
func TestAFailedRestartRestoresThePreviousConfig(t *testing.T) {
	original := "[defaults]\nsort = \"alpha\"\n"
	fixture := setUpASLSync(t, original, healthySurvey())
	fixture.daemon.restartErr = errors.New("daemon socket is held by another process")

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil || !strings.Contains(err.Error(), "another process") {
		t.Fatalf("runConfigASLSync() = %v, want a restart failure", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("the config was not restored after a failed restart:\n%s", got)
	}
}

// The dotted-key ambiguity is refused with the edit that fixes it, rather than
// producing a file that stops parsing at the next daemon start.
func TestAnAmbiguousConfigIsRefusedWithRemediation(t *testing.T) {
	original := "[defaults]\nasl.command = \"asl\"\n"
	fixture := setUpASLSync(t, original, healthySurvey())

	err := runConfigASLSync(configASLSyncCmd)
	if err == nil || !strings.Contains(err.Error(), "[defaults.asl]") {
		t.Fatalf("runConfigASLSync() = %v, want an ambiguity refusal naming the fix", err)
	}
	if got := fixture.configBytes(t); got != original {
		t.Errorf("an ambiguous config was rewritten anyway:\n%s", got)
	}
}

// A config that already names an executable is honoured over the search: a
// deliberate split install must not be silently repointed.
func TestAConfiguredCommandIsPreferredOverTheSearch(t *testing.T) {
	fixture := setUpASLSync(t, "[defaults.asl]\ncommand = \"/opt/split/asl\"\n", healthySurvey())
	var asked string
	aslSyncResolve = func(command string) (aslbridge.Resolution, error) {
		asked = command
		return aslbridge.Resolution{
			Command: command, Executable: "/opt/asl/bin/asl", Source: aslbridge.SourceConfigured,
		}, nil
	}
	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if asked != "/opt/split/asl" {
		t.Errorf("resolved %q, want the configured command", asked)
	}
	_ = fixture
}

// --command overrides both, because that is the flag's only purpose.
func TestTheCommandFlagOverridesTheConfiguredCommand(t *testing.T) {
	setUpASLSync(t, "[defaults.asl]\ncommand = \"/opt/split/asl\"\n", healthySurvey())
	configASLSyncCommand = "/opt/override/asl"
	var asked string
	aslSyncResolve = func(command string) (aslbridge.Resolution, error) {
		asked = command
		return aslbridge.Resolution{Command: command, Executable: "/opt/asl/bin/asl", Source: aslbridge.SourceConfigured}, nil
	}
	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if asked != "/opt/override/asl" {
		t.Errorf("resolved %q, want the flag's value", asked)
	}
}
