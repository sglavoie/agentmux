package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/kittykeys"
)

// fakeOverviewClient is this file's own fake, because the overview's singleton
// check is the one in this tree that must REPORT a Kitty failure rather than
// degrade to "nothing is open": every call it makes therefore needs its own
// injectable error, which fakeDashboardClient has no use for.
type fakeOverviewClient struct {
	windows        []kitty.OSWindow
	listErr        error
	focusTabErr    error
	focusWindowErr error

	listCalls      int
	focusedTabs    []string
	focusedWindows []string
	userVars       []fakeUserVarsCall
}

func (f *fakeOverviewClient) ListWindows() ([]kitty.OSWindow, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.windows, nil
}

func (f *fakeOverviewClient) FocusTab(matchExpr string) error {
	f.focusedTabs = append(f.focusedTabs, matchExpr)
	return f.focusTabErr
}

func (f *fakeOverviewClient) FocusWindow(matchExpr string) error {
	f.focusedWindows = append(f.focusedWindows, matchExpr)
	return f.focusWindowErr
}

func (f *fakeOverviewClient) SetUserVars(match string, vars map[string]string) error {
	f.userVars = append(f.userVars, fakeUserVarsCall{match: match, vars: vars})
	return nil
}

func (*fakeOverviewClient) LaunchTab(kitty.LaunchOpts) error     { return nil }
func (*fakeOverviewClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (*fakeOverviewClient) CloseTab(string) error                { return nil }
func (*fakeOverviewClient) MoveTab(string, int) error            { return nil }
func (*fakeOverviewClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (*fakeOverviewClient) SetTabTitle(string, string) error       { return nil }
func (*fakeOverviewClient) GetText(string, string) (string, error) { return "", nil }
func (*fakeOverviewClient) LaunchOverlayPager(string) error        { return nil }

// startedOverview records what the surface was started with. The surface itself
// is never started: runOverview is replaced, so no test in this file draws a
// Bubble Tea program, reads the operator's config, or touches a managed session.
type startedOverview struct {
	calls          int
	cfg            *config.Config
	client         kitty.KittyClient
	sourceWindowID int
}

func installOverviewSeams(t *testing.T, client kitty.KittyClient, cfg *config.Config, cfgErr error) *startedOverview {
	t.Helper()

	previousClient, previousConfig, previousRun := newOverviewClient, loadOverviewConfig, runOverview
	t.Cleanup(func() {
		newOverviewClient, loadOverviewConfig, runOverview = previousClient, previousConfig, previousRun
	})

	started := &startedOverview{}
	newOverviewClient = func() kitty.KittyClient { return client }
	loadOverviewConfig = func() (*config.Config, error) {
		if cfgErr != nil {
			return nil, cfgErr
		}
		return cfg, nil
	}
	runOverview = func(got *config.Config, gotClient kitty.KittyClient, sourceWindowID int) error {
		started.calls++
		started.cfg = got
		started.client = gotClient
		started.sourceWindowID = sourceWindowID
		return nil
	}
	return started
}

// overviewWindow is a pane whose foreground process is a running `amx overview`,
// spelled the way the F2 binding spells it.
func overviewWindow(windowID int) kitty.Window {
	return kitty.Window{
		ID: windowID,
		ForegroundProcesses: []kitty.ForegroundProcess{{
			Cmdline: []string{"/usr/local/bin/amx", "overview", "--window-id", "101"},
		}},
	}
}

func TestOverviewStartsTheSurfaceWhenNoOverviewIsOpen(t *testing.T) {
	t.Setenv(cli.WindowIDEnv, "101")
	client := &fakeOverviewClient{windows: []kitty.OSWindow{{
		Tabs: []kitty.Tab{
			testManagedTab(10, 101, "api"),
			// Neither of these is an overview: one is a different overlay, and
			// the other merely carries the word as an argument.
			{ID: 20, Windows: []kitty.Window{
				{ID: 201, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"amx", "keybindings"}}}},
				{ID: 202, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"amx", "session", "continue", "overview"}}}},
				{ID: 203, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"claude", "overview"}}}},
			}},
		},
	}}}
	cfg := &config.Config{}
	started := installOverviewSeams(t, client, cfg, nil)

	if err := runOverviewCommand(101); err != nil {
		t.Fatalf("runOverviewCommand returned error: %v", err)
	}
	if started.calls != 1 {
		t.Fatalf("surface starts = %d, want 1", started.calls)
	}
	if started.cfg != cfg {
		t.Fatal("surface started with a different config than the one loaded")
	}
	if started.client != client {
		t.Fatal("surface started with a different Kitty client than the command built")
	}
	if started.sourceWindowID != 101 {
		t.Fatalf("source window ID = %d, want 101", started.sourceWindowID)
	}
	if len(client.focusedTabs) != 0 || len(client.focusedWindows) != 0 {
		t.Fatalf("first launch focused something: tabs=%v windows=%v", client.focusedTabs, client.focusedWindows)
	}
	if len(client.userVars) != 0 {
		t.Fatalf("first launch wrote user vars: %v", client.userVars)
	}
	// One listing answers the singleton question; the surface does its own reads
	// once it is running, so a second round trip here would be waste on the path
	// an operator is waiting on.
	if client.listCalls != 1 {
		t.Fatalf("ListWindows calls = %d, want 1", client.listCalls)
	}
}

func TestOverviewFallsBackToTheAmbientKittyWindow(t *testing.T) {
	t.Setenv(cli.WindowIDEnv, "77")
	client := &fakeOverviewClient{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{testManagedTab(10, 77, "api")}}}}
	started := installOverviewSeams(t, client, &config.Config{}, nil)

	for _, supplied := range []int{0, -1} {
		started.calls, started.sourceWindowID = 0, 0
		if err := runOverviewCommand(supplied); err != nil {
			t.Fatalf("runOverviewCommand(%d) returned error: %v", supplied, err)
		}
		if started.sourceWindowID != 77 {
			t.Fatalf("source window ID for %d = %d, want the ambient 77", supplied, started.sourceWindowID)
		}
	}
}

// The launcher asking the question is itself an `amx overview` process, so the
// one window that must never count as an existing instance is the one the
// command is running in.
func TestOverviewDoesNotDetectItsOwnLauncher(t *testing.T) {
	t.Setenv(cli.WindowIDEnv, "555")
	client := &fakeOverviewClient{windows: []kitty.OSWindow{{
		Tabs: []kitty.Tab{{ID: 50, Windows: []kitty.Window{overviewWindow(555)}}},
	}}}
	started := installOverviewSeams(t, client, &config.Config{}, nil)

	if err := runOverviewCommand(0); err != nil {
		t.Fatalf("runOverviewCommand returned error: %v", err)
	}
	if started.calls != 1 {
		t.Fatalf("surface starts = %d, want 1 (self-detection suppressed the launch)", started.calls)
	}
	if len(client.focusedTabs) != 0 {
		t.Fatalf("launcher focused itself: %v", client.focusedTabs)
	}
}

func TestOverviewFocusesAnExistingInstanceWhereverItIs(t *testing.T) {
	overviewTab := kitty.Tab{ID: 88, Windows: []kitty.Window{overviewWindow(880)}}

	tests := map[string][]kitty.OSWindow{
		"another tab in this OS window": {{ID: 1, Tabs: []kitty.Tab{
			testManagedTab(10, 101, "api"),
			overviewTab,
		}}},
		"another OS window": {
			{ID: 1, Tabs: []kitty.Tab{testManagedTab(10, 101, "api")}},
			{ID: 2, Tabs: []kitty.Tab{testManagedTab(80, 801, "web"), overviewTab}},
		},
	}

	for name, windows := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(cli.WindowIDEnv, "101")
			client := &fakeOverviewClient{windows: windows}
			started := installOverviewSeams(t, client, &config.Config{}, nil)

			if err := runOverviewCommand(101); err != nil {
				t.Fatalf("runOverviewCommand returned error: %v", err)
			}
			if started.calls != 0 {
				t.Fatalf("surface starts = %d, want 0 on a repeated invocation", started.calls)
			}
			if len(client.focusedTabs) != 1 || client.focusedTabs[0] != kitty.MatchTabByID(88) {
				t.Fatalf("focused tabs = %v, want overview tab 88", client.focusedTabs)
			}
			if len(client.focusedWindows) != 1 || client.focusedWindows[0] != kitty.MatchWindowByID(880) {
				t.Fatalf("focused windows = %v, want overview window 880", client.focusedWindows)
			}
			// The dashboard hands a running instance a selection token; the
			// overview shows every session at once and must leave the instance's
			// selection and page exactly as the operator left them.
			if len(client.userVars) != 0 {
				t.Fatalf("repeated invocation wrote user vars: %v", client.userVars)
			}
		})
	}
}

func TestOverviewFocusFailureIsReportedWithoutASecondSurface(t *testing.T) {
	focusFailure := errors.New("kitten @ focus-tab: no such tab")

	tests := map[string]struct {
		client *fakeOverviewClient
		want   string
	}{
		"tab focus fails": {
			client: &fakeOverviewClient{focusTabErr: focusFailure},
			want:   "focus existing overview tab 88",
		},
		"window focus fails": {
			client: &fakeOverviewClient{focusWindowErr: focusFailure},
			want:   "focus existing overview window 880",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(cli.WindowIDEnv, "101")
			tt.client.windows = []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 88, Windows: []kitty.Window{overviewWindow(880)}}}}}
			started := installOverviewSeams(t, tt.client, &config.Config{}, nil)

			err := runOverviewCommand(101)
			if err == nil {
				t.Fatal("runOverviewCommand returned nil, want the focus failure reported")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want it to name %q", err, tt.want)
			}
			if !errors.Is(err, focusFailure) {
				t.Fatalf("error = %q, want it to wrap the Kitty failure", err)
			}
			if started.calls != 0 {
				t.Fatalf("surface starts = %d, want 0: a failed focus must not become a duplicate", started.calls)
			}
		})
	}
}

// A listing failure is not evidence that no overview is open, so it is reported
// rather than treated as "none found".
func TestOverviewListFailureIsReportedWithoutASecondSurface(t *testing.T) {
	t.Setenv(cli.WindowIDEnv, "101")
	listFailure := errors.New("kitten @ ls: remote control is disabled")
	client := &fakeOverviewClient{listErr: listFailure}
	started := installOverviewSeams(t, client, &config.Config{}, nil)

	err := runOverviewCommand(101)
	if err == nil {
		t.Fatal("runOverviewCommand returned nil, want the listing failure reported")
	}
	if !errors.Is(err, listFailure) {
		t.Fatalf("error = %q, want it to wrap the listing failure", err)
	}
	if started.calls != 0 {
		t.Fatalf("surface starts = %d, want 0 when detection could not run", started.calls)
	}
}

func TestOverviewConfigFailureStopsTheLaunchButNotAFocus(t *testing.T) {
	configFailure := errors.New("loading config: unreadable")

	t.Run("no instance open", func(t *testing.T) {
		t.Setenv(cli.WindowIDEnv, "101")
		client := &fakeOverviewClient{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{testManagedTab(10, 101, "api")}}}}
		started := installOverviewSeams(t, client, nil, configFailure)

		if err := runOverviewCommand(101); !errors.Is(err, configFailure) {
			t.Fatalf("error = %v, want the config failure", err)
		}
		if started.calls != 0 {
			t.Fatalf("surface starts = %d, want 0 without a runnable config", started.calls)
		}
	})

	t.Run("instance already open", func(t *testing.T) {
		t.Setenv(cli.WindowIDEnv, "101")
		client := &fakeOverviewClient{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 88, Windows: []kitty.Window{overviewWindow(880)}}}}}}
		installOverviewSeams(t, client, nil, configFailure)

		if err := runOverviewCommand(101); err != nil {
			t.Fatalf("focusing an open overview returned %v, want nil even with an unreadable config", err)
		}
		if len(client.focusedTabs) != 1 {
			t.Fatalf("focused tabs = %v, want the existing overview focused", client.focusedTabs)
		}
	})
}

func TestOverviewCommandTakesTheSourceWindowFlagAndNoArguments(t *testing.T) {
	t.Setenv(cli.WindowIDEnv, "101")
	client := &fakeOverviewClient{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{testManagedTab(10, 42, "api")}}}}
	started := installOverviewSeams(t, client, &config.Config{}, nil)

	command := OverviewCommand()
	t.Cleanup(func() {
		command.SetArgs(nil)
		overviewSourceWindowID = 0
	})

	command.SetArgs([]string{"--window-id", "42"})
	if err := command.Execute(); err != nil {
		t.Fatalf("amx overview --window-id 42: %v", err)
	}
	if started.sourceWindowID != 42 {
		t.Fatalf("source window ID = %d, want the flag's 42", started.sourceWindowID)
	}

	command.SetArgs([]string{"api"})
	command.SilenceErrors, command.SilenceUsage = true, true
	t.Cleanup(func() { command.SilenceErrors, command.SilenceUsage = false, false })
	if err := command.Execute(); err == nil {
		t.Fatal("amx overview api returned nil, want a positional-argument refusal")
	}
}

// The binding, the detector, and the command have to name the same thing. If the
// catalog's F2 line ever stops being recognized as this command, F2 would open a
// second overview on every press.
func TestTheF2BindingArgvIsRecognizedAsThisCommand(t *testing.T) {
	var f2 kittykeys.Action
	for _, action := range kittykeys.Actions {
		if action.Key == "f2" {
			f2 = action
		}
	}
	if f2.Key == "" {
		t.Fatal("the shared catalog has no f2 binding")
	}

	fields := strings.Fields(f2.ConfigLine())
	amx := -1
	for i, field := range fields {
		if field == "amx" {
			amx = i
			break
		}
	}
	if amx < 0 {
		t.Fatalf("f2 ConfigLine() = %q, want an amx launch", f2.ConfigLine())
	}

	argv := []string{"/usr/local/bin/amx"}
	for _, field := range fields[amx+1:] {
		if field == "@active-kitty-window-id" {
			// Kitty substitutes the focused window's ID before exec.
			field = "101"
		}
		argv = append(argv, field)
	}

	if argv[1] != OverviewCommand().Name() {
		t.Fatalf("f2 launches %q, want the registered command %q", argv[1], OverviewCommand().Name())
	}
	if OverviewCommand().Flags().Lookup("window-id") == nil {
		t.Fatal("amx overview has no --window-id flag for the binding to pass")
	}

	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 88, Windows: []kitty.Window{{
		ID:                  880,
		ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: argv}},
	}}}}}}
	loc, found := kitty.FindOverviewWindow(windows, 0)
	if !found {
		t.Fatalf("the F2 argv %v is not recognized as a running overview", argv)
	}
	if loc.TabID != 88 || loc.WindowID != 880 {
		t.Fatalf("location = %+v, want tab 88 window 880", loc)
	}
}
