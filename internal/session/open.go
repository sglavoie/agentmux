package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
)

// resolveEditor picks an editor command. Order:
//
//	cfgEditor → $VISUAL → $EDITOR → "vi"
//
// Splits the chosen value as shell arguments so "code --wait" and quoted
// editor paths both work. Returns (name, args, nil) on success or ("", nil,
// error) if every source is empty or the selected command cannot be parsed.
func resolveEditor(cfgEditor string) (string, []string, error) {
	candidates := []string{
		cfgEditor,
		os.Getenv("VISUAL"),
		os.Getenv("EDITOR"),
		"vi",
	}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		parts, err := splitShellArgs(c)
		if err != nil {
			return "", nil, fmt.Errorf("parsing editor command %q: %w", c, err)
		}
		if len(parts) == 0 {
			continue
		}
		return parts[0], append([]string{}, parts[1:]...), nil
	}
	return "", nil, fmt.Errorf("no editor configured (cfg.Defaults.Editor, $VISUAL, $EDITOR all empty)")
}

// EditorCommand is the configured editor as an argument list, resolved the
// way EditPath resolves it, for a caller that runs the editor in its own terminal.
func EditorCommand(cfg *config.Config) ([]string, error) {
	configured := ""
	if cfg != nil {
		configured = cfg.Defaults.Editor
	}
	name, args, err := resolveEditor(configured)
	if err != nil {
		return nil, err
	}
	return append([]string{name}, args...), nil
}

// EditPath runs the configured editor against one file or directory in the
// current terminal. It waits for the editor to exit, which makes it suitable for workflows
// that must read a user's edits before continuing.
func EditPath(path string, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("editing %q: configuration is required", path)
	}
	name, editorArgs, err := resolveEditor(cfg.Defaults.Editor)
	if err != nil {
		return fmt.Errorf("resolving editor for %q: %w", path, err)
	}
	cmd := exec.Command(name, append(editorArgs, path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editing %q: %w", path, err)
	}
	return nil
}
