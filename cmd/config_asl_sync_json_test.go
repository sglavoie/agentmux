package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon"
)

// The JSON surface of `amx config asl-sync`: the codes a failure carries, the
// report that accompanies a rollback, and what a --check report says an apply
// would change.

// Every refusal carries a distinct structured code, because each one has a
// distinct remediation and a script has to be able to tell them apart.
func TestJSONFailuresCarryDistinctCodes(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		prepare func(t *testing.T, fixture *aslSyncFixture)
		survey  aslPairingReport
	}{
		{
			name:   "not found",
			code:   codeASLNotFound,
			survey: healthySurvey(),
			prepare: func(_ *testing.T, _ *aslSyncFixture) {
				aslSyncResolve = func(string) (aslbridge.Resolution, error) {
					return aslbridge.Resolution{}, errors.New(`exec: "asl": executable file not found in $PATH`)
				}
			},
		},
		{
			name:   "probe failed",
			code:   codeASLProbeFailed,
			survey: healthySurvey(),
			prepare: func(_ *testing.T, _ *aslSyncFixture) {
				aslSyncProbe = func(context.Context, string, bool, time.Duration) (aslPairingReport, error) {
					return aslPairingReport{}, errors.New("signal: killed")
				}
			},
		},
		{
			name: "contract mismatch",
			code: codeASLContractMismatch,
			survey: func() aslPairingReport {
				s := healthySurvey()
				s.ContractSetID = "asl-amx-1.0000000000000000"
				return s
			}(),
		},
		{
			name: "state root",
			code: codeASLStateRoot,
			survey: func() aslPairingReport {
				s := healthySurvey()
				s.OK, s.Reason, s.StateRootPresent = false, "state-root-error", false
				s.Error = "permission denied"
				return s
			}(),
		},
		{
			name: "active runs",
			code: codeASLActiveRuns,
			survey: func() aslPairingReport {
				s := healthySurvey()
				s.ActiveIncompatibleRuns = []aslPairingRun{{PID: 5, RunID: "r"}}
				return s
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setUpASLSync(t, "[defaults]\n", tc.survey)
			configASLSyncJSON = true
			if tc.prepare != nil {
				tc.prepare(t, fixture)
			}
			if err := runConfigASLSync(configASLSyncCmd); err == nil {
				t.Fatal("runConfigASLSync() = nil, want a failure")
			}
			var envelope struct {
				SchemaVersion int `json:"schema_version"`
				Error         struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(fixture.out.Bytes(), &envelope); err != nil {
				t.Fatalf("decode %q: %v", fixture.out, err)
			}
			if envelope.SchemaVersion != JSONSchemaVersion {
				t.Errorf("schema_version = %d, want %d", envelope.SchemaVersion, JSONSchemaVersion)
			}
			if envelope.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, tc.code)
			}
			if !strings.Contains(envelope.Error.Message, "remediation") {
				t.Errorf("message carries no remediation: %q", envelope.Error.Message)
			}
		})
	}
}

// A rollback is a failure whose report is still the most useful thing an
// operator can read, so JSON emits both.
func TestAJSONRollbackEmitsTheReportAlongsideTheFailure(t *testing.T) {
	fixture := setUpASLSync(t, "[defaults]\n", healthySurvey())
	configASLSyncJSON = true
	fixture.daemon.afterRestart = &daemon.Health{Alive: true, Ready: true}

	if err := runConfigASLSync(configASLSyncCmd); err == nil {
		t.Fatal("runConfigASLSync() = nil, want a verification failure")
	}
	var envelope struct {
		SchemaVersion int           `json:"schema_version"`
		Data          aslSyncReport `json:"data"`
	}
	if err := json.Unmarshal(fixture.out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", fixture.out, err)
	}
	if !envelope.Data.RolledBack || envelope.Data.Applied {
		t.Errorf("report = applied %t rolled_back %t; want a rollback",
			envelope.Data.Applied, envelope.Data.RolledBack)
	}
	if envelope.Data.Verification == nil {
		t.Fatal("the report carries no verification block")
	}
	if envelope.Data.Verification.Available {
		t.Error("verification claims an available bridge after a rollback")
	}
}

// The check report is the preflight verbatim, so an operator can read what an
// apply would do before consenting to it.
func TestTheCheckJSONReportNamesWhatAnApplyWouldChange(t *testing.T) {
	survey := healthySurvey()
	survey.Plans = map[string]int{"total": 9, "compatible": 2, "migratable": 7}
	fixture := setUpASLSync(t, "[defaults]\n", survey)
	configASLSyncJSON, configASLSyncCheck = true, true

	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	var envelope struct {
		Data aslSyncReport `json:"data"`
	}
	if err := json.Unmarshal(fixture.out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", fixture.out, err)
	}
	report := envelope.Data
	if report.Mode != "check" || report.Applied {
		t.Errorf("report = mode %q applied %t", report.Mode, report.Applied)
	}
	if report.Migratable != 7 {
		t.Errorf("migratable = %d, want 7", report.Migratable)
	}
	if report.Migrated != 0 {
		t.Errorf("migrated = %d; a check migrates nothing", report.Migrated)
	}
	if report.Executable != "/opt/asl/bin/asl" || report.StateRoot != "/srv/asl-state" {
		t.Errorf("report does not carry the resolved identity: %+v", report)
	}
}

// Migration runs only when there is something to migrate, and what it migrated
// is reported.
func TestAMigratableCorpusIsMigratedAndCounted(t *testing.T) {
	survey := healthySurvey()
	survey.Plans = map[string]int{"total": 3, "migratable": 2, "compatible": 1}
	migrated := survey
	migrated.Migrated = true
	migrated.Migrations = []aslPairingPlan{
		{PlanDir: "/srv/plans/a", Classification: "migratable"},
		{PlanDir: "/srv/plans/b", Classification: "migratable"},
	}
	migrated.Plans = map[string]int{"total": 3, "compatible": 3}

	fixture := setUpASLSync(t, "[defaults]\n", survey)
	aslSyncProbe = func(_ context.Context, _ string, migrate bool, _ time.Duration) (aslPairingReport, error) {
		fixture.probes = append(fixture.probes, migrate)
		if migrate {
			return migrated, nil
		}
		return survey, nil
	}
	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if len(fixture.probes) != 2 || !fixture.probes[1] {
		t.Fatalf("probes = %v, want a survey then a migration", fixture.probes)
	}
	if !strings.Contains(fixture.out.String(), "migrated    2 inactive run(s)") {
		t.Errorf("output does not report the migration:\n%s", fixture.out)
	}
}

// Nothing to migrate means no migrating pass at all: a write is not performed
// speculatively over a corpus that does not need one.
func TestACompatibleCorpusIsNeverMigrated(t *testing.T) {
	fixture := setUpASLSync(t, "[defaults]\n", healthySurvey())
	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if len(fixture.probes) != 1 || fixture.probes[0] {
		t.Errorf("probes = %v, want a single non-migrating survey", fixture.probes)
	}
}

// Pairing a machine whose daemon is down is legitimate — the restart starts one
// — so reachability is recorded rather than required.
func TestPairingProceedsWithADeadDaemon(t *testing.T) {
	fixture := setUpASLSync(t, "[defaults]\n", healthySurvey())
	fixture.daemon.health = daemon.Health{}

	if err := runConfigASLSync(configASLSyncCmd); err != nil {
		t.Fatalf("runConfigASLSync() = %v", err)
	}
	if !strings.Contains(fixture.configBytes(t), `command = "/opt/asl/bin/asl"`) {
		t.Errorf("the pairing did not write:\n%s", fixture.configBytes(t))
	}
}
