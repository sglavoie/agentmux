package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/sglavoie/agentmux/internal/config"
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
		if err := os.MkdirAll(config.DefaultConfigDir(), 0o755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(config.ExampleConfig), 0o644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}
		fmt.Printf("created %s\n", path)
		return nil
	},
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config.toml in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		path := config.DefaultConfigPath()
		c := exec.Command(editor, path)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display config.toml contents",
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := config.ReadDefault()
		if err != nil {
			return fmt.Errorf("reading config: %w", err)
		}
		fmt.Print(string(data))
		return nil
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config.toml path",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(config.DefaultConfigPath())
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate config.toml",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			ccerrors.Print(err)
			return fmt.Errorf("loading config: %w", err)
		}
		if err := config.Validate(cfg); err != nil {
			ccerrors.Print(err)
			return err
		}
		fmt.Println("config is valid")
		return nil
	},
}

var configHooksSyncCmd = &cobra.Command{
	Use:   "hooks-sync [PROFILE]",
	Short: "Sync Agent Mux hooks for configured harness profiles",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			ccerrors.Print(err)
			return fmt.Errorf("loading config: %w", err)
		}
		bin, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolving amx binary: %w", err)
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
		for _, r := range results {
			if r.Err != nil {
				fmt.Fprintf(os.Stderr, "  error (%s): %v\n", r.Account, r.Err)
				continue
			}
			switch r.Status {
			case hooks.MergeCreated:
				fmt.Printf("  created  %s\n", r.Path)
			case hooks.MergeUpdated:
				fmt.Printf("  updated  %s\n", r.Path)
			case hooks.MergeSkipped:
				fmt.Printf("  skipped  %s (already up to date)\n", r.Path)
			}
		}
		return nil
	},
}

var configHooksStatusCmd = &cobra.Command{
	Use:   "hooks-status",
	Short: "Show hooks sync status per profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			ccerrors.Print(err)
			return fmt.Errorf("loading config: %w", err)
		}
		bin, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolving amx binary: %w", err)
		}
		statuses, err := hooks.StatusAll(cfg, bin)
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		fmt.Printf("%-20s %-10s %-10s %s\n", "ACCOUNT", "HARNESS", "STATUS", "PATH")
		needsResync := false
		for _, s := range statuses {
			fmt.Printf("%-20s %-10s %-10s %s\n", s.Account, s.Harness, s.Status, s.Path)
			if s.Status != hooks.SyncInSync {
				needsResync = true
			}
		}
		if needsResync {
			fmt.Println()
			fmt.Println("hint: drifted or missing hooks can strand tab icons; run `amx config hooks-sync` to restore completion events like Stop, TeammateIdle, agentStop, and typed notifications.")
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
