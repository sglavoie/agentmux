// Package transcripts holds the `amx transcripts` command family: the
// maintenance surface for the disposable transcript metadata cache and its
// search sidecars. Its three leaves report what the store currently holds
// (status), migrate legacy search text and drop orphan sidecars (compact), and
// reparse every discoverable source transcript from the configured profiles
// (rebuild). The source transcript files themselves are never rewritten.
package transcripts

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

var (
	transcriptsRebuildAll bool

	transcriptsIndexPath  = transcript.IndexPath
	transcriptsLoadConfig = cli.LoadRunnableConfig
	transcriptsReaders    = cli.TranscriptReaders
)

// Command returns the `transcripts` command tree so the root command can
// register it. Root assembly is the only consumer.
func Command() *cobra.Command { return transcriptsCmd }

var transcriptsCmd = &cobra.Command{
	Use:   "transcripts",
	Short: "Inspect and maintain the transcript search index",
}

var transcriptsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show transcript index, sidecar, and orphan usage",
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
	Short: "Migrate legacy search text and remove orphan sidecars",
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
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecars_removed: %d\n", result.RemovedOrphanSidecars)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecar_size_removed: %d\n", result.RemovedOrphanSidecarSize)
		printTranscriptWarnings(cmd, result.Warnings)
		return nil
	},
}

var transcriptsRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Reparse transcripts from configured profiles and reset sidecars",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := transcriptsLoadConfig()
		if err != nil {
			return err
		}
		result, err := transcript.Rebuild(transcriptsIndexPath(), cfg, transcriptsReaders(), transcriptsRebuildAll)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rebuilt transcript sidecars\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_path: %s\n", result.Status.MetadataPath)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_entries: %d\n", result.Status.VisibleEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_entries: %d\n", result.Status.HiddenEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_profile_memberships: %d\n", result.Status.VisibleProfileMemberships)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_profile_memberships: %d\n", result.Status.HiddenProfileMemberships)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "shared_profile_entries: %d\n", result.Status.SharedProfileEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sidecar_count: %d\n", result.Status.SidecarCount)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecars: %d\n", result.Status.OrphanSidecars)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecar_size: %d\n", result.Status.OrphanSidecarSize)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "missing_sidecars: %d\n", result.Status.MissingSidecars)
		printTranscriptWarnings(cmd, result.Warnings)
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
	_, _ = fmt.Fprintf(out, "orphan_sidecars: %d\n", status.OrphanSidecars)
	_, _ = fmt.Fprintf(out, "orphan_sidecar_size: %d\n", status.OrphanSidecarSize)
	_, _ = fmt.Fprintf(out, "visible_entries: %d\n", status.VisibleEntries)
	_, _ = fmt.Fprintf(out, "hidden_entries: %d\n", status.HiddenEntries)
	_, _ = fmt.Fprintf(out, "visible_profile_memberships: %d\n", status.VisibleProfileMemberships)
	_, _ = fmt.Fprintf(out, "hidden_profile_memberships: %d\n", status.HiddenProfileMemberships)
	_, _ = fmt.Fprintf(out, "shared_profile_entries: %d\n", status.SharedProfileEntries)
	_, _ = fmt.Fprintf(out, "missing_sidecars: %d\n", status.MissingSidecars)
	printTranscriptWarnings(cmd, status.Warnings)
}

func printTranscriptWarnings(cmd *cobra.Command, warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
	}
}

func init() {
	transcriptsRebuildCmd.Flags().BoolVar(&transcriptsRebuildAll, "all", false, "Ignore configured transcript scan age and rebuild every discoverable sidecar")
	transcriptsCmd.AddCommand(transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd)
}
