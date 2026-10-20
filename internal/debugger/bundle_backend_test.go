package debugger

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// A backend that failed has to be diagnosable from a bundle someone else
// reads, and the bundle must not be how a prompt, a credential, or a home
// directory leaves the machine. The counts, classes, and releases of the
// backend and runtime blocks survive share mode; every free-form string in
// them, and every path, does not.
func TestShareBundleKeepsBackendHealthAndDropsWhatItCouldCarry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const (
		promptCanary = "PROMPT-CANARY-rewrite-the-billing-service"
		secretCanary = "sk-abcdefghijklmnopqrstuvwxyz"
		homeCanary   = "/Users/alice/clients/acme"
	)
	// The record a reconciliation edge writes, with a field a careless
	// producer might add.
	edge := diagnostics.NewEnvelope("daemon.runtime_reconcile")
	edge.Outcome, edge.Reason = "error", "reconcile_failed"
	edge.Evidence = map[string]string{
		"failure_class": "timeout", "live_sessions": "30", "attached_sessions": "4", "servers": "1", "duration_ms": "5003.2",
		"pane_text": promptCanary, "stderr": "tmux: " + homeCanary + " " + secretCanary,
	}
	if err := journal.Append(t.Context(), edge); err != nil {
		t.Fatal(err)
	}

	age := 95.0
	runtime := &protocol.RuntimeHealth{
		Reconcile: protocol.RuntimeReconcileHealth{
			Enabled: true, IntervalSeconds: 2, Passes: 900, Failures: 7, ConsecutiveFailures: 7,
			AgeSeconds: &age, LastDurationMs: 5003.2, MaxDurationMs: 5003.2, FailureClass: "timeout",
		},
		Sessions:   protocol.RuntimeSessionHealth{Known: true, Live: 30, Tmux: 30, Servers: 1, Attached: 4, Unattached: 26},
		Capture:    protocol.ScreenCaptureHealth{Reads: 4000, Failures: 12, ConsecutiveFailures: 12},
		TmuxClient: protocol.TmuxClientHealth{Limit: 4, Commands: 5000, Timeouts: 7, MaxInFlight: 4, Queued: 80, MaxQueueMs: 5000},
	}
	// The shape of cli.BackendStatus, with canaries wherever it holds text.
	backend := map[string]any{
		"launch_backend": "tmux",
		"tmux": map[string]any{
			"available": false, "min_version": "3.5", "verified": false,
			"candidates": []any{map[string]any{"source": "path", "path": homeCanary + "/bin/tmux", "problem": "tmux said: " + promptCanary}},
		},
		"server": map[string]any{
			"directory": "/private/tmp/agent-mux-501", "socket": "/private/tmp/agent-mux-501/tmux.sock",
			"running": true, "binary": homeCanary + "/tmux", "generation": "4242:1760000000", "sessions": 30, "agents": 29,
		},
		"problems": []any{map[string]any{
			"code": "server_unreadable", "severity": "error",
			"message": "The private tmux server is running but did not answer: " + promptCanary + " " + secretCanary,
			"hint":    "Look in " + homeCanary,
		}},
	}
	status := &StatusReport{
		SchemaVersion: StatusSchemaVersion, GeneratedAt: now,
		Daemon:  DaemonReport{Alive: true, Ready: true, Runtime: runtime},
		Backend: backend,
		Findings: []Finding{
			{Verdict: VerdictWarning, Check: "runtime_reconcile_failing", Message: "the last 7 passes failed " + promptCanary},
		},
	}
	doctor := map[string]any{
		"schema_version": 7,
		"backend":        backend,
		"versions":       map[string]any{"amx": "0.1.1", "go": "go1.26.0", "tmux": "3.7c", "tmux_source": "bundled", "tmux_min": "3.5", "kitty": "0.43.1"},
		"daemon":         map[string]any{"alive": true, "runtime": runtime},
		"findings": []any{map[string]any{
			"severity": "failure", "check": "backend_server_unreadable", "message": "did not answer: " + promptCanary,
		}},
	}
	output := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(t.Context(), BundleOptions{
		Paths: paths, OutputPath: output, Mode: BundleModeShare, Now: now, Journal: journal,
		Config: &config.Config{}, Status: status, Doctor: doctor, Build: buildinfo.Info{Version: "0.1.1"},
	}); err != nil {
		t.Fatal(err)
	}
	members := readBundleMembers(t, output)

	for name, data := range members {
		for _, canary := range []string{promptCanary, secretCanary, homeCanary, "alice", "agent-mux-501", "4242:1760000000"} {
			if strings.Contains(string(data), canary) {
				t.Errorf("%s carries %q:\n%s", name, canary, data)
			}
		}
	}

	var shared struct {
		Daemon struct {
			Runtime protocol.RuntimeHealth `json:"runtime"`
		} `json:"daemon"`
		Backend struct {
			LaunchBackend string `json:"launch_backend"`
			Server        struct {
				Running  bool `json:"running"`
				Sessions int  `json:"sessions"`
				Agents   int  `json:"agents"`
			} `json:"server"`
			Problems []struct {
				Code     string `json:"code"`
				Severity string `json:"severity"`
			} `json:"problems"`
		} `json:"backend"`
		Versions map[string]string `json:"versions"`
		Findings []struct {
			Check string `json:"check"`
		} `json:"findings"`
	}
	for _, name := range []string{"status.json", "doctor.json"} {
		shared.Versions = nil
		if err := json.Unmarshal(members[name], &shared); err != nil {
			t.Fatalf("decode %s: %v\n%s", name, err, members[name])
		}
		got := shared.Daemon.Runtime
		if got.Reconcile.ConsecutiveFailures != 7 || got.Reconcile.FailureClass != "timeout" || got.Reconcile.AgeSeconds == nil ||
			got.Sessions != runtime.Sessions || got.Capture.ConsecutiveFailures != 12 || got.TmuxClient != runtime.TmuxClient {
			t.Errorf("%s runtime = %+v; want the daemon's block intact", name, got)
		}
		b := shared.Backend
		if b.LaunchBackend != "tmux" || !b.Server.Running || b.Server.Sessions != 30 || b.Server.Agents != 29 ||
			len(b.Problems) != 1 || b.Problems[0].Code != "server_unreadable" || b.Problems[0].Severity != "error" {
			t.Errorf("%s backend = %+v; want the setup and the problem's code", name, b)
		}
		if len(shared.Findings) != 1 || !strings.Contains(shared.Findings[0].Check, "_") {
			t.Errorf("%s findings = %+v; want the identifier kept", name, shared.Findings)
		}
	}
	if shared.Versions["tmux"] != "3.7c" || shared.Versions["tmux_min"] != "3.5" || shared.Versions["kitty"] != "0.43.1" || shared.Versions["go"] != "go1.26.0" {
		t.Errorf("doctor.json versions = %+v; want every release kept", shared.Versions)
	}

	// The journal edge keeps its class and its counts.
	records := string(members["causal-records.json"])
	for _, want := range []string{`"daemon.runtime_reconcile"`, `"failure_class": "timeout"`, `"live_sessions": "30"`, `"duration_ms": "5003.2"`} {
		if !strings.Contains(records, want) {
			t.Errorf("causal-records.json missing %s:\n%s", want, records)
		}
	}
}
