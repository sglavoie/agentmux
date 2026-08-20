package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon"
)

func aslDoctorHealth(status *aslbridge.Status) daemon.Health {
	build := doctorTestBuild
	return daemon.Health{
		Alive: true, Ready: true, PID: 1, Uptime: time.Minute, Build: &build, ASL: status,
	}
}

func TestDoctorStrictStaysCleanWhenAutoModeASLIsAbsent(t *testing.T) {
	setUpDoctorFixture(t, aslDoctorHealth(&aslbridge.Status{
		Mode: string(aslbridge.ModeAuto), Command: "asl", Reason: "bridge exited",
	}), daemon.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("an absent optional ASL must not fail strict doctor: %v", err)
	}
	if !strings.Contains(out.String(), "asl: unavailable (mode auto via asl; bridge exited)") {
		t.Errorf("doctor output missing the asl line:\n%s", out.String())
	}
}

func TestDoctorStrictFailsWhenRequiredASLIsUnavailable(t *testing.T) {
	setUpDoctorFixture(t, aslDoctorHealth(&aslbridge.Status{
		Mode: string(aslbridge.ModeRequired), Command: "asl", Reason: "bridge exited", RestartCount: 3,
	}), daemon.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	err := runDoctor(doctorCmd, nil)

	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != exitHealthFailure {
		t.Fatalf("err = %+v; want strict health failure", err)
	}
	for _, want := range []string{
		"restarts 3",
		"[asl_unavailable] asl integration is required but unavailable: bridge exited",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorReportsStaleASLAsReadOnly(t *testing.T) {
	age := int64(4)
	setUpDoctorFixture(t, aslDoctorHealth(&aslbridge.Status{
		Mode:               string(aslbridge.ModeAuto),
		Command:            "asl",
		Executable:         "/usr/local/bin/asl",
		Available:          true,
		Version:            "1.2.3",
		Protocol:           1,
		SnapshotAgeSeconds: &age,
		SnapshotStale:      true,
	}), daemon.EventsExport{})
	doctorStrict = false

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"asl: read-only (mode auto at /usr/local/bin/asl; asl 1.2.3 protocol 1; snapshot 4s old (stale))",
		"[asl_read_only]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorJSONCarriesASLCapability(t *testing.T) {
	setUpDoctorFixture(t, aslDoctorHealth(&aslbridge.Status{
		Mode: string(aslbridge.ModeRequired), Command: "asl", Available: true, MutationsEnabled: true,
	}), daemon.EventsExport{})
	doctorJSON = true
	doctorStrict = false

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	var report struct {
		Daemon struct {
			ASL *aslbridge.Status `json:"asl"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decoding doctor JSON: %v\n%s", err, out.String())
	}
	if report.Daemon.ASL == nil || !report.Daemon.ASL.MutationsEnabled {
		t.Fatalf("daemon.asl = %+v", report.Daemon.ASL)
	}
}

// Diagnosing the integration means answering "is it there, which build, has it
// been crashing, how old is what I am looking at, and are actions landing".
// Every one of those is a counter or an identity; none of them can be a plan
// path, a question, or a reply, and this test pins both halves at once.
func TestDoctorASLLifecycleFactsCarryNoPlanContent(t *testing.T) {
	age := int64(3)
	setUpDoctorFixture(t, aslDoctorHealth(&aslbridge.Status{
		Mode:               string(aslbridge.ModeAuto),
		Command:            "asl",
		Executable:         "/usr/local/bin/asl",
		Available:          true,
		MutationsEnabled:   true,
		Protocol:           1,
		Version:            "1.2.3",
		SnapshotAgeSeconds: &age,
		SnapshotGeneration: 9,
		RestartCount:       2,
		Reason:             "bridge refused: " + aslbridge.CodeStaleActionToken,
		Metrics: &aslbridge.Metrics{
			Restarts: 2,
			Refusals: 1,
			RequestsByMethod: map[string]uint64{
				aslbridge.MethodRunsSnapshot:  9,
				aslbridge.MethodQuestionReply: 2,
			},
			OperationsByStatus: map[string]uint64{
				aslbridge.OperationAccepted: 2,
				aslbridge.OperationConsumed: 1,
				aslbridge.OperationRejected: 1,
			},
		},
	}), daemon.EventsExport{})
	doctorJSON = false
	doctorStrict = false

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"asl 1.2.3 protocol 1",
		"snapshot 3s old",
		"restarts 2",
		"actions 2 accepted/1 consumed/1 rejected",
		"bridge refused: " + aslbridge.CodeStaleActionToken,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor text missing %q:\n%s", want, text)
		}
	}

	doctorJSON = true
	out.Reset()
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("runDoctor --json: %v", err)
	}
	var report struct {
		Daemon struct {
			ASL *aslbridge.Status `json:"asl"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decoding doctor JSON: %v\n%s", err, out.String())
	}
	if report.Daemon.ASL == nil || report.Daemon.ASL.Metrics == nil {
		t.Fatalf("daemon.asl = %+v", report.Daemon.ASL)
	}
	if got := report.Daemon.ASL.Metrics.OperationsByStatus[aslbridge.OperationRejected]; got != 1 {
		t.Errorf("rejected operations = %d, want 1", got)
	}
	// The two maps are the only place the block holds caller-supplied keys, so
	// they are the only place a plan path or a question id could hide. Both are
	// closed vocabularies defined in aslbridge.
	for method := range report.Daemon.ASL.Metrics.RequestsByMethod {
		if !slices.Contains(report.Daemon.ASL.Capabilities, method) &&
			!slices.Contains(aslCapabilityNames, method) {
			t.Errorf("metrics name an undeclared method %q", method)
		}
	}
	for status := range report.Daemon.ASL.Metrics.OperationsByStatus {
		if !slices.Contains([]string{
			aslbridge.OperationAccepted, aslbridge.OperationConsumed, aslbridge.OperationRejected,
			aslbridge.OperationExpired, aslbridge.OperationSuperseded, "other",
		}, status) {
			t.Errorf("metrics name an undeclared operation status %q", status)
		}
	}
}

// The debug bundle reaches ASL only through the doctor snapshot, so a bundle
// carries exactly the block doctor publishes. Pinning that here is what stops a
// future ASL field from arriving in a shared archive without review.
func TestBundleDoctorSnapshotCarriesTheASLCapabilityBlock(t *testing.T) {
	setUpDoctorFixture(t, aslDoctorHealth(&aslbridge.Status{
		Mode: string(aslbridge.ModeAuto), Command: "asl", Available: true,
		MutationsEnabled: true, Protocol: 1, Version: "1.2.3",
		Metrics: &aslbridge.Metrics{OperationsByStatus: map[string]uint64{
			aslbridge.OperationAccepted: 1,
		}},
	}), daemon.EventsExport{})

	snapshot := collectDoctorSnapshotForBundle(&cobra.Command{})
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal bundle doctor snapshot: %v", err)
	}
	var report struct {
		Daemon struct {
			ASL *aslbridge.Status `json:"asl"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatalf("decode bundle doctor snapshot: %v\n%s", err, encoded)
	}
	if report.Daemon.ASL == nil {
		t.Fatalf("bundle doctor snapshot has no asl block: %s", encoded)
	}
	if report.Daemon.ASL.Version != "1.2.3" || report.Daemon.ASL.Metrics == nil {
		t.Errorf("bundle asl block = %+v", report.Daemon.ASL)
	}
}

// aslCapabilityNames is the protocol vocabulary a request counter may be keyed
// by, independent of what any one bridge advertised.
var aslCapabilityNames = []string{
	aslbridge.MethodRunsSnapshot, aslbridge.MethodQuestionGet, aslbridge.MethodQuestionReply,
	aslbridge.MethodQuestionResolve, aslbridge.MethodRunResume, aslbridge.MethodRunStopAfterCurrent,
	aslbridge.MethodRunAbort, aslbridge.MethodWorkbenchEnsure,
}

func TestDoctorReportsUnreportedASLOnOlderDaemon(t *testing.T) {
	setUpDoctorFixture(t, aslDoctorHealth(nil), daemon.EventsExport{})
	doctorStrict = true

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("an older daemon without ASL reporting must not fail strict doctor: %v", err)
	}
	if !strings.Contains(out.String(), "asl: unreported (older daemon)") {
		t.Errorf("doctor output missing the unreported asl line:\n%s", out.String())
	}
}
