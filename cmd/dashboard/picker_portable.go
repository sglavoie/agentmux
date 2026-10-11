package dashboard

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/customization"
	"github.com/spf13/cobra"
)

// Outside Kitty the two pickers are portable views: they open in the terminal
// they are typed in and edit a tmux session instead of a Kitty tab. The icon
// is part of the session's title, which every surface that lists the session
// shows. The color is kept in the session's metadata (session.ColorVar), and
// the dashboard draws the session's rows in it. Inside Kitty nothing changes:
// the pickers stay Kitty overlays over the tab they were opened on.

// pickerRuntimeSessionFlag names the tmux session a picker edits.
const pickerRuntimeSessionFlag = "runtime-session"

func addPickerSessionFlag(cmd *cobra.Command) {
	cmd.Flags().String(pickerRuntimeSessionFlag, "", "outside Kitty: the runtime session whose tmux session to edit (default: $"+runtimesession.EnvVar+", or the only one running)")
}

// pickerTarget is the tmux session a portable picker edits.
type pickerTarget struct {
	terminal backend.Backend
	pane     backend.Pane
	running  session.RunningSession
}

// portablePickerRequested reports whether a picker should edit a tmux session
// instead of a Kitty tab: always outside Kitty, and inside Kitty only when a
// session was named.
func portablePickerRequested(cmd *cobra.Command) bool {
	named, _ := cmd.Flags().GetString(pickerRuntimeSessionFlag)
	return strings.TrimSpace(named) != "" || !insideKitty()
}

// resolvePickerTarget finds the session a portable picker edits: the one
// named by the flag, else the one this command is typed in, else the only one
// running. Several candidates and no way to choose is an error that lists
// them; it never guesses.
func resolvePickerTarget(cmd *cobra.Command, cfg *config.Config) (pickerTarget, error) {
	named, _ := cmd.Flags().GetString(pickerRuntimeSessionFlag)
	named = strings.TrimSpace(named)
	source := "--" + pickerRuntimeSessionFlag
	if named == "" {
		named, source = strings.TrimSpace(os.Getenv(runtimesession.EnvVar)), "$"+runtimesession.EnvVar
	}
	var want runtimesession.ID
	if named != "" {
		id, err := runtimesession.ParseID(named)
		if err != nil {
			return pickerTarget{}, fmt.Errorf("%s: %q is not a runtime session ID", source, named)
		}
		want = id
	}
	terminal, err := openTmux()
	if err != nil {
		return pickerTarget{}, fmt.Errorf("%s edits a tmux session outside Kitty, and tmux is unavailable: %w", cmd.Name(), err)
	}
	panes, err := terminal.Discover()
	if err != nil {
		return pickerTarget{}, fmt.Errorf("list tmux sessions: %w", err)
	}
	running := session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg)
	sort.Slice(running, func(i, j int) bool { return running[i].RuntimeSessionID < running[j].RuntimeSessionID })
	pick := func(rs session.RunningSession) (pickerTarget, error) {
		pane, ok := backend.FindPane(panes, rs.PaneRef())
		if !ok {
			return pickerTarget{}, fmt.Errorf("tmux session %s has no pane", rs.RuntimeSessionID)
		}
		return pickerTarget{terminal: terminal, pane: pane, running: rs}, nil
	}
	if want != "" {
		for _, rs := range running {
			if rs.RuntimeSessionID == want {
				return pick(rs)
			}
		}
		return pickerTarget{}, fmt.Errorf("%s: no running tmux session is %s", source, want)
	}
	switch len(running) {
	case 0:
		return pickerTarget{}, errors.New("no tmux session is running, so there is nothing to edit")
	case 1:
		return pick(running[0])
	}
	var list strings.Builder
	for _, rs := range running {
		fmt.Fprintf(&list, "\n  %s  %s (%s)", rs.RuntimeSessionID, rs.Project, rs.EffectiveProfile())
	}
	return pickerTarget{}, fmt.Errorf("%d tmux sessions are running; name one with --%s:%s", len(running), pickerRuntimeSessionFlag, list.String())
}

// sessionSubject names a session in a picker's header.
func sessionSubject(rs session.RunningSession) string {
	if rs.Project != "" {
		return rs.Project
	}
	return rs.Base
}

// runPortableColorPicker opens the color picker in this terminal for a tmux
// session. The pick is written to the session's metadata; resetting removes
// it, which is the profile's own color again.
func runPortableColorPicker(cmd *cobra.Command) error {
	cfg, err := loadPickerConfig()
	if err != nil {
		return fmt.Errorf("load Agent Mux config: %w", err)
	}
	target, err := resolvePickerTarget(cmd, cfg)
	if err != nil {
		return err
	}
	defaultColor := "gray"
	for _, profile := range cfg.Accounts {
		if strings.EqualFold(profile.Name, target.running.EffectiveProfile()) && profile.Color != "" {
			defaultColor = profile.Color
		}
	}
	return runColorPickerOverlay(customization.ColorContext{
		DefaultColor: defaultColor,
		Subject:      sessionSubject(target.running),
		Apply: func(colorName string) error {
			return target.terminal.SetMetadata(target.pane.Ref, map[string]string{session.ColorVar: colorName})
		},
	})
}

// runPortableIconPicker opens the harness icon picker in this terminal for a
// tmux session and writes the choice into the session's title.
func runPortableIconPicker(cmd *cobra.Command) error {
	cfg, err := loadIconPickerConfig()
	if err != nil {
		return fmt.Errorf("load Agent Mux config: %w", err)
	}
	target, err := resolvePickerTarget(cmd, cfg)
	if err != nil {
		return err
	}
	return runIconPickerOverlay(customization.IconContext{
		CurrentTitle:  target.pane.GroupTitle,
		HarnessEmojis: cfg.HarnessEmojis,
		SetTitle: func(title string) error {
			return target.terminal.SetTitle(target.pane.Group, title)
		},
	})
}
