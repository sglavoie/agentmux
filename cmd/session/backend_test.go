package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// useBackendWorld replaces the configuration and the machine probe, and
// counts how often the server is stopped.
func useBackendWorld(t *testing.T, cfg *config.Config, cfgErr error, probe cli.BackendProbe) *int {
	t.Helper()
	previousLoad, previousProbe, previousStop := backendLoadConfig, backendProbe, backendStopServer
	stops := 0
	backendLoadConfig = func() (*config.Config, error) { return cfg, cfgErr }
	backendProbe = func() cli.BackendProbe { return probe }
	backendStopServer = func() error { stops++; return nil }
	t.Cleanup(func() {
		backendLoadConfig, backendProbe, backendStopServer = previousLoad, previousProbe, previousStop
		backendStatusJSON, backendStatusCheck, backendStopJSON, backendStopYes = false, false, false, false
		tmux.SetConfiguredBinary("")
	})
	return &stops
}

func backendProbeWith(available, running bool, agents int) cli.BackendProbe {
	return cli.BackendProbe{
		Resolve: func() (tmux.Runtime, []tmux.Candidate, error) {
			if !available {
				candidates := []tmux.Candidate{
					{Source: tmux.SourceBundled, Problem: "not present (this amx was not started by Agent Mux.app)"},
					{Source: tmux.SourcePath, Problem: "tmux not found"},
				}
				return tmux.Runtime{}, candidates, &tmux.NoRuntimeError{Candidates: candidates}
			}
			rt := tmux.Runtime{Binary: "/runtime/bin/tmux", Version: "3.7c", Source: tmux.SourceBundled, Verified: true}
			return rt, []tmux.Candidate{{Source: rt.Source, Path: rt.Binary, Version: rt.Version}}, nil
		},
		ServerDir: func() string { return "/tmp/agent-mux-test" },
		Listening: func(string) bool { return running },
		Pinned:    func(string) string { return "/runtime/bin/tmux" },
		Survey: func() ([]backend.Pane, string, error) {
			panes := make([]backend.Pane, agents)
			for i := range panes {
				id := string(rune('0' + i))
				panes[i] = backend.Pane{Ref: backend.PaneID("%" + id), Group: backend.GroupID("$" + id)}
			}
			return panes, "4242-1760000000", nil
		},
	}
}

func runBackend(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	backendCmd.SetOut(&out)
	backendCmd.SetErr(&out)
	backendCmd.SetArgs(args)
	t.Cleanup(func() { backendCmd.SetArgs(nil) })
	err := backendCmd.Execute()
	return out.String(), err
}

func TestBackendStatusJSONIsTheVersionedReport(t *testing.T) {
	useBackendWorld(t, &config.Config{Defaults: config.Defaults{LaunchBackend: "tmux"}}, nil, backendProbeWith(true, true, 2))
	out, err := runBackend(t, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		Data          cli.BackendStatus `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decoding %q: %v", out, err)
	}
	if envelope.SchemaVersion != 1 {
		t.Fatalf("schema version = %d", envelope.SchemaVersion)
	}
	status := envelope.Data
	if status.LaunchBackend != "tmux" || !status.Tmux.Available || !status.Tmux.Verified || status.Server.Agents != 2 {
		t.Fatalf("status = %+v", status)
	}
	// Consumers iterate these; null would make them branch.
	if !strings.Contains(out, `"problems":[]`) {
		t.Fatalf("an empty problem list is not encoded as []:\n%s", out)
	}
}

func TestBackendStatusWithoutTmuxExplainsItselfAndStillSucceeds(t *testing.T) {
	useBackendWorld(t, &config.Config{Defaults: config.Defaults{LaunchBackend: "tmux"}}, nil, backendProbeWith(false, false, 0))
	out, err := runBackend(t, "status")
	if err != nil {
		t.Fatalf("status reports a missing tmux; it must not fail on one: %v", err)
	}
	for _, want := range []string{
		"launch backend: tmux", "tmux: not available (need 3.5 or newer)", "server: not running",
		"error: The tmux backend cannot start agents", "PATH: tmux not found", "→ Install tmux 3.5 or newer (`brew install tmux`)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not say %q:\n%s", want, out)
		}
	}
}

func TestBackendStatusCheckFailsWhenTmuxIsUnusable(t *testing.T) {
	useBackendWorld(t, nil, nil, backendProbeWith(false, false, 0))
	out, err := runBackend(t, "status", "--check", "--json")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 || !exit.Quiet {
		t.Fatalf("status --check = %v, want a quiet exit 1", err)
	}
	if !strings.Contains(out, `"code":"tmux_unavailable"`) {
		t.Fatalf("the report was not written before failing:\n%s", out)
	}
}

func TestBackendStatusTreatsAMissingConfigurationAsAFreshInstall(t *testing.T) {
	useBackendWorld(t, nil, &config.MissingConfigError{Path: "/nowhere/config.toml"}, backendProbeWith(true, false, 0))
	out, err := runBackend(t, "status", "--check")
	if err != nil {
		t.Fatalf("status --check on a fresh install: %v\n%s", err, out)
	}
	if strings.Contains(out, "config") || !strings.Contains(out, "launch backend: kitty") {
		t.Fatalf("a fresh install is reported as a configuration problem:\n%s", out)
	}
}

func TestBackendStatusReportsAnInvalidConfiguration(t *testing.T) {
	useBackendWorld(t, nil, errors.New("defaults.tmux.path \"tmux\": must be an absolute path"), backendProbeWith(true, false, 0))
	out, err := runBackend(t, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "error: The configuration could not be loaded: defaults.tmux.path") {
		t.Fatalf("status:\n%s", out)
	}
}

func TestBackendStopNeedsConfirmationAndNamesWhatItEnds(t *testing.T) {
	stops := useBackendWorld(t, nil, nil, backendProbeWith(true, true, 3))
	out, err := runBackend(t, "stop")
	if err == nil || !strings.Contains(err.Error(), "this ends 3 running agents; pass --yes to confirm") {
		t.Fatalf("stop = %v\n%s", err, out)
	}
	if *stops != 0 {
		t.Fatal("stop ended the server without --yes")
	}

	out, err = runBackend(t, "stop", "--json")
	if err == nil || !strings.Contains(out, `"code":"confirmation_required"`) {
		t.Fatalf("stop --json = %v\n%s", err, out)
	}
	if *stops != 0 {
		t.Fatal("stop --json ended the server without --yes")
	}
}

func TestBackendStopWithYesEndsTheServer(t *testing.T) {
	stops := useBackendWorld(t, nil, nil, backendProbeWith(true, true, 1))
	out, err := runBackend(t, "stop", "--yes", "--json")
	if err != nil {
		t.Fatalf("stop --yes: %v\n%s", err, out)
	}
	if *stops != 1 {
		t.Fatalf("the server was stopped %d times", *stops)
	}
	if !strings.Contains(out, `"stopped":true`) || !strings.Contains(out, `"agents":1`) {
		t.Fatalf("result:\n%s", out)
	}
}

func TestBackendStopWithNoServerStopsNothing(t *testing.T) {
	stops := useBackendWorld(t, nil, nil, backendProbeWith(true, false, 0))
	out, err := runBackend(t, "stop", "--yes")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if *stops != 0 || !strings.Contains(out, "nothing to stop") {
		t.Fatalf("stops = %d\n%s", *stops, out)
	}
}
