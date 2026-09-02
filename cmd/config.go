package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/debugger"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/spf13/cobra"
)

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
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		if err := validateProjectDirectories(cfg.Projects); err != nil {
			ccerrors.Print(err)
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "config is valid")
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

var configHooksSyncCmd = &cobra.Command{
	Use:   "hooks-sync [PROFILE]",
	Short: "Sync Agent Mux hooks for configured harness profiles",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		bin, err := cli.AmxBinary()
		if err != nil {
			return err
		}
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		results, err := hooks.MergeAll(cfg, filter, bin)
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		var failed []string
		for _, r := range results {
			if r.Err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  error (%s): %v\n", r.Account, r.Err)
				failed = append(failed, r.Account)
				continue
			}
			switch r.Status {
			case hooks.MergeCreated:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  created  %s\n", r.Path)
			case hooks.MergeUpdated:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  updated  %s\n", r.Path)
			case hooks.MergeSkipped:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  skipped  %s (already up to date)\n", r.Path)
			}
		}
		// Surface partial-failure scenarios with a non-zero exit code so callers
		// (humans and CI) notice that one or more profiles' hooks were not
		// updated. Previously the per-account error was only printed to stderr
		// and the process exited 0, masking the failure.
		if len(failed) > 0 {
			return fmt.Errorf("hooks-sync failed for %d profile(s): %s", len(failed), strings.Join(failed, ", "))
		}
		return nil
	},
}

var configHooksStatusCmd = &cobra.Command{
	Use:   "hooks-status",
	Short: "Show hook sync and activation status per profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		bin, err := cli.AmxBinary()
		if err != nil {
			return err
		}
		report := debugger.InspectHooks(cfg, bin, time.Now())
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		if _, err := fmt.Fprintf(out, "%-20s %-10s %-10s %-12s %s\n", "ACCOUNT", "HARNESS", "SYNC", "ACTIVATION", "DETAIL"); err != nil {
			return err
		}
		needsAttention := false
		for _, s := range report.Profiles {
			if s.CheckError != "" {
				if _, err := fmt.Fprintf(out, "%-20s %-10s %-10s %-12s %s\n", s.Profile, s.Harness, "error", hooks.ActivationFailure, s.CheckError); err != nil {
					return err
				}
				needsAttention = true
				continue
			}
			if _, err := fmt.Fprintf(out, "%-20s %-10s %-10s %-12s %s\n", s.Profile, s.Harness, s.Sync, s.Activation, s.Detail()); err != nil {
				return err
			}
			if s.Verdict != debugger.VerdictHealthy {
				needsAttention = true
			}
		}
		if report.Error != "" {
			if _, err := fmt.Fprintln(errOut); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(errOut, "error: %s\n", report.Error); err != nil {
				return err
			}
		}
		if needsAttention {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, "hint: run `amx config hooks-sync` for drifted or missing files; activation details identify disabling settings, launch flags, and trust records, while CLI-version or last-seen gaps remain unverified."); err != nil {
				return err
			}
		}
		if report.Error != "" {
			return fmt.Errorf("%s", report.Error)
		}
		return nil
	},
}

func init() {
	configCmd.AddCommand(
		configInitCmd,
		configEditCmd,
		configShowCmd,
		configPathCmd,
		configValidateCmd,
		configHooksSyncCmd,
		configHooksStatusCmd,
	)
}
