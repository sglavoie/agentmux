package kitty_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

var updateKittyBaseline = flag.Bool("update-kitty-baseline", false, "rewrite testdata/remote_control_baseline_v1.json")

const kittyBaselinePath = "testdata/remote_control_baseline_v1.json"

// kittyBaselineCall is one KittyClient method invocation and the exact
// `kitten @` argv it produced.
type kittyBaselineCall struct {
	Method string   `json:"method"`
	Argv   []string `json:"argv"`
}

// TestRemoteControlBaseline pins, byte for byte, the `kitten @` argv every
// KittyClient write and read method issues today, together with the match
// expressions callers build. It is the Kitty half of the backend migration's
// compatibility baseline (docs/backend-migration.md): moving Kitty behind a
// backend adapter must leave this file unchanged. A deliberate change to what
// AgentMux asks of Kitty regenerates it with -update-kitty-baseline and says
// why in the same commit.
func TestRemoteControlBaseline(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ --to unix:/tmp/kitty-baseline ls": []byte("[]"),
	})
	client := kitty.NewClientWithSocket(runner, "unix:/tmp/kitty-baseline")

	steps := []struct {
		method string
		call   func() error
	}{
		{"ListWindows", func() error { _, err := client.ListWindows(); return err }},
		{"LaunchTab", func() error {
			return client.LaunchTab(kitty.LaunchOpts{
				Title:     "W:api",
				Directory: "/projects/api",
				Location:  "after",
				Env:       map[string]string{"KITTY_LISTEN_ON": "unix:/tmp/kitty-baseline", "CLAUDE_CONFIG_DIR": "/home/work"},
				UserVars:  map[string]string{"amx_profile": "work", "amx_project": "api", "amx_harness": "claude"},
				Cmdline:   []string{"/bin/zsh", "-lc", "claude --resume x"},
			})
		}},
		{"LaunchOverlay", func() error {
			return client.LaunchOverlay(kitty.LaunchOpts{
				Title:     "amx",
				Directory: "/projects/api",
				Hold:      true,
				Env:       map[string]string{"AMX_CONFIG": "/cfg.toml"},
				UserVars:  map[string]string{"amx_overlay": "dashboard"},
				Cmdline:   []string{"amx", "overview", "--window-id", "7"},
			})
		}},
		{"CloseTab", func() error { return client.CloseTab(kitty.MatchTabByID(3)) }},
		{"MoveTab", func() error { return client.MoveTab(kitty.MatchTabByID(3), -2) }},
		{"SetTabColor", func() error {
			return client.SetTabColor(kitty.MatchTabByWindowID(7), "#112233", "#445566", "#ffffff", "#cccccc")
		}},
		{"SetTabTitle", func() error { return client.SetTabTitle(kitty.MatchTabByWindowID(7), "⚡ W:api") }},
		{"SetUserVars", func() error {
			return client.SetUserVars(kitty.MatchWindowByID(7), map[string]string{"amx_state": "{}", "amx_locked": "1"})
		}},
		{"FocusTab", func() error { return client.FocusTab(kitty.MatchTabByTitle("W:api")) }},
		{"FocusWindow", func() error { return client.FocusWindow(kitty.MatchWindowByVar("amx_profile", "work")) }},
		{"GetText", func() error { _, err := client.GetText(kitty.MatchWindowByID(7), "screen"); return err }},
		{"LaunchOverlayPager", func() error { return client.LaunchOverlayPager(kitty.MatchWindowByID(7)) }},
	}

	var got []kittyBaselineCall
	for _, step := range steps {
		before := len(runner.Calls)
		if err := step.call(); err != nil {
			t.Fatalf("%s: %v", step.method, err)
		}
		for _, c := range runner.Calls[before:] {
			got = append(got, kittyBaselineCall{Method: step.method, Argv: append([]string{c.Name}, c.Args...)})
		}
	}

	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if *updateKittyBaseline {
		if err := os.MkdirAll(filepath.Dir(kittyBaselinePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(kittyBaselinePath, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(kittyBaselinePath)
	if err != nil {
		t.Fatalf("reading the committed baseline (regenerate with -update-kitty-baseline): %v", err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("Kitty remote-control argv drifted from %s:\n got: %s\nwant: %s", kittyBaselinePath, encoded, want)
	}

	// Every KittyClient method is in the baseline, so a method added to the
	// interface without a pinned argv fails here rather than escaping it.
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.Method] = true
	}
	iface := reflect.TypeOf((*kitty.KittyClient)(nil)).Elem()
	for i := 0; i < iface.NumMethod(); i++ {
		if name := iface.Method(i).Name; !seen[name] {
			t.Errorf("KittyClient.%s has no entry in %s", name, kittyBaselinePath)
		}
	}
}
