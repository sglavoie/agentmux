package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// Run the shared headless/native fixture installer, then load its output with
// the daemon's real parser. A temporary HOME/PATH cannot contain native macOS
// alerts: Alerter discovers absolute host paths and posts under Terminal.
func TestPackagedAcceptanceFixtureDisablesNotifications(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required to exercise the packaged acceptance fixture")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "acceptance", "packaged-app.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, content string
	}{
		{"guided setup", config.ExampleConfig},
		{"omitted notification settings", "[defaults]\nlaunch_backend = 'tmux'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "home", ".config", "agent-mux", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			content := tc.content + `
[[profiles]]
name = "fixture"
harness = "claude"
home_dir = "~/.claude"
color = "yellow"
[[projects]]
title = "First project"
directory = "/tmp/acceptance-project"
profile = "fixture"
`
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(python, "-B", "-c", `
import importlib.util
import json
from pathlib import Path
import sys

spec = importlib.util.spec_from_file_location("packaged", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
world = module.Acceptance.__new__(module.Acceptance)
world.root = Path(sys.argv[2])
world.binary = world.root / "Applications/Agent Mux.app/Contents/Resources/bin/amx"
world.agent = world.root / "claude"
settings = world.root / "home/.claude/settings.json"
settings.parent.mkdir()
settings.write_text(json.dumps({"hooks": {
    event: [{"hooks": [{"command": str(world.binary) + " hook-indicator"}]}]
    for event in ("SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd")}}))
world.install_fixture()
`, script, root)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("install fixture: %v\n%s", err, output)
			}
			after, err := config.Load(path)
			if err != nil {
				t.Fatalf("fixture config cannot load: %v", err)
			}
			if after.Defaults.Notifications.AnyEnabled() {
				t.Fatalf("fixture can still post native alerts: %+v", after.Defaults.Notifications)
			}
			if !reflect.DeepEqual(before.Profiles, after.Profiles) || !reflect.DeepEqual(before.Projects, after.Projects) {
				t.Fatal("fixture notification isolation changed the configured profiles or projects")
			}
			if !reflect.DeepEqual(before.HarnessEmojis, after.HarnessEmojis) || !reflect.DeepEqual(before.HarnessColors, after.HarnessColors) {
				t.Fatal("fixture notification isolation changed the following config tables")
			}
		})
	}
}
