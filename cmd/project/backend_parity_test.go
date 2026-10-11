package project

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// userVarKittyClient is a Kitty that remembers what a launch wrote, the way
// the real one does: a second launch can only be recognized as a duplicate
// from the user vars of the first.
type userVarKittyClient struct{ recordingKittyClient }

func (c *userVarKittyClient) LaunchTab(opts kitty.LaunchOpts) error {
	if err := c.recordingKittyClient.LaunchTab(opts); err != nil {
		return err
	}
	raw, err := json.Marshal(opts.UserVars)
	if err != nil {
		return err
	}
	tabs := c.windows[0].Tabs
	window := &tabs[len(tabs)-1].Windows[0]
	window.CWD = opts.Directory
	return json.Unmarshal(raw, &window.UserVars)
}

// backendIdentityKeys are the launch payload's keys that say where the
// session is. They are the one part of the payload that depends on the
// backend, and each backend has its own.
var backendIdentityKeys = map[string][]string{
	"kitty": {"backend", "tab_id", "window_id"},
	"tmux":  {"backend", "runtime_session_id"},
}

type launchOutcome struct {
	exit     int
	envelope map[string]any
}

// neutral is the envelope without the identity keys: what must be the same
// whichever backend ran the launch.
func (o launchOutcome) neutral(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(o.envelope)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if data, ok := out["data"].(map[string]any); ok {
		for _, keys := range backendIdentityKeys {
			for _, key := range keys {
				delete(data, key)
			}
		}
	}
	return out
}

func (o launchOutcome) identityKeys() []string {
	data, _ := o.envelope["data"].(map[string]any)
	var keys []string
	for _, all := range backendIdentityKeys {
		for _, key := range all {
			if _, ok := data[key]; ok && !contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func launchOutcomeOf(t *testing.T, out string, err error) launchOutcome {
	t.Helper()
	outcome := launchOutcome{}
	if err != nil {
		var exit *cli.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("launch failed outside the exit contract: %v\n%s", err, out)
		}
		outcome.exit = exit.Code
	}
	if err := json.Unmarshal([]byte(out), &outcome.envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return outcome
}

// The same launches on Kitty and on tmux exit the same way and write the same
// JSON, apart from the keys that name where the session is.
func TestProjectLaunchJSONAndExitCodesMatchAcrossBackends(t *testing.T) {
	useTmux(t)
	// The real directory rule: this package's tests otherwise accept every
	// directory, and the rule is what keeps a backend from being the one to
	// find out.
	stubbed := actions.ProjectDirectoryProblem
	actions.ProjectDirectoryProblem = config.ProjectDirectoryProblem
	t.Cleanup(func() { actions.ProjectDirectoryProblem = stubbed })
	directory := t.TempDir()
	newConfig := func() *config.Config {
		return &config.Config{
			Defaults: config.Defaults{Sort: "alpha"},
			Profiles: []config.Account{
				{Name: "work", Harness: harness.Claude, Color: "purple"},
				{Name: "other", Harness: harness.Claude, Color: "blue"},
			},
			Projects: []config.Session{
				{Title: "API", Directory: directory, Profile: "work"},
				{Title: "Twin", Directory: directory, Profile: "work"},
				{Title: "Twin", Directory: directory, Profile: "other"},
				{Title: "Gone", Directory: directory + "/missing", Profile: "work"},
			},
		}
	}
	steps := []struct {
		name, profile, title string
		wantExit             int
		wantChanged          any // nil for an error envelope
		wantError            string
	}{
		{name: "first launch", profile: "work", title: "API", wantChanged: true},
		// The command always starts a new instance; dedup is the dashboard's.
		{name: "same project again", profile: "work", title: "API", wantChanged: true},
		{name: "same title on another profile", profile: "other", title: "Twin", wantChanged: true},
		{name: "ambiguous title", title: "Twin", wantExit: 1, wantError: "ambiguous_project"},
		{name: "unknown project", title: "Nope", wantExit: 1, wantError: "not_found"},
		{name: "unusable directory", profile: "work", title: "Gone", wantExit: 1, wantError: "directory_unusable"},
	}

	outcomes := map[string][]launchOutcome{}
	for _, backend := range []string{"kitty", "tmux"} {
		selectBackend(t, backend)
		client := &userVarKittyClient{recordingKittyClient{windows: []kitty.OSWindow{{ID: 1, IsFocused: true}}}}
		for _, step := range steps {
			out, err := runProject(t, "launch", newConfig(), client, true, step.profile, step.title)
			outcomes[backend] = append(outcomes[backend], launchOutcomeOf(t, out, err))
		}
		if backend == "tmux" && len(client.launched) != 0 {
			t.Fatalf("tmux launches opened %d Kitty tabs", len(client.launched))
		}
	}

	for i, step := range steps {
		onKitty, onTmux := outcomes["kitty"][i], outcomes["tmux"][i]
		for backend, outcome := range map[string]launchOutcome{"kitty": onKitty, "tmux": onTmux} {
			if outcome.exit != step.wantExit {
				t.Errorf("%s on %s: exit %d; want %d (%v)", step.name, backend, outcome.exit, step.wantExit, outcome.envelope)
			}
			if step.wantError != "" {
				failure, _ := outcome.envelope["error"].(map[string]any)
				if failure["code"] != step.wantError {
					t.Errorf("%s on %s: error %v; want code %s", step.name, backend, outcome.envelope, step.wantError)
				}
				continue
			}
			data, _ := outcome.envelope["data"].(map[string]any)
			if data["changed"] != step.wantChanged || data["backend"] != backend {
				t.Errorf("%s on %s: changed %v backend %v; want %v on %s", step.name, backend, data["changed"], data["backend"], step.wantChanged, backend)
			}
			// A launch that started an agent says where it is, in its own
			// backend's keys and in no other's.
			if step.wantChanged == true {
				want := append([]string(nil), backendIdentityKeys[backend]...)
				sort.Strings(want)
				if got := outcome.identityKeys(); !reflect.DeepEqual(got, want) {
					t.Errorf("%s on %s: identity keys %v; want %v", step.name, backend, got, want)
				}
			}
		}
		if onKitty.exit != onTmux.exit {
			t.Errorf("%s: exit %d on Kitty and %d on tmux", step.name, onKitty.exit, onTmux.exit)
		}
		if k, m := onKitty.neutral(t), onTmux.neutral(t); !reflect.DeepEqual(k, m) {
			t.Errorf("%s: JSON differs across backends\nkitty: %v\ntmux:  %v", step.name, k, m)
		}
	}
}
