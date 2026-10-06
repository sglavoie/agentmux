package debug

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// A bridge that resolves and then dies is not a resolution problem. Sending the
// operator to asl-sync would persist the same path and change nothing, so the
// dimension names what was launched and points at its environment instead.
func TestALaunchedBridgeThatKeepsExitingIsNotCalledUnresolved(t *testing.T) {
	resetDoctorTestState(t)

	in := pairedInputs()
	in.Status.Available = false
	in.Status.MutationsEnabled = false
	in.Status.Reason = "bridge exited"
	in.BridgeFailure = "asl bridge: handshake failed: EOF (stderr: SyntaxError: invalid syntax)"
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionDaemonResolution)
	if dim.Status != aslStatusFail {
		t.Errorf("daemon_resolution = %s in required mode, want fail", dim.Status)
	}
	if !strings.Contains(dim.Detail, "launched /opt/asl/bin/asl") || !strings.Contains(dim.Detail, "SyntaxError") {
		t.Errorf("detail = %q, want the launched path and the logged failure", dim.Detail)
	}
	if strings.Contains(dim.Remediation, "asl-sync") || !strings.Contains(dim.Remediation, "PATH") {
		t.Errorf("remediation = %q, want the daemon's environment rather than asl-sync", dim.Remediation)
	}
}

// With nothing launched, the old diagnosis still stands.
func TestAnUnresolvedBridgeStillPointsAtASLSync(t *testing.T) {
	resetDoctorTestState(t)

	in := pairedInputs()
	in.Status.Available = false
	in.Status.Executable = ""
	in.Status.ExecutableSource = ""
	in.Status.Reason = "asl bridge: asl: executable file not found"
	dim := dimension(t, evaluateASLIntegration(in), aslDimensionDaemonResolution)
	if !strings.Contains(dim.Remediation, "asl-sync") {
		t.Errorf("remediation = %q, want asl-sync", dim.Remediation)
	}
}

// The log is read only when there is a launched, disconnected bridge to
// explain; a healthy pairing never touches it.
func TestTheDaemonLogIsReadOnlyForALaunchedDisconnectedBridge(t *testing.T) {
	resetDoctorTestState(t)
	reads := 0
	doctorLastASLBridgeFailure = func() string { reads++; return "boom" }

	in := gatherASLIntegrationInputs(ipc.Health{Alive: true, ASL: &aslbridge.Status{
		Mode: string(aslbridge.ModeRequired), Available: true, Executable: "/opt/asl/bin/asl",
	}})
	if reads != 0 || in.BridgeFailure != "" {
		t.Fatalf("a connected bridge read the log %d times (%q)", reads, in.BridgeFailure)
	}
	in = gatherASLIntegrationInputs(ipc.Health{Alive: true, ASL: &aslbridge.Status{
		Mode: string(aslbridge.ModeRequired), Executable: "/opt/asl/bin/asl", Reason: "bridge exited",
	}})
	if reads != 1 || in.BridgeFailure != "boom" {
		t.Fatalf("reads = %d, failure = %q, want one read carrying the logged error", reads, in.BridgeFailure)
	}
}

func TestLastASLBridgeFailureReturnsTheNewestExitRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.jsonl")
	lines := []string{
		`{"level":"WARN","msg":"daemon: asl bridge exited; restarting","error":"first"}`,
		`{"level":"INFO","msg":"daemon: asl bridge connected"}`,
		`not json at all, mentioning daemon: asl bridge exited; restarting`,
		`{"level":"WARN","msg":"daemon: asl bridge exited; restarting","error":"asl bridge: handshake failed: EOF\n(stderr: SyntaxError: invalid syntax)"}`,
		`{"level":"INFO","msg":"something later"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := lastASLBridgeFailure(path)
	want := "asl bridge: handshake failed: EOF (stderr: SyntaxError: invalid syntax)"
	if got != want {
		t.Fatalf("failure = %q, want %q", got, want)
	}
}

func TestLastASLBridgeFailureIsSilentWithoutALog(t *testing.T) {
	if got := lastASLBridgeFailure(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Fatalf("failure = %q, want nothing for a missing log", got)
	}
}

func TestLastASLBridgeFailureIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.jsonl")
	line := `{"msg":"daemon: asl bridge exited; restarting","error":"` + strings.Repeat("x", 2000) + `"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := []rune(lastASLBridgeFailure(path)); len(got) != aslBridgeFailureMaxChars {
		t.Fatalf("failure has %d runes, want %d", len(got), aslBridgeFailureMaxChars)
	}
}

func TestTheDefaultReaderUsesTheDiagnosticDaemonLog(t *testing.T) {
	resetDoctorTestState(t)
	root := t.TempDir()
	paths := diagnostics.PathsForRoot(root)
	if err := os.MkdirAll(paths.DaemonDir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"msg":"daemon: asl bridge exited; restarting","error":"from the real path"}`
	if err := os.WriteFile(paths.DaemonLogFile, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doctorDiagnosticPaths = func() (diagnostics.Paths, error) { return paths, nil }
	if got := doctorLastASLBridgeFailure(); got != "from the real path" {
		t.Fatalf("failure = %q", got)
	}
}
