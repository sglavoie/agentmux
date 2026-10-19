package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

func TestDaemonSettingsStatusJSONNeverClaimsUnknownSettingsApplied(t *testing.T) {
	oldLoad, oldHealth := daemonSettingsLoad, daemonSettingsHealth
	oldOut := configDaemonStatusCmd.OutOrStdout()
	t.Cleanup(func() {
		daemonSettingsLoad = oldLoad
		daemonSettingsHealth = oldHealth
		configDaemonStatusCmd.SetOut(oldOut)
		_ = configDaemonStatusCmd.Flags().Set("json", "false")
	})
	cfg := &config.Config{}
	daemonSettingsLoad = func() (*config.Config, error) { return cfg, nil }
	_ = configDaemonStatusCmd.Flags().Set("json", "true")
	running := cfg.DaemonSettings()
	for _, tc := range []struct {
		health ipc.Health
		known  bool
		count  int
	}{
		{ipc.Health{}, false, 0},
		{ipc.Health{Alive: true}, false, 0},
		{ipc.Health{Alive: true, DaemonSettings: running}, true, 0},
	} {
		daemonSettingsHealth = func() ipc.Health { return tc.health }
		var out bytes.Buffer
		configDaemonStatusCmd.SetOut(&out)
		if err := configDaemonStatusCmd.RunE(configDaemonStatusCmd, nil); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			SchemaVersion int                  `json:"schema_version"`
			Data          daemonSettingsStatus `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.SchemaVersion != 1 || envelope.Data.Known != tc.known || len(envelope.Data.Changed) != tc.count {
			t.Fatalf("report = %s", out.Bytes())
		}
	}
	cfg.Defaults.Notifications.Stop = true
	daemonSettingsHealth = func() ipc.Health { return ipc.Health{Alive: true, DaemonSettings: running} }
	var out bytes.Buffer
	configDaemonStatusCmd.SetOut(&out)
	if err := configDaemonStatusCmd.RunE(configDaemonStatusCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"changed":["Notifications"]`)) {
		t.Fatalf("changed report = %s", out.Bytes())
	}
	daemonSettingsLoad = func() (*config.Config, error) { return nil, errors.New("invalid TOML") }
	out.Reset()
	if err := configDaemonStatusCmd.RunE(configDaemonStatusCmd, nil); err == nil || !bytes.Contains(out.Bytes(), []byte(`"error"`)) {
		t.Fatalf("invalid config: %s, %v", out.Bytes(), err)
	}
}
