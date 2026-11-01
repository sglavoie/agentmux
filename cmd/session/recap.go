package session

import (
	"context"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/recap"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

var sessionRecapService = func(profile config.Account, model string) recap.Service {
	return recap.Service{Generator: recap.Claude{Home: config.ExpandTilde(profile.HomeDir), Model: model}}
}

var sessionRecapCmd = &cobra.Command{
	Use:   "recap <harness>:<session-id>",
	Short: "Generate or read a saved conversation recap",
	Long: `Generates a source-linked goal, latest outcome, and unfinished-work recap.
Uses the first configured Claude profile (or defaults.recaps.profile), for
conversations from any harness. Generation uses that profile's service/quota.
Tools, hooks, customizations and session persistence are disabled. Only a
bounded selection of user/assistant prose is sent. Unchanged recaps are reused.
--cached reads without starting a model; --refresh retries or regenerates.
Set [defaults.recaps] enabled = false to stop generation.`,
	ValidArgsFunction: completeConversation,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		cached, _ := cmd.Flags().GetBool("cached")
		force, _ := cmd.Flags().GetBool("refresh")
		if cached && force {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("--cached and --refresh cannot be combined"))
		}
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "recap", args)
		if err != nil {
			return err
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		readers := sessionCommandsSessionReaders()
		meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, readers, identity)
		if err != nil {
			return err
		}
		profile, profileErr := cfg.RecapProfile()
		model := cfg.Defaults.Recaps.Model
		if model == "" {
			model = "haiku"
		}
		reader, ok := readers[meta.Harness]
		if !ok {
			return cli.CommandFailure(cmd, jsonOutput, "command_failed", fmt.Errorf("no transcript reader for %s", meta.Harness))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
		defer cancel()
		report, err := sessionRecapService(profile, model).Get(ctx, recap.Request{
			Conversation: meta.Harness + ":" + meta.ID, Path: meta.Path,
			Profile: profile.Name, Home: config.ExpandTilde(profile.HomeDir), Model: model,
			Enabled: cfg.Defaults.Recaps.EnabledValue(), CachedOnly: cached, Force: force,
			Load: func() ([]transcript.Message, error) { return reader.ReadMessages(meta.Path) },
		})
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "recap_failed", err)
		}
		if profileErr != nil && report.Status == "unconfigured" {
			report.Warning = profileErr.Error()
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, report)
		}
		if report.Recap != nil {
			for _, field := range []struct{ name, text string }{{"Goal", report.Recap.Goal.Text}, {"Latest", report.Recap.Latest.Text}, {"Outstanding", report.Recap.Outstanding.Text}} {
				if field.text != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", field.name, field.text)
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated %s · %s · %s (%s)\n", report.Recap.GeneratedAt.Format(time.RFC3339), report.Recap.Profile, report.Recap.Model, report.Status)
		} else {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Recap:", report.Status)
		}
		if report.Warning != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), report.Warning)
		}
		return nil
	},
}

func init() {
	cli.AddJSONFlag(sessionRecapCmd, nil)
	sessionRecapCmd.Flags().Bool("cached", false, "read the saved recap without generating one")
	sessionRecapCmd.Flags().Bool("refresh", false, "regenerate even an unchanged recap, or retry a failed request")
	sessionCmd.AddCommand(sessionRecapCmd)
}
