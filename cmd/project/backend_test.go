package project

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// selectBackend sets --backend for one test.
func selectBackend(t *testing.T, value string) {
	t.Helper()
	if err := projectLaunchCmd.Flags().Set(cli.BackendFlag, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projectLaunchCmd.Flags().Set(cli.BackendFlag, "") })
}

// useTmux points tmux launches at a private server and the default registry
// at a temporary home.
func useTmux(t *testing.T) *tmuxtest.Fixture {
	t.Helper()
	f := tmuxtest.New(t)
	t.Setenv("HOME", t.TempDir())
	previous := cli.NewTmuxBackend
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return f.Backend, nil }
	t.Cleanup(func() { cli.NewTmuxBackend = previous })
	return f
}

func tmuxProjectConfig(t *testing.T) *config.Config {
	return &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude}},
		Projects: []config.Session{{Title: "API", Directory: t.TempDir(), Profile: "work"}},
	}
}

type launchEnvelope struct {
	SchemaVersion int               `json:"schema_version"`
	Data          projectLaunchData `json:"data"`
}

func TestProjectLaunchOnTmuxReportsTheRuntimeSession(t *testing.T) {
	f := useTmux(t)
	selectBackend(t, "tmux")
	client := &recordingKittyClient{}
	out, err := runProject(t, "launch", tmuxProjectConfig(t), client, true, "", "API")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, out)
	}
	if len(client.launched) != 0 {
		t.Fatalf("a tmux launch opened %d Kitty tabs", len(client.launched))
	}
	var envelope launchEnvelope
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	data := envelope.Data
	if envelope.SchemaVersion != 1 || !data.Changed || data.Backend != "tmux" || data.TabID != 0 || data.WindowID != 0 {
		t.Fatalf("envelope = %+v, want a changed tmux launch without Kitty IDs", envelope)
	}
	id, err := runtimesession.ParseID(data.RuntimeSessionID)
	if err != nil {
		t.Fatalf("runtime_session_id %q: %v", data.RuntimeSessionID, err)
	}
	store, err := runtimesession.NewStore(runtimesession.DefaultRegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Get(id)
	if err != nil || record.State != runtimesession.StateLive {
		t.Fatalf("record = %+v, %v; want live", record, err)
	}
	panes, err := f.Backend.Discover()
	if err != nil || len(panes) != 1 || panes[0].Ref.ID != record.Target.Ref {
		t.Fatalf("panes = %+v, %v; want the registered pane", panes, err)
	}
}

func TestProjectLaunchOnKittyReportsTheBackend(t *testing.T) {
	client := &recordingKittyClient{windows: []kitty.OSWindow{{ID: 1, IsFocused: true}}}
	out, err := runProject(t, "launch", testConfig(), client, true, "work", "API")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, out)
	}
	var envelope launchEnvelope
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Backend != "kitty" || envelope.Data.RuntimeSessionID != "" || len(client.launched) != 1 {
		t.Fatalf("envelope = %+v, want a kitty launch without a runtime session", envelope)
	}
}

func TestProjectLaunchBackendErrorsUseTheErrorEnvelope(t *testing.T) {
	tests := []struct {
		name, backend, code string
		setup               func(t *testing.T) *config.Config
	}{
		{name: "unknown backend", backend: "screen", code: "invalid_backend", setup: func(*testing.T) *config.Config { return testConfig() }},
		{name: "tmux missing", backend: "tmux", code: "backend_unavailable", setup: func(t *testing.T) *config.Config {
			previous := cli.NewTmuxBackend
			cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return nil, errors.New("tmux: not found") }
			t.Cleanup(func() { cli.NewTmuxBackend = previous })
			return testConfig()
		}},
		{name: "tmux launch fails", backend: "tmux", code: "action_failed", setup: func(t *testing.T) *config.Config {
			useTmux(t)
			cfg := tmuxProjectConfig(t)
			cfg.Projects[0].Directory += "/missing"
			return cfg
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.setup(t)
			selectBackend(t, test.backend)
			client := &recordingKittyClient{}
			out, err := runProject(t, "launch", cfg, client, true, "work", "API")
			var exit *cli.ExitError
			if !errors.As(err, &exit) || exit.Code != 1 {
				t.Fatalf("err = %v, want a quiet exit 1", err)
			}
			var envelope struct {
				Error cli.JSONError `json:"error"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.Error.Code != test.code {
				t.Fatalf("out = %s (%v), want error code %s", out, err, test.code)
			}
			if len(client.launched) != 0 {
				t.Fatal("a failed launch opened a Kitty tab")
			}
		})
	}
}
