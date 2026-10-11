package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// TestViewerKeysMatchTheApp holds the two halves of a history request
// together. The app's embedded terminal writes an escape sequence for Page
// Up, Find, and the rest (macos/AgentMux/Ghostty/SurfaceView+History.swift);
// the private tmux server binds the same sequences as user keys
// (internal/tmux). Neither side can see the other at build time, so a
// request added, removed, or reordered on one side only would silently do
// the wrong thing in a session view.
func TestViewerKeysMatchTheApp(t *testing.T) {
	// The requests in order, as both sides must have them.
	want := []struct {
		swift string
		key   tmux.ViewerKey
	}{
		{"pageUp", tmux.ViewerPageUp},
		{"pageDown", tmux.ViewerPageDown},
		{"top", tmux.ViewerTop},
		{"bottom", tmux.ViewerBottom},
		{"find", tmux.ViewerFind},
		{"findNext", tmux.ViewerFindNext},
		{"findPrevious", tmux.ViewerFindPrevious},
		{"markers", tmux.ViewerMarkers},
	}
	keys := tmux.ViewerKeys()
	if len(keys) != len(want) {
		t.Fatalf("tmux has %d viewer keys, this test knows %d: add the new one to both sides and here", len(keys), len(want))
	}
	for i, w := range want {
		if keys[i] != w.key || int(w.key) != i {
			t.Fatalf("viewer key %d is not %s on the tmux side", i, w.swift)
		}
	}

	const path = "macos/AgentMux/Ghostty/SurfaceView+History.swift"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)

	// enum HistoryRequest: Int, CaseIterable { case a, b, c ... }
	enum := regexp.MustCompile(`enum HistoryRequest: Int, CaseIterable \{\s*case ([A-Za-z, ]+)\n`).FindStringSubmatch(source)
	if enum == nil {
		t.Fatalf("%s no longer declares HistoryRequest as an Int enum with one case line", path)
	}
	var cases []string
	for _, c := range strings.Split(enum[1], ",") {
		cases = append(cases, strings.TrimSpace(c))
	}
	if len(cases) != len(want) {
		t.Fatalf("the app has %d history requests %v, tmux has %d", len(cases), cases, len(want))
	}
	for i, w := range want {
		if cases[i] != w.swift {
			t.Errorf("history request %d is %q in the app and %q on the tmux side", i, cases[i], w.swift)
		}
	}

	// var serverKey: String { "\(5001 + rawValue)~" }
	key := regexp.MustCompile(`var serverKey: String \{ "\\\((\d+) \+ rawValue\)(.)" \}`).FindStringSubmatch(source)
	if key == nil {
		t.Fatalf("%s no longer derives serverKey from a base number and rawValue", path)
	}
	base, err := strconv.Atoi(key[1])
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range want {
		app := strconv.Itoa(base+i) + key[2]
		if app != w.key.CSI() {
			t.Errorf("%s: the app sends CSI %s, tmux binds CSI %s", w.swift, app, w.key.CSI())
		}
		// And the server configuration really declares that sequence as
		// the user key of that index.
		declared := `set -s user-keys[` + strconv.Itoa(i) + `] "\e[` + w.key.CSI() + `"`
		if !strings.Contains(tmux.Config, declared) {
			t.Errorf("the tmux configuration does not contain %s", declared)
		}
		if !strings.Contains(tmux.Config, "bind-key -n User"+strconv.Itoa(i)+" ") {
			t.Errorf("the tmux configuration binds nothing to User%d (%s)", i, w.swift)
		}
	}
}

// TestWorkspaceFunctionKeysMatchTheApp holds the reference F1 shows in the
// app's workspace (kittykeys.Workspace, rendered by amx) to the function keys
// the app's main menu really binds (macos/AgentMux/UI/WorkspaceMenu.swift).
// The two are written in different languages and built apart, so a key added
// to one side only would be advertised and dead, or live and undocumented.
func TestWorkspaceFunctionKeysMatchTheApp(t *testing.T) {
	const path = "macos/AgentMux/UI/WorkspaceMenu.swift"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// (5, "Set Session Icon…", #selector(AppDelegate.pickSessionIcon(_:))),
	entries := regexp.MustCompile(`(?m)^\s*\((\d+), "[^"]+", #selector\(`).FindAllStringSubmatch(string(data), -1)
	if len(entries) == 0 {
		t.Fatalf("%s no longer lists its function keys one tuple a line", path)
	}
	bound := map[string]bool{}
	for _, entry := range entries {
		key := "f" + entry[1]
		if bound[key] {
			t.Errorf("the app binds %s twice", key)
		}
		bound[key] = true
	}

	listed := map[string]bool{}
	for _, action := range kittykeys.Workspace {
		if action.ConfigLine() != "" {
			t.Errorf("%s: a workspace key is the app's to run, yet it has the Kitty line %q", action.Key, action.ConfigLine())
		}
		if regexp.MustCompile(`^f\d+$`).MatchString(action.Key) {
			listed[action.Key] = true
			if !bound[action.Key] {
				t.Errorf("the reference lists %s (%s), which the app does not bind", action.Key, action.Description)
			}
		}
	}
	for key := range bound {
		if !listed[key] {
			t.Errorf("the app binds %s, which the reference does not list", key)
		}
	}

	// Every function key Kitty binds for its tabs works in the app too.
	for _, action := range kittykeys.Actions {
		if regexp.MustCompile(`^f\d+$`).MatchString(action.Key) && !bound[action.Key] {
			t.Errorf("Kitty binds %s (%s) and the app does not", action.Key, action.Description)
		}
	}
}
