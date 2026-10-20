package debug

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// With the daemon down the report says so once: no second failure for the
// socket it left no trace of, one line for the ASL dimensions only a daemon
// answers, each remediation on a line of its own, and an ASL warning printed
// in its section only.
func TestDoctorWithTheDaemonDownSaysItOnce(t *testing.T) {
	setUpDoctorFixture(t, ipc.Health{}, protocol.EventsExport{})
	if err := os.Remove(doctorSocketPath()); err != nil {
		t.Fatal(err)
	}
	doctorLoadConfig = func() (*config.Config, error) {
		cfg := &config.Config{}
		cfg.Defaults.ASL.Command = "asl"
		return cfg, nil
	}
	aslDoctorLookPath = func(string) (string, error) { return "/Users/op/.local/bin/asl", nil }

	var out bytes.Buffer
	doctorCmd.SetOut(&out)
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"socket: none (the daemon is not running)",
		"- [daemon_unreachable] daemon is unreachable\n  remediation: run `amx daemon restart`",
		"daemon-side",
		"daemon_resolution, contract, projection, freshness, and delivery cannot be answered",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "[socket]") {
		t.Errorf("a missing socket is reported beside daemon_unreachable:\n%s", text)
	}
	if got := strings.Count(text, "the daemon is unreachable"); got != 1 {
		t.Errorf("the ASL section says the daemon is unreachable %d times, want once:\n%s", got, text)
	}
	if got := strings.Count(text, "does not inherit this PATH"); got != 1 {
		t.Errorf("the shell-resolution warning is printed %d times, want once:\n%s", got, text)
	}

	// The JSON report keeps every dimension and the ASL finding.
	out.Reset()
	doctorJSON = true
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	checks := map[string]bool{}
	for _, finding := range report.Findings {
		checks[finding.Check] = true
	}
	if checks["socket"] || !checks["daemon_unreachable"] || !checks[findingASLShellResolution] {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if len(report.ASLIntegration.Dimensions) != len(aslDimensionOrder) {
		t.Fatalf("dimensions = %+v", report.ASLIntegration.Dimensions)
	}
}
