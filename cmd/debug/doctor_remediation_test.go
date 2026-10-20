package debug

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// actionableFindings is every failure or warning identifier doctor and audit
// raise whose remedy follows from the identifier, with a representative
// message. The backend problems and the ASL dimensions are absent on purpose:
// their remedy is the hint or dimension remediation they come with
// (TestAttachedRemediationsFollowTheirSource).
var actionableFindings = []doctorFinding{
	{Severity: severityFailure, Check: "daemon_unreachable", Message: "daemon is unreachable"},
	{Severity: severityWarning, Check: "daemon_degraded", Message: "daemon started degraded: x"},
	{Severity: severityFailure, Check: "daemon_export", Message: "daemon export is unavailable: x"},
	{Severity: severityFailure, Check: checkBuildMismatch, Message: "CLI is a but daemon runs b"},
	{Severity: severityFailure, Check: "socket", Message: "daemon socket is older than the installed binary"},
	{Severity: severityFailure, Check: "socket", Message: "installed amx binary is unavailable: x"},
	{Severity: severityWarning, Check: "config_changed_since_daemon_start", Message: "x"},
	{Severity: severityWarning, Check: "notifications_unknown", Message: "x"},
	{Severity: severityFailure, Check: "notifications_unreported", Message: "x"},
	{Severity: severityFailure, Check: "notifications_unavailable", Message: "interactive notifications are enabled but unavailable: not_found"},
	{Severity: severityWarning, Check: checkScreenPatternOverrideInvalid, Message: "x"},
	{Severity: severityFailure, Check: checkScreenPersistentDecline, WindowID: 3, Message: "x"},
	{Severity: severityWarning, Check: "asl_starting", Message: "x"},
	{Severity: severityFailure, Check: "asl_unavailable", Message: "x"},
	{Severity: severityWarning, Check: "asl_read_only", Message: "x"},
	{Severity: severityWarning, Check: "kitty", Message: "Kitty was not asked for its windows"},
	{Severity: severityFailure, Check: "kitty", Message: "kitten @ ls failed: x"},
	{Severity: severityWarning, Check: checkKittySnapshotStale, Message: "x"},
	{Severity: severityFailure, Check: "renderer_drift", Message: "x"},
	{Severity: severityWarning, Check: checkCompanionBuildMismatch, Message: "x"},
	{Severity: severityWarning, Check: checkRuntimeReconcileFailing, Message: "x"},
	{Severity: severityWarning, Check: checkRuntimeReconcileStale, Message: "x"},
	{Severity: severityWarning, Check: checkScreenCaptureFailing, Message: "x"},
	{Severity: severityFailure, Check: "hooks", Message: "hooks for p require attention: drifted"},
	{Severity: severityFailure, Check: "hooks", Message: "hook status unavailable: x"},
	{Severity: severityWarning, Check: "hooks_unverified", Message: "x"},
	{Severity: severityWarning, Check: "hook_delivery_failures", Message: "x"},
	{Severity: severityWarning, Check: "project_directories", Message: "x"},
	{Severity: severityWarning, Check: "native_logging", Message: "x"},
	{Severity: severityFailure, Check: checkASLActiveState, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkSubagentVisibility, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkActiveAfterTerminal, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkShadowCurrentMismatch, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkQueueSaturation, Message: "x"},
	{Severity: severityFailure, Check: checkRenderGenerationDrift, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkRenderCausalBound, WindowID: 4, Message: "x"},
	{Severity: severityWarning, Check: checkHarnessDrift, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkOwnerMismatchPrompt, WindowID: 4, Message: "x"},
	{Severity: severityWarning, Check: checkOwnerMismatchBurst, WindowID: 4, Message: "x"},
	{Severity: severityWarning, Check: checkUnknownProducerFact, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkAcknowledgedPressure, WindowID: 4, Message: "x"},
	{Severity: severityFailure, Check: checkMalformedProcessIdentity, WindowID: 4, Message: "x"},
	{Severity: severityWarning, Check: checkStopHoldStuck, WindowID: 4, Message: "x"},
}

func TestEveryActionableFindingCarriesARemediation(t *testing.T) {
	for _, finding := range actionableFindings {
		if doctorRemediation(finding) == "" {
			t.Errorf("%s (%s, %q) has no remediation", finding.Check, finding.Severity, finding.Message)
		}
	}
	// Informational findings are context, not something to fix.
	for _, check := range []string{checkClosedTombstone, checkAcknowledgedState, checkTmuxClientTimeouts, "uninstalled_binary"} {
		if got := doctorRemediation(doctorFinding{Severity: severityInfo, Check: check}); got != "" {
			t.Errorf("info %s carries remediation %q", check, got)
		}
	}
}

// verifiedCommands is every amx command path a remediation may name, each
// checked against this build's command tree when it was added here. A
// remediation that names anything else fails until it is checked and listed.
var verifiedCommands = []string{
	"amx daemon restart", "amx daemon status", "amx daemon audit --duration", "amx daemon export-events --window", "amx daemon export-events --last",
	"amx config daemon-status", "amx config validate", "amx config asl-sync", "amx config hooks-sync", "amx config hooks-status", "amx config edit",
	"amx debug bundle --mode share", "amx debug tail", "amx debug probe --runtime-session", "amx debug status",
	"amx backend status", "amx project list --missing", "amx doctor",
}

var backtickCommand = regexp.MustCompile("`(amx [^`]*)`")

func TestDoctorRemediationsNameRealCommands(t *testing.T) {
	var all []string
	for _, finding := range actionableFindings {
		all = append(all, doctorRemediation(finding))
	}
	for _, message := range []string{": invalid_override", ": unsupported_platform", ": version_unsupported"} {
		all = append(all, notificationRemediation("interactive notifications are enabled but unavailable"+message))
	}
	for _, remediation := range all {
		for _, match := range backtickCommand.FindAllStringSubmatch(remediation, -1) {
			command := match[1]
			ok := false
			for _, verified := range verifiedCommands {
				if command == verified || strings.HasPrefix(command, verified+" ") {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("remediation names an unverified command %q: %s", command, remediation)
			}
		}
	}
}

func TestInvariantRemediationsNameTheWindowsExport(t *testing.T) {
	got := doctorRemediation(doctorFinding{Severity: severityFailure, Check: checkASLActiveState, WindowID: 42})
	if !strings.Contains(got, "`amx daemon export-events --window 42 --last 50`") {
		t.Errorf("remediation = %q, want the window's own export", got)
	}
	if got := doctorRemediation(doctorFinding{Severity: severityFailure, Check: checkBuildMismatch}); got != "run `amx daemon restart` so the daemon runs the installed build" {
		t.Errorf("build_mismatch remediation = %q", got)
	}
}

func TestNotificationRemediationFollowsTheReason(t *testing.T) {
	const prefix = "interactive notifications are enabled but unavailable: "
	for reason, want := range map[string]string{
		"not_found":            "install alerter 26 or newer",
		"version_unsupported":  "install alerter 26 or newer",
		"invalid_override":     "set [defaults.notifications] alerter_path to the absolute path",
		"unsupported_platform": "native notifications need macOS",
	} {
		got := notificationRemediation(prefix + reason)
		if !strings.Contains(got, want) || !strings.Contains(got, "[defaults.notifications] kind to false") {
			t.Errorf("%s: remediation = %q, want %q and how to turn notifications off", reason, got, want)
		}
	}
}

// A backend problem's remediation is `amx backend status`'s own hint, and an
// ASL dimension's is its dimension's; neither message changes.
func TestAttachedRemediationsFollowTheirSource(t *testing.T) {
	report := doctorReport{
		Backend: cli.BackendStatus{Problems: []cli.BackendProblem{{Code: "server_unreadable", Hint: "Run `amx backend status` again."}}},
		ASLIntegration: doctorASLIntegration{Dimensions: []doctorASLDimension{
			{Name: aslDimensionShellResolution, Status: aslStatusFail, Remediation: "run `amx config asl-sync`"},
		}},
		Findings: []doctorFinding{
			{Severity: severityFailure, Check: "backend_server_unreadable", Message: "did not answer. Run `amx backend status` again."},
			{Severity: severityFailure, Check: findingASLShellResolution, Message: "bare name; remediation: run `amx config asl-sync`"},
			{Severity: severityFailure, Check: checkBuildMismatch, Message: "CLI is a but daemon runs b"},
			{Severity: severityInfo, Check: checkClosedTombstone, Message: "closed"},
		},
	}
	attachDoctorRemediations(&report)
	want := []string{"Run `amx backend status` again.", "run `amx config asl-sync`", "run `amx daemon restart` so the daemon runs the installed build", ""}
	for i, finding := range report.Findings {
		if finding.Remediation != want[i] {
			t.Errorf("%s remediation = %q, want %q", finding.Check, finding.Remediation, want[i])
		}
	}
	if report.Findings[1].Message != "bare name; remediation: run `amx config asl-sync`" {
		t.Errorf("the ASL message changed: %q", report.Findings[1].Message)
	}
}

// The text report prints a remediation on its own line under its finding,
// and only when the message does not already say it.
func TestDoctorTextPrintsRemediationOnItsOwnLine(t *testing.T) {
	findings := []doctorFinding{
		newDoctorFinding(severityFailure, checkBuildMismatch, 0, "CLI is a but daemon runs b"),
		{Severity: severityFailure, Check: "socket", Message: "the daemon is not running the installed amx at x; run `amx daemon restart`", Remediation: "run `amx daemon restart`"},
		{Severity: severityInfo, Check: checkClosedTombstone, WindowID: 9, Message: "closed"},
	}
	var out bytes.Buffer
	if err := writeDoctorFindings(&out, findings, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	want := "- [build_mismatch] CLI is a but daemon runs b\n  remediation: run `amx daemon restart` so the daemon runs the installed build\n"
	if !strings.Contains(got, want) {
		t.Errorf("text report:\n%s\nwant it to contain:\n%s", got, want)
	}
	if strings.Count(got, "remediation:") != 1 {
		t.Errorf("a remediation the message already says was printed again:\n%s", got)
	}
	// The verdict still counts finding lines, not remediation lines.
	if got := doctorVerdict([]doctorFinding{newDoctorFinding(severityWarning, "native_logging", 0, "x")}); got != "overall: ok (1 warning)" {
		t.Errorf("doctorVerdict = %q", got)
	}
}

// The field is additive: present on an actionable finding in --json, absent
// from one with none, and the message is what it always was.
func TestDoctorJSONCarriesRemediation(t *testing.T) {
	backend := cli.BackendStatus{
		LaunchBackend: config.LaunchBackendTmux,
		Tmux:          cli.TmuxStatus{MinVersion: tmux.MinVersion},
		Problems: []cli.BackendProblem{{
			Code: "tmux_unavailable", Severity: "error",
			Message: "The tmux backend cannot start agents: no usable tmux was found.", Hint: tmux.RuntimeHint,
		}},
	}
	got, report, _ := runDoctorForBackend(t, backend, nil, false)
	found := findingsFor(report, "backend_tmux_unavailable")
	if len(found) != 1 || found[0].Remediation != tmux.RuntimeHint || !strings.Contains(found[0].Message, tmux.RuntimeHint) {
		t.Fatalf("backend_tmux_unavailable = %+v; want the hint as remediation and the message unchanged", found)
	}
	if unreachable := findingsFor(report, "daemon_unreachable"); len(unreachable) != 1 || !strings.Contains(unreachable[0].Remediation, "amx daemon restart") {
		t.Errorf("daemon_unreachable = %+v", unreachable)
	}
	if !strings.Contains(got, "  remediation: run `amx daemon restart`") {
		t.Errorf("the text report does not print the remediation:\n%s", got)
	}
	encoded, err := json.Marshal(doctorFinding{Severity: severityInfo, Check: checkClosedTombstone, Message: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "remediation") {
		t.Errorf("an empty remediation is encoded: %s", encoded)
	}
}
