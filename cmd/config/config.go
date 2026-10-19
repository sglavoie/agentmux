// Package config holds the `amx config` command family: the config file's
// lifecycle (init, edit, show, path, profiles, validate — including the $EDITOR field
// splitter and the project-directory audit) and `asl-sync`, the ordered
// preflight-migrate-write-restart-verify pairing with agent-session-loop and
// its structured error codes. It registers cmd/hooks' two maintenance leaves
// (hooks-sync and hooks-status) under itself.
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
	Long: `Validate config.toml and the directory of every configured project.

A config that loads is valid. Unknown and deprecated keys and project
directories that are missing or not directories are printed as warnings,
the way amx doctor reports them, because a project checked out on another
machine is not a broken config. Pass --strict to fail on a project directory
instead.`,
	// Failures are already printed by ccerrors.Print; cobra printing them a
	// second time only repeats the line that matters.
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		strict, _ := cmd.Flags().GetBool("strict")
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			if jsonOutput {
				return cli.CommandFailure(cmd, true, "config_invalid", err)
			}
			ccerrors.Print(err)
			return err
		}
		if jsonOutput {
			return writeConfigValidateJSON(cmd, cfg, strict)
		}
		// Key warnings come first so they are still seen when a strict
		// project directory check fails below.
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
		if strict {
			if err := validateProjectDirectories(cfg.Projects); err != nil {
				ccerrors.Print(err)
				return err
			}
			_, err = fmt.Fprintln(out, "config is valid")
			return err
		}
		issues := config.ProjectDirectoryIssues(cfg.Projects)
		for _, issue := range issues {
			if _, err := fmt.Fprintf(out, "warning: %s\n", issue.Format(cli.DisplayPath(issue.Directory))); err != nil {
				return err
			}
		}
		if len(issues) > 0 {
			_, err = fmt.Fprintln(out, "config is valid; fix or remove the projects above with `amx config edit` (or set `archived = true` on the ones to keep), or pass --strict to fail on them")
			return err
		}
		_, err = fmt.Fprintln(out, "config is valid")
		return err
	},
}

// configValidateData is the `config validate --json` payload. A config that
// loads but names missing project directories is a finding, not a malformed
// request: the directories are always listed, and only under --strict do they
// make Valid false with a nonzero quiet exit. Only a config that cannot be
// loaded at all uses the error envelope.
type configValidateData struct {
	Valid                     bool                   `json:"valid"`
	Path                      string                 `json:"path"`
	UnknownKeys               []string               `json:"unknown_keys"`
	Deprecations              []configDeprecationRow `json:"deprecations"`
	InvalidProjectDirectories []string               `json:"invalid_project_directories"`
}

type configDeprecationRow struct {
	Key         string `json:"key"`
	Location    string `json:"location"`
	Replacement string `json:"replacement"`
}

func writeConfigValidateJSON(cmd *cobra.Command, cfg *config.Config, strict bool) error {
	data := configValidateData{
		Path:                      config.DefaultConfigPath(),
		UnknownKeys:               append([]string{}, cfg.UnknownKeys...),
		Deprecations:              []configDeprecationRow{},
		InvalidProjectDirectories: append([]string{}, config.ProjectDirectoryProblems(cfg.Projects)...),
	}
	for _, d := range cfg.Deprecations {
		data.Deprecations = append(data.Deprecations, configDeprecationRow{Key: d.Key, Location: d.Location, Replacement: d.Replacement})
	}
	data.Valid = !strict || len(data.InvalidProjectDirectories) == 0
	if err := cli.WriteJSON(cmd, data); err != nil {
		return err
	}
	if !data.Valid {
		return &cli.ExitError{Code: 1, Err: errors.New("invalid project directories"), Quiet: true}
	}
	return nil
}

func validateProjectDirectories(projects []config.Session) error {
	issues := config.ProjectDirectoryIssues(projects)
	if len(issues) == 0 {
		return nil
	}
	lines := make([]string, len(issues))
	for i, issue := range issues {
		lines[i] = issue.Format(cli.DisplayPath(issue.Directory))
	}
	return fmt.Errorf("invalid project directories:\n%s\nfix each directory, remove its [[projects]] entry, or set `archived = true` on it, with `amx config edit`",
		strings.Join(lines, "\n"))
}

func init() {
	configValidateCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	configValidateCmd.Flags().Bool("strict", false, "fail when a project directory is missing or not a directory")
	configCmd.AddCommand(
		configInitCmd,
		configSetupCmd,
		configEditCmd,
		configShowCmd,
		configPathCmd,
		configValidateCmd,
		hooks.SyncCommand(),
		hooks.StatusCommand(),
	)
}
