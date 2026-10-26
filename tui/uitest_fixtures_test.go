//go:build tuitest

package tui

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/kitty/kittytest"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// uitestTestdataDir is the repo-root testdata/uitest directory relative to the
// tui package.
func uitestTestdataDir(name string) string {
	return filepath.Join("..", "testdata", "uitest", name)
}

// setenvFunc adapts t.Setenv (which auto-restores at test end) to the
// error-returning callback shape newUITestConfigDriver expects (matching
// os.Setenv for the CLI path).
func setenvFunc(t *testing.T) func(string, string) error {
	return func(k, v string) error {
		t.Setenv(k, v)
		return nil
	}
}

func TestUITestFixtureNamesAreSortedAndKnown(t *testing.T) {
	t.Parallel()

	names := UITestFixtureNames()
	want := []string{"action_feedback", "actions", "asl_question", "asl_runs", "attention_overlap", "basic", "blocked_reasons", "duplicate_names", "family_unread", "health_states", "no_sessions", "paused_output", "paused_queue", "pinned_sessions", "portable_tmux", "preview_failure", "project_families", "run_timers", "setup_profile", "setup_project", "triage_integration", "triage_vertical", "unseen_stops", "wide_profiles", "worktree_branches", "worktree_lifecycle", "worktrees"}
	if len(names) != len(want) {
		t.Fatalf("expected %d fixtures, got %v", len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("fixture names not sorted/complete: got %v, want %v", names, want)
		}
	}
}

func TestTriageIntegrationFixturePinsReleaseMatrix(t *testing.T) {
	t.Parallel()

	fx, err := uitestFixtureByName("triage_integration")
	if err != nil {
		t.Fatalf("triage_integration fixture: %v", err)
	}
	if got := len(fx.Config.Sessions); got != 12 {
		t.Fatalf("session count = %d, want 12", got)
	}
	if got := len(fx.States); got != 12 {
		t.Fatalf("state count = %d, want 12", got)
	}
	if got := len(fx.Families); got != 12 {
		t.Fatalf("family count = %d, want 12", got)
	}

	wantStates := map[int]string{
		11: "error", 21: "key", 31: "question", 41: "stop",
		51: "warning", 61: "bolt", 71: "subagent", 81: "compact",
		91: "", 101: "stop", 111: "legacy", 121: "stop",
	}
	for windowID, want := range wantStates {
		if got := fx.States[windowID].IconState; got != want {
			t.Errorf("window %d state = %q, want %q", windowID, got, want)
		}
	}
	if !fx.States[41].Unseen {
		t.Error("unseen stop rank is absent")
	}
	if fx.States[61].PresentationChangedAtMS == 0 || fx.States[61].PresentationChangedAtMS != fx.States[71].PresentationChangedAtMS {
		t.Error("bolt/subagent equal-timestamp case is absent")
	}
	if fx.States[81].PresentationChangedAtMS != 0 || fx.States[121].PresentationChangedAtMS != 0 {
		t.Error("zero-timestamp cases are absent")
	}
	if fx.States[11].PresentationChangedAtMS == fx.States[21].PresentationChangedAtMS {
		t.Error("distinct-timestamp case is absent")
	}

	workPortal := fx.Families[21]
	personalPortal := fx.Families[31]
	if workPortal.Key == personalPortal.Key || workPortal.Label == personalPortal.Label ||
		!strings.EqualFold(workPortal.Label, personalPortal.Label) {
		t.Fatalf("portal variants do not span distinct families with case-folded labels: %+v %+v", workPortal, personalPortal)
	}
	if fx.Config.Sessions[1].Profile == fx.Config.Sessions[2].Profile {
		t.Fatal("portal variants do not span profiles")
	}
	labels := make(map[string]bool)
	for _, family := range fx.Families {
		labels[strings.ToLower(family.Label)] = true
	}
	if got := len(labels); got < 10 {
		t.Fatalf("canonical label count = %d, want enough to force the switcher to scroll", got)
	}
}

func TestTriageIntegrationFixtureRendersPriorityAndScopeOrder(t *testing.T) {
	t.Parallel()

	fx, err := uitestFixtureByName("triage_integration")
	if err != nil {
		t.Fatalf("triage_integration fixture: %v", err)
	}
	d, _ := newUITestFixtureDriver(fx, UITestSize{Width: 140, Height: 20})
	cleanupUITestDriver(t, d)
	m := d.Model()

	gotSessions := make([]string, len(m.filtered))
	for i := range m.filtered {
		gotSessions[i] = m.filtered[i].EffectiveProject()
	}
	wantSessions := []string{
		"release-guard", "portal-work", "portal-personal", "finished-work",
		"watchtower", "agent-fleet", "build-farm", "compact-ops",
		"quiet-queue", "archive", "legacy-lab", "spare-parts",
	}
	if !reflect.DeepEqual(gotSessions, wantSessions) {
		t.Fatalf("session priority order = %v, want %v", gotSessions, wantSessions)
	}

	scopes := projectScopes(m)
	gotLabels := make([]string, len(scopes))
	for i := range scopes {
		gotLabels[i] = scopes[i].Label
	}
	wantLabels := []string{
		"All projects", "Release Guard", "Client Portal", "Finished Work",
		"Watchtower", "Agent Fleet", "Build Farm", "Compact Ops",
		"Quiet Queue", "Archive", "Legacy Lab", "Spare Parts",
	}
	if !reflect.DeepEqual(gotLabels, wantLabels) {
		t.Fatalf("scope order = %v, want %v", gotLabels, wantLabels)
	}
	// Client Portal is the case-folded merge: two sessions under two profiles
	// reporting the same label in two casings aggregate into one scope.
	if scopes[0].Counts != [3]int{4, 5, 3} || scopes[2].Counts != [3]int{2, 0, 0} {
		t.Fatalf("scope tallies: all=%v client=%v", scopes[0].Counts, scopes[2].Counts)
	}
}

func TestUITestFixtureByNameUnknownReturnsStructuredError(t *testing.T) {
	t.Parallel()

	_, err := uitestFixtureByName("does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown fixture")
	}
	var fe *uitestFixtureError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *uitestFixtureError, got %T: %v", err, err)
	}
	if fe.Name != "does-not-exist" {
		t.Errorf("expected error to name the bad fixture, got %q", fe.Name)
	}
	if len(fe.Known) == 0 {
		t.Error("expected error to list known fixtures")
	}
}

func TestUITestFixtureDriverLoadsSessionsWithoutLiveKitty(t *testing.T) {
	t.Parallel()

	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatalf("basic fixture: %v", err)
	}
	d, client := newUITestFixtureDriver(fx, UITestSize{})
	cleanupUITestDriver(t, d)
	if got := len(d.Model().sessions); got != 3 {
		t.Fatalf("expected 3 loaded sessions, got %d", got)
	}
	// Construction enumerates sessions through ListWindows (a read) only; it
	// must not have attempted any mutating/focus Kitty calls.
	if len(client.Calls) != 0 {
		t.Fatalf("expected no trapped Kitty calls after construction, got %+v", client.Calls)
	}
	obs := d.Observe()
	if obs.Mode != "list" {
		t.Fatalf("expected initial mode list, got %q", obs.Mode)
	}
	if obs.Size != fx.Size {
		t.Fatalf("expected fixture default size %+v, got %+v", fx.Size, obs.Size)
	}
}

func TestUITestFixtureDriverPartialSizeUsesFixtureDefaults(t *testing.T) {
	t.Parallel()

	fx, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatalf("basic fixture: %v", err)
	}
	// The basic fixture defaults to 100x32; each override below supplies one
	// dimension and must inherit the fixture default for the omitted one.
	cases := []struct {
		name string
		in   UITestSize
		want UITestSize
	}{
		{"width only", UITestSize{Width: 140}, UITestSize{Width: 140, Height: fx.Size.Height}},
		{"height only", UITestSize{Height: 48}, UITestSize{Width: fx.Size.Width, Height: 48}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newUITestFixtureDriver(fx, tc.in)
			cleanupUITestDriver(t, d)
			if got := d.Observe().Size; got != tc.want {
				t.Fatalf("partial %+v resolved to %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestUITestFixtureActionsOverlayOpens(t *testing.T) {
	t.Parallel()

	fx, err := uitestFixtureByName("actions")
	if err != nil {
		t.Fatalf("actions fixture: %v", err)
	}
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	cleanupUITestDriver(t, d)
	if err := d.Key("a"); err != nil {
		t.Fatalf("press a: %v", err)
	}
	obs := d.Observe()
	if obs.Mode != "actions" {
		t.Fatalf("expected actions overlay to open, got mode %q", obs.Mode)
	}
	joined := strings.Join(obs.Screen.Lines, "\n")
	if !strings.Contains(joined, "Actions for") {
		t.Fatalf("expected actions overlay header, screen was:\n%s", joined)
	}
}

func TestUITestFixtureConfigPickerOpens(t *testing.T) {
	t.Parallel()

	fx, err := uitestFixtureByName("wide_profiles")
	if err != nil {
		t.Fatalf("wide_profiles fixture: %v", err)
	}
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	cleanupUITestDriver(t, d)
	// `C` is an actions-overlay mnemonic now, not a dashboard key, so the
	// picker is two presses away: `a` to open the overlay against the cursor
	// row, `C` to pick the entry that launches another session in its project.
	if err := d.Key(keyA); err != nil {
		t.Fatalf("press a: %v", err)
	}
	if err := d.Key(keyDashboardLaunch); err != nil {
		t.Fatalf("press C: %v", err)
	}
	obs := d.Observe()
	if obs.Mode != "config_picker" {
		t.Fatalf("expected config picker to open, got mode %q", obs.Mode)
	}
	joined := strings.Join(obs.Screen.Lines, "\n")
	if !strings.Contains(joined, "copilot-enterprise-review-board") {
		t.Fatalf("expected a long profile name in the picker, screen was:\n%s", joined)
	}
}

func TestUITestKittyClientReturnsFixtureWindowsAndRecordsCalls(t *testing.T) {
	t.Parallel()

	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
	)}
	client := kittytest.NewClient(windows)

	got, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(got) != 1 || got[0].Tabs[0].Title != "🦊api" {
		t.Fatalf("expected fixture windows echoed back, got %+v", got)
	}

	if err := client.FocusTab("id:1"); err != nil {
		t.Fatalf("FocusTab: %v", err)
	}
	if err := client.CloseTab("id:1"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}
	if err := client.LaunchTab(kitty.LaunchOpts{Title: "new"}); err != nil {
		t.Fatalf("LaunchTab: %v", err)
	}
	if err := client.SetUserVars("id:11", map[string]string{"amx_profile": "work"}); err != nil {
		t.Fatalf("SetUserVars: %v", err)
	}

	if len(client.Calls) != 4 {
		t.Fatalf("expected 4 recorded calls, got %d: %+v", len(client.Calls), client.Calls)
	}
	if focus := client.CallsFor("FocusTab"); len(focus) != 1 || focus[0].Match != "id:1" {
		t.Fatalf("expected recorded FocusTab id:1, got %+v", focus)
	}
	if launch := client.CallsFor("LaunchTab"); len(launch) != 1 || launch[0].Opts == nil || launch[0].Opts.Title != "new" {
		t.Fatalf("expected recorded LaunchTab with opts, got %+v", launch)
	}
	if vars := client.CallsFor("SetUserVars"); len(vars) != 1 || vars[0].Vars["amx_profile"] != "work" {
		t.Fatalf("expected recorded SetUserVars, got %+v", vars)
	}

	// GetText is a read and returns canned data without recording.
	if text, err := client.GetText("id:11", "screen"); err != nil || text != "" {
		t.Fatalf("expected empty canned GetText, got %q err=%v", text, err)
	}
}

func TestUITestConfigDriverIsolatesUserPaths(t *testing.T) {
	// Ensure the lowercase config override cannot leak a real path through
	// DefaultConfigPath's precedence.
	t.Setenv("amx_CONFIG", "")

	root := t.TempDir()
	d, client, env, err := newUITestConfigDriver(
		root,
		uitestTestdataDir("basic_config.toml"),
		nil,
		UITestSize{Width: 100, Height: 32},
		setenvFunc(t),
	)
	if err != nil {
		t.Fatalf("newUITestConfigDriver: %v", err)
	}
	cleanupUITestDriver(t, d)
	if client == nil || d == nil {
		t.Fatal("expected non-nil driver and client")
	}

	if !strings.HasPrefix(env.Home, root) {
		t.Fatalf("staged HOME %q not under temp root %q", env.Home, root)
	}

	// Every user-path helper must now resolve under the staged, throwaway root.
	if got := config.DefaultConfigPath(); got != env.ConfigPath {
		t.Fatalf("config path not staged: got %q want %q", got, env.ConfigPath)
	}
	ovPath, err := overrides.Path()
	if err != nil {
		t.Fatalf("overrides.Path: %v", err)
	}
	for label, got := range map[string]string{
		"overrides":  ovPath,
		"plans":      plans.IndexPath(),
		"transcript": transcript.IndexPath(),
		"daemon":     ipc.SocketPath(),
	} {
		if !strings.HasPrefix(got, env.Home) {
			t.Fatalf("%s path %q escaped staged HOME %q", label, got, env.Home)
		}
	}

	// The loaded config came from the staged copy, not any real file.
	if got := len(d.Model().cfg.Accounts); got != 2 {
		t.Fatalf("expected 2 profiles from staged config, got %d", got)
	}
	if len(client.Calls) != 0 {
		t.Fatalf("expected no trapped Kitty calls, got %+v", client.Calls)
	}
}

func TestUITestConfigDriverUnknownConfigErrors(t *testing.T) {
	root := t.TempDir()
	_, _, _, err := newUITestConfigDriver(
		root,
		uitestTestdataDir("does_not_exist.toml"),
		nil,
		UITestSize{Width: 80, Height: 24},
		setenvFunc(t),
	)
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "reading config") {
		t.Fatalf("expected reading-config error, got %v", err)
	}
}
