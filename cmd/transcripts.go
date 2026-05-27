package cmd

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

var (
	transcriptsRebuildAll bool

	transcriptsIndexPath  = transcript.IndexPath
	transcriptsLoadConfig = func() (*config.Config, error) {
		return config.LoadDefault()
	}
)

var transcriptsCmd = &cobra.Command{
	Use:   "transcripts",
	Short: "Inspect and maintain the transcript search index",
}

var transcriptsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show transcript index and sidecar status",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := transcript.Status(transcriptsIndexPath())
		if err != nil {
			return err
		}
		printTranscriptStatus(cmd, status)
		return nil
	},
}

var transcriptsCompactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Migrate legacy search text and rewrite metadata-only JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := transcript.Compact(transcriptsIndexPath())
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "compacted transcript index\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "legacy_search_text_entries: %d\n", result.LegacyFields)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_size_before: %d\n", result.StatusBefore.MetadataSize)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_size_after: %d\n", result.StatusAfter.MetadataSize)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sidecar_count: %d\n", result.StatusAfter.SidecarCount)
		return nil
	},
}

var transcriptsRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Refresh transcripts from configured profiles and rebuild sidecars",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := transcriptsLoadConfig()
		if err != nil {
			return err
		}
		result, err := transcript.Rebuild(transcriptsIndexPath(), cfg, transcriptReaders(), transcriptsRebuildAll)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rebuilt transcript sidecars\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_path: %s\n", result.Status.MetadataPath)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_entries: %d\n", result.Status.VisibleEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_entries: %d\n", result.Status.HiddenEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sidecar_count: %d\n", result.Status.SidecarCount)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "missing_sidecars: %d\n", result.Status.MissingSidecars)
		return nil
	},
}

func printTranscriptStatus(cmd *cobra.Command, status transcript.StoreStatus) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "metadata_path: %s\n", status.MetadataPath)
	_, _ = fmt.Fprintf(out, "metadata_size: %d\n", status.MetadataSize)
	_, _ = fmt.Fprintf(out, "sidecar_dir: %s\n", status.SidecarDir)
	_, _ = fmt.Fprintf(out, "sidecar_size: %d\n", status.SidecarSize)
	_, _ = fmt.Fprintf(out, "sidecar_count: %d\n", status.SidecarCount)
	_, _ = fmt.Fprintf(out, "visible_entries: %d\n", status.VisibleEntries)
	_, _ = fmt.Fprintf(out, "hidden_entries: %d\n", status.HiddenEntries)
	_, _ = fmt.Fprintf(out, "missing_sidecars: %d\n", status.MissingSidecars)
}

func transcriptReaders() map[string]transcript.Reader {
	return map[string]transcript.Reader{
		harness.Claude:  transcript.NewClaudeReader(),
		harness.Copilot: transcript.NewCopilotReader(),
		harness.Codex:   transcript.NewCodexReader(),
	}
}

func init() {
	transcriptsRebuildCmd.Flags().BoolVar(&transcriptsRebuildAll, "all", false, "Ignore configured transcript scan age and rebuild every discoverable sidecar")
	transcriptsCmd.AddCommand(transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd)
}
