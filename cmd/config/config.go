package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/sglavoie/agentmux/cmd/hooks"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/spf13/cobra"
)

// Command returns the `config` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return configCmd }

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage amx configuration",
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a starter config.toml",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := config.DefaultConfigPath()
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists at %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(config.ExampleConfig), 0o644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", path)
		return err
	},
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config.toml in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := config.DefaultConfigPath()
		name, editorArgs, err := resolveEditorCommand(os.Getenv("EDITOR"), path)
		if err != nil {
			return err
		}
		c := exec.Command(name, editorArgs...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

// resolveEditorCommand turns the user's $EDITOR value into a program name and
// argument list suitable for exec.Command, with the target file path appended.
//
// $EDITOR commonly includes flags (e.g. "code --wait", "emacs -nw"). It can
// also include quoted arguments (e.g. `vim -c "set ft=toml"`), so use a small
// shell-style field splitter instead of strings.Fields.
func resolveEditorCommand(editor, path string) (string, []string, error) {
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
	}
	parts, err := splitEditorFields(editor)
	if err != nil {
		return "", nil, err
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("invalid EDITOR value: %q", editor)
	}
	editorArgs := append([]string{}, parts[1:]...)
	editorArgs = append(editorArgs, path)
	return parts[0], editorArgs, nil
}

func splitEditorFields(s string) ([]string, error) {
	var (
		fields       []string
		b            strings.Builder
		quote        rune
		escaping     bool
		fieldStarted bool
	)

	flush := func() {
		if !fieldStarted {
			return
		}
		fields = append(fields, b.String())
		b.Reset()
		fieldStarted = false
	}

	for _, r := range s {
		if escaping {
			b.WriteRune(r)
			escaping = false
			fieldStarted = true
			continue
		}
		if quote != 0 {
			switch r {
			case '\\':
				if quote == '"' {
					escaping = true
				} else {
					b.WriteRune(r)
				}
			case quote:
				quote = 0
			default:
				b.WriteRune(r)
			}
			fieldStarted = true
			continue
		}
		switch {
		case r == '\\':
			escaping = true
			fieldStarted = true
		case r == '\'' || r == '"':
			quote = r
			fieldStarted = true
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
			fieldStarted = true
		}
	}
	if escaping {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("invalid EDITOR value %q: unterminated quote", s)
	}
	flush()
	return fields, nil
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display config.toml contents",
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := config.ReadDefault()
		if err != nil {
			return fmt.Errorf("reading config: %w", err)
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
		return err
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config.toml path",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), config.DefaultConfigPath())
		return err
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate config.toml and project directories",
	// Failures are already printed by ccerrors.Print; cobra printing them a
	// second time only repeats the line that matters.
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		// Warnings come first so they are still seen when a project directory
		// check fails below.
		out := cmd.OutOrStdout()
		for _, key := range cfg.UnknownKeys {
			if _, err := fmt.Fprintf(out, "warning: unknown key %s (ignored; check its spelling and section)\n", key); err != nil {
				return err
			}
		}
		for _, d := range cfg.Deprecations {
			if _, err := fmt.Fprintf(out, "warning: deprecated %s in %s; use %s\n", d.Key, d.Location, d.Replacement); err != nil {
				return err
			}
		}
		if err := validateProjectDirectories(cfg.Projects); err != nil {
			ccerrors.Print(err)
			return err
		}
		_, err = fmt.Fprintln(out, "config is valid")
		return err
	},
}

func validateProjectDirectories(projects []config.Session) error {
	var errs []string
	for _, project := range projects {
		configuredPath := project.Directory
		info, err := os.Stat(config.ExpandTilde(configuredPath))
		if err != nil {
			var reason string
			if errors.Is(err, os.ErrNotExist) {
				reason = "does not exist"
			} else {
				var pathErr *os.PathError
				if errors.As(err, &pathErr) {
					reason = fmt.Sprintf("cannot be inspected: %v", pathErr.Err)
				} else {
					reason = fmt.Sprintf("cannot be inspected: %v", err)
				}
			}
			errs = append(errs, fmt.Sprintf(
				"project %q: directory %q %s",
				project.Title, configuredPath, reason,
			))
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Sprintf(
				"project %q: directory %q is not a directory",
				project.Title, configuredPath,
			))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid project directories:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

func init() {
	configCmd.AddCommand(
		configInitCmd,
		configEditCmd,
		configShowCmd,
		configPathCmd,
		configValidateCmd,
		hooks.SyncCommand(),
		hooks.StatusCommand(),
	)
}
