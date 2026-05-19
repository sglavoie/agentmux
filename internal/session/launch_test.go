package session_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestBuildUserVars_Parity is the contract guard for the user-var map written
// onto every managed tab. Both Launch and any future re-anchor helper must
// build the same set of keys with the same values — if this fails, hook-side
// consumers (cc_account, cc_config_dir, EffectiveProfile, ...) will see
// inconsistent state. Update the expected map deliberately when changing the
// contract.
func TestBuildUserVars_Parity(t *testing.T) {
	defaults := config.Defaults{
		Sort: "alpha",
		Args: []string{"--shared-default"},
	}
	acc := config.Account{
		Name:    "work",
		Harness: "claude",
		HomeDir: "~/.config/claude-work",
		Color:   "blue",
		Args:    []string{"--profile-default"},
	}
	sess := config.Session{
		Title:     "api",
		Directory: "~/projects/api",
		Profile:   "work",
		Account:   "work",
		Args:      []string{"--prompt", "hello world"},
		Env:       map[string]string{"DEBUG": "1", "PORT": "8080"},
		Groups:    []string{"backend", "infra"},
	}

	homeDir := config.ExpandTilde("~/.config/claude-work")
	projectDir := config.ExpandTilde("~/projects/api")
	quotedArgs := "--prompt 'hello world'"

	want := map[string]string{
		"amx_profile":       "work",
		"amx_harness":       "claude",
		"amx_home_dir":      homeDir,
		"amx_project":       "api",
		"amx_directory":     projectDir,
		"amx_args":          quotedArgs,
		"amx_env":           "DEBUG=1\x1fPORT=8080",
		"amx_groups":        "backend\x1finfra",
		"cc_account":        "work",
		"cc_config_dir":     homeDir,
		"cc_cwd":            projectDir,
		"cc_flags":          quotedArgs,
		"cc_explicit_flags": quotedArgs,
		"cc_env":            "DEBUG=1\x1fPORT=8080",
		"cc_groups":         "backend\x1finfra",
	}

	got := session.BuildUserVars(sess, acc, defaults)

	if len(got) != len(want) {
		t.Fatalf("BuildUserVars returned %d keys, expected %d\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Errorf("missing key %q in BuildUserVars output", k)
			continue
		}
		if gv != v {
			t.Errorf("key %q: got %q, want %q", k, gv, v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected extra key %q in BuildUserVars output", k)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildUserVars map mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestBuildUserVars_ProfileFallsBackToAccount verifies that when Session.Profile
// is empty but Account.Name is set, BuildUserVars uses the account name for
// both amx_profile and cc_account.
func TestBuildUserVars_ProfileFallsBackToAccount(t *testing.T) {
	defaults := config.Defaults{}
	acc := config.Account{
		Name:    "personal",
		Harness: "claude",
		HomeDir: "~/.config/claude",
	}
	sess := config.Session{
		Title:     "dotfiles",
		Directory: "/tmp/dotfiles",
		Account:   "personal",
	}

	got := session.BuildUserVars(sess, acc, defaults)
	if got["amx_profile"] != "personal" {
		t.Errorf("amx_profile: got %q, want %q", got["amx_profile"], "personal")
	}
	if got["cc_account"] != "personal" {
		t.Errorf("cc_account: got %q, want %q", got["cc_account"], "personal")
	}
}

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
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"},
			{Name: "personal", ConfigDir: "~/.config/claude", Color: "green"},
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

// TestLaunch_CallsKittenWithCorrectArgs verifies that Launch calls kitten @ launch
// with the correct tab title and user vars.
func TestLaunch_CallsKittenWithCorrectArgs(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		// All ls calls return empty — no existing sessions.
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:       "newproject",
		Account:     "work",
		Directory:   "/tmp/newproject",
		ClaudeFlags: []string{"--dangerously-skip-permissions"},
		Env:         map[string]string{"DEBUG": "1"},
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	// Verify kitten @ launch was called.
	launchCalled := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, a := range c.Args {
			if a == "launch" {
				hasLaunch = true
			}
		}
		if !hasLaunch {
			continue
		}
		launchCalled = true

		// Check expected args are present.
		argMap := make(map[string]bool)
		for _, a := range c.Args {
			argMap[a] = true
		}
		if !argMap["--tab-title=W:newproject"] {
			t.Errorf("expected --tab-title=W:newproject in args: %v", c.Args)
		}
		if !argMap["--var=cc_account=work"] {
			t.Errorf("expected --var=cc_account=work in args: %v", c.Args)
		}
		if !argMap["--var=cc_flags=--dangerously-skip-permissions"] {
			t.Errorf("expected cc_flags arg in args: %v", c.Args)
		}
		// Empty Kitty: no --location flag, Kitty uses its default placement.
		for _, a := range c.Args {
			if strings.HasPrefix(a, "--location") {
				t.Errorf("expected no --location arg for empty kitty, got %q", a)
			}
		}
	}
	if !launchCalled {
		t.Error("expected kitten @ launch to be called")
	}
}

// TestLaunch_ColorRetryApplied verifies that SetTabColor is called when the
// new tab appears in a post-launch ls response.
func TestLaunch_ColorRetryApplied(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")

	// Post-launch fixture: W:newproject tab with ID 100 (not in pre-launch IDs).
	postLaunch := []byte(`[{"id":1,"is_active":true,"is_focused":true,"last_focused":true,"tabs":[` +
		`{"id":100,"is_active":true,"title":"W:newproject","windows":[` +
		`{"id":101,"is_active":true,"title":"W:newproject","cwd":"/tmp/newproject","cmdline":["claude"],` +
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

// TestLaunch_StoresExplicitFlags verifies that Launch stores project-explicit
// args separately from the effective launch args.
func TestLaunch_StoresExplicitFlags(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue", Args: []string{"--account-flag"}},
		},
	}

	sess := config.Session{
		Title:     "explicit",
		Account:   "work",
		Directory: "/tmp/explicit",
		Args:      []string{"--project-flag"},
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a != "launch" {
				continue
			}
			argSet := make(map[string]bool)
			for _, arg := range c.Args {
				argSet[arg] = true
			}
			if !argSet["--var=cc_flags=--project-flag"] {
				t.Errorf("expected cc_flags=--project-flag, args: %v", c.Args)
			}
			if !argSet["--var=cc_explicit_flags=--project-flag"] {
				t.Errorf("expected cc_explicit_flags=--project-flag, args: %v", c.Args)
			}
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

func TestLaunch_MixedHarnessUsesProfileArgs(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha",
			Args: []string{"--shared-default"},
		},
		Accounts: []config.Account{
			{
				Name:    "claude-work",
				Harness: "claude",
				HomeDir: "~/.config/claude-work",
				Color:   "blue",
				Args:    []string{"--dangerously-skip-permissions"},
			},
			{
				Name:    "copilot-work",
				Harness: "copilot",
				HomeDir: "~/.copilot-work",
				Color:   "green",
				Args:    []string{"--model", "gpt-5"},
			},
		},
	}

	sess := config.Session{
		Title:     "docs",
		Profile:   "copilot-work",
		Account:   "copilot-work",
		Directory: "/tmp/docs",
	}
	acc := cfg.Accounts[1]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	expectedHome := "--env=COPILOT_HOME=" + config.ExpandTilde("~/.copilot-work")
	launchFound := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, a := range c.Args {
			if a == "launch" {
				hasLaunch = true
				break
			}
		}
		if !hasLaunch {
			continue
		}
		launchFound = true

		argSet := make(map[string]bool)
		for _, a := range c.Args {
			argSet[a] = true
		}
		if !argSet["--var=amx_harness=copilot"] {
			t.Errorf("expected amx_harness=copilot in args: %v", c.Args)
		}
		if !argSet["--var=cc_flags=--model gpt-5"] {
			t.Errorf("expected cc_flags=--model gpt-5 in args: %v", c.Args)
		}
		if !argSet[expectedHome] {
			t.Errorf("expected %q in args: %v", expectedHome, c.Args)
		}

		sawCopilot := false
		for _, arg := range c.Args {
			if strings.Contains(arg, "exec copilot --model gpt-5") {
				sawCopilot = true
			}
			if strings.Contains(arg, "--dangerously-skip-permissions") {
				t.Errorf("unexpected Claude-only flag in Copilot launch args: %v", c.Args)
			}
			if strings.Contains(arg, "--shared-default") {
				t.Errorf("unexpected shared defaults override in Copilot launch args: %v", c.Args)
			}
		}
		if !sawCopilot {
			t.Errorf("expected Copilot executable in launch args: %v", c.Args)
		}
	}
	if !launchFound {
		t.Error("expected kitten @ launch to be called")
	}
}

func TestLaunch_QuotesArgsWithSpacesAndMetacharacters(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "quoted",
		Account:   "work",
		Directory: "/tmp/quoted",
		Args:      []string{"--prompt", "hello world", "--literal=$HOME", "semi;colon"},
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	wantArgs := "--prompt 'hello world' '--literal=$HOME' 'semi;colon'"
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, a := range c.Args {
			if a == "launch" {
				hasLaunch = true
				break
			}
		}
		if !hasLaunch {
			continue
		}

		argSet := make(map[string]bool)
		for _, arg := range c.Args {
			argSet[arg] = true
		}
		if !argSet["--var=cc_flags="+wantArgs] {
			t.Errorf("expected quoted cc_flags in args: %v", c.Args)
		}
		if !argSet["--var=cc_explicit_flags="+wantArgs] {
			t.Errorf("expected quoted cc_explicit_flags in args: %v", c.Args)
		}

		sawQuotedShellCmd := false
		for _, arg := range c.Args {
			if arg == "exec claude "+wantArgs {
				sawQuotedShellCmd = true
				break
			}
		}
		if !sawQuotedShellCmd {
			t.Errorf("expected quoted shell command in args: %v", c.Args)
		}
		return
	}

	t.Error("expected kitten @ launch to be called")
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
	acc.Color = ""

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
