package dashboard

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/keybindings"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/customization"
	tuidashboard "github.com/sglavoie/agentmux/tui/dashboard"
)

// These run the pickers and the dashboard against a real private tmux server.

type portableWorld struct {
	t      *testing.T
	tmux   *tmuxtest.Fixture
	cfg    *config.Config
	ids    map[string]runtimesession.ID
	colors []customization.ColorContext
	icons  []customization.IconContext
}

func newPortableWorld(t *testing.T, titles ...string) *portableWorld {
	t.Helper()
	w := &portableWorld{t: t, tmux: tmuxtest.New(t), ids: map[string]runtimesession.ID{}}
	w.cfg = &config.Config{
		HarnessEmojis: map[string]string{harness.Claude: "🦊", harness.Codex: "🤖"},
		Accounts:      []config.Account{{Name: "work", Harness: harness.Claude, Color: "blue"}},
	}
	for _, title := range titles {
		project := config.Session{Title: title, Directory: t.TempDir(), Profile: "work"}
		w.cfg.Sessions = append(w.cfg.Sessions, project)
		launched, err := session.LaunchOn(session.Runtime{Tmux: w.tmux.Backend, Registry: w.tmux.Registry}, project, w.cfg.Accounts[0], w.cfg, session.LaunchPolicyAlwaysNewInstance)
		if err != nil {
			t.Fatalf("launch %s: %v", title, err)
		}
		w.ids[title] = launched.RuntimeSessionID
	}

	restore := []func(){}
	t.Cleanup(func() {
		for _, undo := range restore {
			undo()
		}
	})
	swap := func(undo func()) { restore = append(restore, undo) }
	oldOpen := openTmux
	openTmux = func() (backend.TargetLauncher, error) { return w.tmux.Backend, nil }
	swap(func() { openTmux = oldOpen })
	oldPickerConfig, oldIconConfig := loadPickerConfig, loadIconPickerConfig
	loadPickerConfig = func() (*config.Config, error) { return w.cfg, nil }
	loadIconPickerConfig = loadPickerConfig
	swap(func() { loadPickerConfig, loadIconPickerConfig = oldPickerConfig, oldIconConfig })
	// The overlay seams stand in for the operator: they record the view that
	// would have opened. A test then makes the choice through the view's own
	// hooks, which is all the real picker does on Enter.
	oldColor, oldIcon := runColorPickerOverlay, runIconPickerOverlay
	runColorPickerOverlay = func(ctx customization.ColorContext) error { w.colors = append(w.colors, ctx); return nil }
	runIconPickerOverlay = func(ctx customization.IconContext) error { w.icons = append(w.icons, ctx); return nil }
	swap(func() { runColorPickerOverlay, runIconPickerOverlay = oldColor, oldIcon })
	t.Setenv(runtimesession.EnvVar, "")
	return w
}

func (w *portableWorld) run(command *cobra.Command, runtime string) error {
	w.t.Helper()
	if err := command.Flags().Set(pickerRuntimeSessionFlag, runtime); err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = command.Flags().Set(pickerRuntimeSessionFlag, "") })
	return command.RunE(command, nil)
}

func (w *portableWorld) sessions() map[string]session.RunningSession {
	w.t.Helper()
	panes, err := w.tmux.Backend.Discover()
	if err != nil {
		w.t.Fatal(err)
	}
	byTitle := map[string]session.RunningSession{}
	for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, w.cfg) {
		for title, id := range w.ids {
			if rs.RuntimeSessionID == id {
				byTitle[title] = rs
			}
		}
	}
	return byTitle
}

// The color picker opens for a tmux session and its pick lands
// in that session's metadata, where the dashboard reads the row's color from.
func TestColorPickerOpensAsAPortableViewForATmuxSession(t *testing.T) {
	w := newPortableWorld(t, "api", "web")
	if err := w.run(ColorPickerCommand(), string(w.ids["api"])); err != nil {
		t.Fatalf("color-picker: %v", err)
	}
	if len(w.colors) != 1 {
		t.Fatalf("%d color pickers opened; want 1", len(w.colors))
	}
	view := w.colors[0]
	if view.Apply == nil || view.DefaultColor != "blue" || view.Subject != "api" {
		t.Fatalf("picker = apply %v default %q subject %q; want a picker for api defaulting to the profile color", view.Apply != nil, view.DefaultColor, view.Subject)
	}

	if err := view.Apply("purple"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	running := w.sessions()
	// web keeps the color its launch recorded: its profile's.
	if running["api"].Color != "purple" || running["web"].Color != "blue" {
		t.Fatalf("colors = api %q web %q; want purple on api only", running["api"].Color, running["web"].Color)
	}
	profiles := tuidashboard.ProfileColorMap(w.cfg)
	if got := tuidashboard.ProjectIdentityColor(running["api"], profiles, w.cfg); got != "purple" {
		t.Fatalf("dashboard color of api = %q; want the picked purple", got)
	}
	if got := tuidashboard.ProjectIdentityColor(running["web"], profiles, w.cfg); got != "blue" {
		t.Fatalf("dashboard color of web = %q; want its profile's blue", got)
	}

	// Reset is the absence of a pick.
	if err := view.Apply(""); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := w.sessions()["api"]; got.Color != "" || tuidashboard.ProjectIdentityColor(got, profiles, w.cfg) != "blue" {
		t.Fatalf("after reset api has color %q; want the profile color again", got.Color)
	}
}

// Without a desktop workspace the icon picker opens for a tmux session and writes the
// choice into that session's title, and no other's.
func TestIconPickerOpensAsAPortableViewForATmuxSession(t *testing.T) {
	w := newPortableWorld(t, "api", "web")
	before := w.sessions()
	if err := w.run(IconPickerCommand(), string(w.ids["web"])); err != nil {
		t.Fatalf("icon-picker: %v", err)
	}
	if len(w.icons) != 1 {
		t.Fatalf("%d icon pickers opened; want 1", len(w.icons))
	}
	view := w.icons[0]
	if view.SetTitle == nil || view.CurrentTitle != before["web"].Title || view.HarnessEmojis[harness.Codex] != "🤖" {
		t.Fatalf("picker = %+v; want a picker over web's title %q", view, before["web"].Title)
	}
	if err := view.SetTitle("🤖web"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	after := w.sessions()
	if after["web"].Title != "🤖web" {
		t.Fatalf("web title = %q; want the picked icon", after["web"].Title)
	}
	if after["api"].Title != before["api"].Title {
		t.Fatalf("api title changed to %q", after["api"].Title)
	}
	if after["web"].RuntimeSessionID != before["web"].RuntimeSessionID || after["web"].PaneRef() != before["web"].PaneRef() {
		t.Fatal("editing the title changed which session it is")
	}
}

// The session is the one named, else the one the command is typed in, else
// the only one there is. With several and nothing to choose by, the picker
// lists them and opens nothing.
func TestPortablePickersChooseTheirSessionWithoutGuessing(t *testing.T) {
	for _, command := range []*cobra.Command{ColorPickerCommand(), IconPickerCommand()} {
		t.Run(command.Name(), func(t *testing.T) {
			w := newPortableWorld(t, "api", "web")
			opened := func() int { return len(w.colors) + len(w.icons) }

			err := w.run(command, "")
			if err == nil || !strings.Contains(err.Error(), "2 tmux sessions are running") ||
				!strings.Contains(err.Error(), string(w.ids["api"])) || !strings.Contains(err.Error(), string(w.ids["web"])) {
				t.Fatalf("with two sessions and none named: %v; want both listed", err)
			}
			if err := w.run(command, "rs-nope"); err == nil || !strings.Contains(err.Error(), "not a runtime session ID") {
				t.Fatalf("malformed session: %v", err)
			}
			if err := w.run(command, "rs-ffffffffffffffffffffffffffffffff"); err == nil || !strings.Contains(err.Error(), "no running tmux session") {
				t.Fatalf("unknown session: %v", err)
			}
			if opened() != 0 {
				t.Fatalf("%d pickers opened on a refused target", opened())
			}

			// Typed in a session: that session.
			t.Setenv(runtimesession.EnvVar, string(w.ids["web"]))
			if err := w.run(command, ""); err != nil || opened() != 1 {
				t.Fatalf("typed in web: %v, %d opened", err, opened())
			}
		})
		t.Run(command.Name()+" with one session", func(t *testing.T) {
			w := newPortableWorld(t, "api")
			if err := w.run(command, ""); err != nil || len(w.colors)+len(w.icons) != 1 {
				t.Fatalf("with the only session: %v", err)
			}
		})
		t.Run(command.Name()+" with no session", func(t *testing.T) {
			w := newPortableWorld(t)
			if err := w.run(command, ""); err == nil || !strings.Contains(err.Error(), "no tmux session is running") {
				t.Fatalf("with no session: %v", err)
			}
		})
	}
}

// The dashboard opens in this terminal on the tmux backend. Each invocation
// creates a view without looking for another dashboard.
func TestDashboardOpensInThisTerminalOnTmux(t *testing.T) {
	w := newPortableWorld(t, "api")
	oldLoad, oldRun := loadRunnableConfig, runDashboard
	t.Cleanup(func() { loadRunnableConfig, runDashboard = oldLoad, oldRun })
	loadRunnableConfig = func() (*config.Config, error) { return w.cfg, nil }
	var runs []int
	runDashboard = func(_ *config.Config, source int) error {
		runs = append(runs, source)
		return nil
	}

	// Twice: a second invocation is a second view, not a search for the first.
	for range 2 {
		if err := Run("", View{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	if len(runs) != 2 {
		t.Fatalf("%d dashboards started; want one per invocation", len(runs))
	}
	for _, source := range runs {
		if source != 0 {
			t.Fatalf("dashboard started with selection %d; want zero", source)
		}
	}
}

// The keybinding reference opens in this terminal without
// looking for another, and lists the keys the tmux configuration binds.
func TestKeybindingsOpenInThisTerminalOnTmux(t *testing.T) {
	newPortableWorld(t)
	old := runKeybindingsReference
	t.Cleanup(func() { runKeybindingsReference = old })
	var shown [][]keybindings.Action
	runKeybindingsReference = func(actions []keybindings.Action) error {
		shown = append(shown, actions)
		return nil
	}
	if err := KeybindingsCommand().RunE(KeybindingsCommand(), nil); err != nil || len(shown) != 1 {
		t.Fatalf("keybindings: %v, opened %d times", err, len(shown))
	}
	if len(shown[0]) != len(keybindings.Tmux) || shown[0][0] != keybindings.Tmux[0] {
		t.Fatalf("the reference lists %d keys; want the tmux table", len(shown[0]))
	}
}

func TestDashboardLaunchDestinationAndSourceFollowConfigurationAndFlag(t *testing.T) {
	w := newPortableWorld(t, "api")
	oldLoad, oldRun := loadRunnableConfig, runDashboard
	t.Cleanup(func() { loadRunnableConfig, runDashboard = oldLoad, oldRun })
	loadRunnableConfig = func() (*config.Config, error) { return w.cfg, nil }
	runDashboard = func(_ *config.Config, _ int) error {
		if commands.Portable.Launch != runtimesession.BackendTmux {
			t.Fatal("wrong launch destination")
		}
		return nil
	}
	w.cfg.Defaults.LaunchBackend = "tmux"
	if err := Run("", View{}); err != nil {
		t.Fatal(err)
	}
	if commands.Portable.LaunchSource != "config.toml" {
		t.Fatal(commands.Portable.LaunchSource)
	}
	w.cfg.Defaults.LaunchBackend = "unsupported"
	if err := Run("tmux", View{}); err != nil {
		t.Fatal(err)
	}
	if commands.Portable.LaunchSource != "--backend" {
		t.Fatal(commands.Portable.LaunchSource)
	}
}
