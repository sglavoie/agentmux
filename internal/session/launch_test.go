package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// sequentialLSRunner returns different kitten @ ls responses on successive
// calls. All other commands get an empty successful response.
type sequentialLSRunner struct {
	lsResponses [][]byte
	lsCallIdx   int
	Calls       []kitty.MockCall
}

func (r *sequentialLSRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	r.Calls = append(r.Calls, kitty.MockCall{Name: name, Args: args})
	for _, a := range args {
		if a == "ls" {
			if r.lsCallIdx < len(r.lsResponses) {
				resp := r.lsResponses[r.lsCallIdx]
				r.lsCallIdx++
				return resp, nil, nil
			}
			// Past the end: return last response.
			return r.lsResponses[len(r.lsResponses)-1], nil, nil
		}
	}
	return []byte{}, nil, nil
}

func buildTestConfig() *config.Config {
	return &config.Config{
		Defaults:      config.Defaults{Sort: "alpha"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work"},
			{Name: "personal", ConfigDir: "~/.config/claude"},
		},
		Sessions: []config.Session{},
	}
}

// TestLaunch_DedupSkipsExisting verifies that launching an already-running
// session does not call kitten @ launch.
func TestLaunch_DedupSkipsExisting(t *testing.T) {
	existing := loadFixture(t, "kitty_ls.json") // has P:dotfiles and W:api
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": existing,
	})
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{Title: "api", Account: "work", Directory: "/tmp"}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, c := range runner.Calls {
		for _, a := range c.Args {
			if a == "launch" {
				t.Error("expected no kitten @ launch call for dedup, but got one")
			}
		}
	}
}

// TestLaunch_DedupUsesEffectiveProfile verifies that Launch dedups against the
// session's effective profile name rather than the account record's Name. A
// running tab anchored to `amx_profile=claude-prod` must be detected as a
// duplicate when relaunching a project whose Session.Profile is "claude-prod"
// even if the matching config.Account uses a different Name (e.g. when callers
// pass the profile's account record but Session.Profile takes precedence per
// the rest of the codebase).
func TestLaunch_DedupUsesEffectiveProfile(t *testing.T) {
	// Existing tab uses amx_profile=claude-prod (matches sess.Profile below).
	fixture := []byte(`[{"id":1,"is_active":true,"is_focused":true,"tabs":[` +
		`{"id":10,"is_active":true,"title":"api","windows":[` +
		`{"id":11,"is_active":true,"title":"api","cwd":"/tmp/api","user_vars":{` +
		`"amx_profile":"claude-prod","amx_project":"api","amx_directory":"/tmp/api"` +
		`}}]}]}]`)
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "claude", ConfigDir: "~/.config/claude"},
		},
	}

	// Session.Profile differs from Account.Name; effective profile = claude-prod.
	sess := config.Session{Title: "api", Profile: "claude-prod", Directory: "/tmp/api"}
	acc := cfg.Accounts[0]

	if err := session.Launch(client, sess, acc, cfg); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, c := range runner.Calls {
		for _, a := range c.Args {
			if a == "launch" {
				t.Errorf("expected dedup hit (Session.Profile matches existing tab amx_profile); got kitten @ launch call: %v", c.Args)
			}
		}
	}
}

// TestLaunch_InsertLocationUsesEffectiveProfile verifies account-mode sorted
// insertion uses the same effective profile as dedup and user vars. When a
// caller passes an account record whose Name differs from Session.Profile, the
// new tab must be placed in the Session.Profile group, not the account-name
// group.
func TestLaunch_InsertLocationUsesEffectiveProfile(t *testing.T) {
	fixture := []byte(`[{"id":1,"is_active":true,"is_focused":true,"tabs":[` +
		`{"id":10,"is_active":true,"title":"apple","windows":[` +
		`{"id":11,"is_active":true,"title":"apple","cwd":"/tmp/apple","user_vars":{` +
		`"amx_profile":"beta","amx_project":"apple","amx_directory":"/tmp/apple"` +
		`}}]},` +
		`{"id":20,"is_active":false,"title":"carrot","windows":[` +
		`{"id":21,"is_active":true,"title":"carrot","cwd":"/tmp/carrot","user_vars":{` +
		`"amx_profile":"gamma","amx_project":"carrot","amx_directory":"/tmp/carrot"` +
		`}}]}` +
		`]}]`)
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{fixture, fixture},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "account"},
		Accounts: []config.Account{
			{Name: "alpha", ConfigDir: "~/.config/alpha"},
		},
	}

	sess := config.Session{Title: "banana", Profile: "beta", Directory: "/tmp/banana"}
	acc := cfg.Accounts[0]

	if err := session.Launch(client, sess, acc, cfg); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	var launchArgs []string
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		argSet := make(map[string]bool)
		for _, a := range c.Args {
			argSet[a] = true
		}
		if argSet["launch"] {
			launchArgs = c.Args
			break
		}
	}
	if launchArgs == nil {
		t.Fatal("expected kitten @ launch to be called")
	}
	argSet := make(map[string]bool)
	for _, a := range launchArgs {
		argSet[a] = true
	}
	if !argSet["--location=after"] {
		t.Fatalf("expected launch after the beta/apple tab, got args: %v", launchArgs)
	}
	if !argSet["--var=amx_profile=beta"] {
		t.Errorf("expected amx_profile=beta in launch args: %v", launchArgs)
	}
	if !argSet["--var=cc_account=beta"] {
		t.Errorf("expected cc_account=beta in launch args: %v", launchArgs)
	}
}

// TestLaunch_DedupSeesConfigMatchedGhostTabs verifies that Launch does not
// duplicate a configured project whose anchor window lost its user vars. The
// config-backed title fallback already treats that tab as running for
// ListWithConfig/ReanchorAll; launch dedup must use the same view.
func TestLaunch_DedupSeesConfigMatchedGhostTabs(t *testing.T) {
	fixture := []byte(`[{"id":1,"is_active":true,"is_focused":true,"tabs":[` +
		`{"id":10,"is_active":true,"title":"api","windows":[` +
		`{"id":11,"is_active":true,"title":"api","cwd":"/tmp/api","user_vars":{}}` +
		`]}]}]`)
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: "/tmp/api"},
		},
	}

	sess := config.Session{Title: "api", Account: "work", Directory: "/tmp/api"}
	acc := cfg.Accounts[0]

	if err := session.Launch(client, sess, acc, cfg); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	for _, c := range runner.Calls {
		for _, a := range c.Args {
			if a == "launch" {
				t.Errorf("expected ghost-tab dedup hit; got kitten @ launch call: %v", c.Args)
			}
		}
	}
}

// TestLaunch_ColorRetryApplied verifies that SetTabColor is called when the
// new tab appears in a post-launch ls response.
func TestLaunch_ColorRetryApplied(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")

	// Post-launch fixture: newproject tab with ID 100 (not in pre-launch IDs).
	postLaunch := []byte(`[{"id":1,"is_active":true,"is_focused":true,"last_focused":true,"tabs":[` +
		`{"id":100,"is_active":true,"title":"newproject","windows":[` +
		`{"id":101,"is_active":true,"title":"newproject","cwd":"/tmp/newproject","cmdline":["claude"],` +
		`"user_vars":{"cc_account":"work","cc_config_dir":"","cc_flags":"","cc_pw":"","cc_env":"","cc_groups":"","cc_mute":"","cc_cwd":"/tmp/newproject"}}` +
		`]}]}]`)

	runner := &sequentialLSRunner{
		// ls calls: empty (dedup), empty (compute insert), then post-launch (color retry)
		lsResponses: [][]byte{empty, empty, postLaunch, postLaunch, postLaunch},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "newproject",
		Account:   "work",
		Directory: "/tmp/newproject",
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	colorCalled := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "set-tab-color" {
				colorCalled = true
				break
			}
		}
	}
	if !colorCalled {
		t.Error("expected kitten @ set-tab-color to be called after color retry found new tab")
	}
}

func TestLaunch_InsertsRelativeToFocusedWindow(t *testing.T) {
	twoWindows := loadFixture(t, "kitty_ls_two_windows.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{twoWindows, twoWindows},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "beta",
		Account:   "work",
		Directory: "/Users/user/projects/beta",
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	var sawLaunch bool
	var sawFocusedWindowRef bool
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}

		argSet := make(map[string]bool)
		for _, arg := range c.Args {
			argSet[arg] = true
		}

		if argSet["focus-tab"] {
			if argSet["--match=id:4"] {
				sawFocusedWindowRef = true
			}
			if argSet["--match=id:2"] {
				t.Fatalf("Launch focused a tab in another OS window: %v", c.Args)
			}
		}

		if !argSet["launch"] {
			continue
		}
		sawLaunch = true
		if !argSet["--location=before"] {
			t.Errorf("expected --location=before in launch args: %v", c.Args)
		}
	}

	if !sawFocusedWindowRef {
		t.Error("expected Launch to focus the in-window reference tab before launching")
	}
	if !sawLaunch {
		t.Error("expected kitten @ launch to be called")
	}
}
