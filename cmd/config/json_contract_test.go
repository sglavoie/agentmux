package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
	"strings"
)

// Config leaves that share the retained command envelope. `asl-sync` has its
// own audit in asl_sync_json_test.go; this one covers the rest of the family.
func TestConfigJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{configValidateCmd, configProfilesCmd, configDaemonStatusCmd, configShowCmd, configASLSyncCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}

func runConfigValidateJSON(t *testing.T, body string) (configValidateData, error) {
	t.Helper()
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	path := filepath.Join(tempHome, "config.toml")
	if err := os.WriteFile(path, []byte(body+validConfigBody(tempHome)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("amx_CONFIG", path)
	if err := configValidateCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	oldOut := configValidateCmd.OutOrStdout()
	t.Cleanup(func() {
		_ = configValidateCmd.Flags().Set("json", "false")
		configValidateCmd.SetOut(oldOut)
	})
	var out bytes.Buffer
	configValidateCmd.SetOut(&out)
	runErr := configValidateCmd.RunE(configValidateCmd, nil)
	var payload struct {
		SchemaVersion int                `json:"schema_version"`
		Data          configValidateData `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Data.Path != path {
		t.Fatalf("path = %q, want %q", payload.Data.Path, path)
	}
	return payload.Data, runErr
}

func TestConfigValidateJSONReportsWarningsOnAValidConfig(t *testing.T) {
	data, err := runConfigValidateJSON(t, "[defaults]\nrefresh_intervall = 3\nclaude_flags = []\n")
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !data.Valid || len(data.UnknownKeys) != 1 || data.UnknownKeys[0] != "defaults.refresh_intervall" {
		t.Fatalf("data = %+v", data)
	}
	if len(data.Deprecations) != 1 || data.Deprecations[0].Key != "defaults.claude_flags" {
		t.Fatalf("deprecations = %+v", data.Deprecations)
	}
	if data.InvalidProjectDirectories == nil || len(data.InvalidProjectDirectories) != 0 {
		t.Fatalf("invalid_project_directories = %#v, want an empty array", data.InvalidProjectDirectories)
	}
}

func TestConfigValidateJSONMissingDirectoryIsAWarningByDefault(t *testing.T) {
	data, err := runConfigValidateJSON(t, "[[projects]]\ntitle = \"gone\"\ndirectory = \"~/gone\"\nprofile = \"work\"\n")
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !data.Valid || len(data.InvalidProjectDirectories) != 1 {
		t.Fatalf("data = %+v", data)
	}
}

func TestConfigValidateJSONStrictMissingDirectoryIsAFindingWithQuietExit(t *testing.T) {
	setValidateStrict(t)
	data, err := runConfigValidateJSON(t, "[[projects]]\ntitle = \"gone\"\ndirectory = \"~/gone\"\nprofile = \"work\"\n")
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet || exitErr.Code == 0 {
		t.Fatalf("err = %v, want a quiet nonzero exit", err)
	}
	if data.Valid || len(data.InvalidProjectDirectories) != 1 {
		t.Fatalf("data = %+v", data)
	}
}

func TestProfilesCatalogIncludesUnassignedProfilesWithoutPrivateConfiguration(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	body := strings.Replace(validConfigBody(home), "[harness_emojis]", "[harness_emojis]\ncodex = \"🧪\"", 1) + "\n[[profiles]]\nname = \"review-only\"\nharness = \"codex\"\nhome_dir = \"/private/reviewer\"\ncolor = \"blue\"\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMX_CONFIG", path)
	oldOut := configProfilesCmd.OutOrStdout()
	t.Cleanup(func() { configProfilesCmd.SetOut(oldOut); _ = configProfilesCmd.Flags().Set("json", "false") })
	_ = configProfilesCmd.Flags().Set("json", "true")
	var out bytes.Buffer
	configProfilesCmd.SetOut(&out)
	if err := configProfilesCmd.RunE(configProfilesCmd, nil); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SchemaVersion int                 `json:"schema_version"`
		Data          []map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 {
		t.Fatal(out.String())
	}
	found := false
	for _, row := range envelope.Data {
		if len(row) != 2 {
			t.Fatalf("profile catalog leaked extra fields: %v", row)
		}
		if row["name"] == "review-only" && row["harness"] == "codex" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unassigned profile omitted: %s", out.String())
	}
}

// `config show --json` carries the file as text beside its path, and a config
// that is not there is the structured error, not an empty content.
func TestConfigShowJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	t.Setenv("amx_CONFIG", "")
	t.Setenv("AMX_CONFIG", path)
	run := func() (string, error) {
		t.Helper()
		if err := configShowCmd.Flags().Set("json", "true"); err != nil {
			t.Fatal(err)
		}
		oldOut := configShowCmd.OutOrStdout()
		t.Cleanup(func() {
			_ = configShowCmd.Flags().Set("json", "false")
			configShowCmd.SetOut(oldOut)
		})
		var out bytes.Buffer
		configShowCmd.SetOut(&out)
		err := configShowCmd.RunE(configShowCmd, nil)
		return out.String(), err
	}

	out, err := run()
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !strings.Contains(out, `"code":"config_unreadable"`) {
		t.Fatalf("missing config: out = %s, err = %v", out, err)
	}

	body := "# mine\n[defaults]\nsort = \"alpha\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = run()
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		SchemaVersion int            `json:"schema_version"`
		Data          configShowData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out)
	}
	if payload.SchemaVersion != 1 || payload.Data.Path != path || payload.Data.Content != body {
		t.Fatalf("payload = %+v", payload)
	}
}
