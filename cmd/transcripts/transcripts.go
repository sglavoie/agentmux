// Package transcripts holds the `amx transcripts` command family: the
// maintenance surface for the disposable transcript metadata cache and its
// search sidecars. Its three leaves report what the store currently holds
// (status), migrate legacy search text and drop orphan sidecars (compact), and
// reparse every discoverable source transcript from the configured profiles
// (rebuild). The source transcript files themselves are never rewritten.
package transcripts

import (
	"fmt"
	"strconv"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

var (
	transcriptsRebuildAll bool

	transcriptsIndexPath  = transcript.IndexPath
	transcriptsLoadConfig = cli.LoadRunnableConfig
	transcriptsReaders    = cli.TranscriptReaders
	transcriptsIsTerminal = cli.IsTerminal
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
		jsonOutput, _ := cmd.Flags().GetBool("json")
		status, err := transcript.Status(transcriptsIndexPath())
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "transcript_index_unavailable", err)
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, storeStatusDataFrom(status))
		}
		printTranscriptStatus(cmd, status)
		return nil
	},
}

var transcriptsCompactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Migrate legacy search text and remove orphan sidecars",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		result, err := transcript.Compact(transcriptsIndexPath())
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "transcript_compact_failed", err)
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, compactData{
				LegacySearchTextEntries:  result.LegacyFields,
				OrphanSidecarsRemoved:    result.RemovedOrphanSidecars,
				OrphanSidecarSizeRemoved: result.RemovedOrphanSidecarSize,
				Before:                   storeStatusDataFrom(result.StatusBefore),
				After:                    storeStatusDataFrom(result.StatusAfter),
				Warnings:                 nonNilWarnings(result.Warnings),
			})
		}
		size := transcriptSizeFormatter(transcriptsIsTerminal(cmd.OutOrStdout()))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "compacted transcript index\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "legacy_search_text_entries: %d\n", result.LegacyFields)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_size_before: %s\n", size(result.StatusBefore.MetadataSize))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_size_after: %s\n", size(result.StatusAfter.MetadataSize))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sidecar_count: %d\n", result.StatusAfter.SidecarCount)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecars_removed: %d\n", result.RemovedOrphanSidecars)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecar_size_removed: %s\n", size(result.RemovedOrphanSidecarSize))
		printTranscriptWarnings(cmd, result.Warnings)
		return nil
	},
}

var transcriptsRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Reparse transcripts from configured profiles and reset sidecars",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		cfg, err := transcriptsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		result, err := transcript.Rebuild(transcriptsIndexPath(), cfg, transcriptsReaders(), transcriptsRebuildAll)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "transcript_rebuild_failed", err)
		}
		if jsonOutput {
			status := storeStatusDataFrom(result.Status)
			status.Warnings = nonNilWarnings(result.Warnings)
			return cli.WriteJSON(cmd, status)
		}
		size := transcriptSizeFormatter(transcriptsIsTerminal(cmd.OutOrStdout()))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rebuilt transcript sidecars\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_path: %s\n", result.Status.MetadataPath)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_entries: %d\n", result.Status.VisibleEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_entries: %d\n", result.Status.HiddenEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "empty_entries: %d\n", result.Status.EmptyEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_profile_memberships: %d\n", result.Status.VisibleProfileMemberships)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_profile_memberships: %d\n", result.Status.HiddenProfileMemberships)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "shared_profile_entries: %d\n", result.Status.SharedProfileEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sidecar_count: %d\n", result.Status.SidecarCount)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecars: %d\n", result.Status.OrphanSidecars)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "orphan_sidecar_size: %s\n", size(result.Status.OrphanSidecarSize))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "missing_sidecars: %d\n", result.Status.MissingSidecars)
		printTranscriptWarnings(cmd, result.Warnings)
		return nil
	},
}

func printTranscriptStatus(cmd *cobra.Command, status transcript.StoreStatus) {
	out := cmd.OutOrStdout()
	human := transcriptsIsTerminal(out)
	size := transcriptSizeFormatter(human)
	path := func(p string) string { return p }
	hiddenNote := ""
	if human {
		path = cli.DisplayPath
		hiddenNote = " (removed from Past Sessions)"
	}
	fields := [][2]string{
		{"metadata_path", path(status.MetadataPath)},
		{"metadata_size", size(status.MetadataSize)},
		{"sidecar_dir", path(status.SidecarDir)},
		{"sidecar_size", size(status.SidecarSize)},
		{"sidecar_count", strconv.Itoa(status.SidecarCount)},
		{"orphan_sidecars", strconv.Itoa(status.OrphanSidecars)},
		{"orphan_sidecar_size", size(status.OrphanSidecarSize)},
		{"visible_entries", strconv.Itoa(status.VisibleEntries)},
		{"hidden_entries", strconv.Itoa(status.HiddenEntries) + hiddenNote},
		{"empty_entries", strconv.Itoa(status.EmptyEntries)},
		{"visible_profile_memberships", strconv.Itoa(status.VisibleProfileMemberships)},
		{"hidden_profile_memberships", strconv.Itoa(status.HiddenProfileMemberships)},
		{"shared_profile_entries", strconv.Itoa(status.SharedProfileEntries)},
		{"missing_sidecars", strconv.Itoa(status.MissingSidecars)},
	}
	// A terminal reader gets the values in one column; pipes keep the single
	// space after the colon that scripts have always split on.
	width := 0
	if human {
		for _, field := range fields {
			width = max(width, len(field[0])+1)
		}
	}
	for _, field := range fields {
		_, _ = fmt.Fprintf(out, "%-*s %s\n", width, field[0]+":", field[1])
	}
	printTranscriptWarnings(cmd, status.Warnings)
}

// transcriptSizeFormatter returns how a byte count is printed: raw bytes for
// pipes and scripts, which have always parsed them, and a binary-unit size for
// a person reading a terminal.
func transcriptSizeFormatter(human bool) func(int64) string {
	if !human {
		return func(v int64) string { return strconv.FormatInt(v, 10) }
	}
	return formatTranscriptBytes
}

func formatTranscriptBytes(v int64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	value, suffix := float64(v)/unit, "KiB"
	for _, next := range []string{"MiB", "GiB", "TiB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, next
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

// storeStatusData is the JSON form of a transcript store status. Its keys are
// the ones the text form prints, so a script can move between the two without
// a lookup table; sizes are bytes.
type storeStatusData struct {
	MetadataPath              string   `json:"metadata_path"`
	MetadataSize              int64    `json:"metadata_size"`
	SidecarDir                string   `json:"sidecar_dir"`
	SidecarSize               int64    `json:"sidecar_size"`
	SidecarCount              int      `json:"sidecar_count"`
	OrphanSidecars            int      `json:"orphan_sidecars"`
	OrphanSidecarSize         int64    `json:"orphan_sidecar_size"`
	VisibleEntries            int      `json:"visible_entries"`
	HiddenEntries             int      `json:"hidden_entries"`
	EmptyEntries              int      `json:"empty_entries"`
	VisibleProfileMemberships int      `json:"visible_profile_memberships"`
	HiddenProfileMemberships  int      `json:"hidden_profile_memberships"`
	SharedProfileEntries      int      `json:"shared_profile_entries"`
	MissingSidecars           int      `json:"missing_sidecars"`
	Warnings                  []string `json:"warnings"`
}

// compactData is the `transcripts compact --json` payload: what compaction
// changed, plus the store as it stood before and after.
type compactData struct {
	LegacySearchTextEntries  int             `json:"legacy_search_text_entries"`
	OrphanSidecarsRemoved    int             `json:"orphan_sidecars_removed"`
	OrphanSidecarSizeRemoved int64           `json:"orphan_sidecar_size_removed"`
	Before                   storeStatusData `json:"before"`
	After                    storeStatusData `json:"after"`
	Warnings                 []string        `json:"warnings"`
}

func storeStatusDataFrom(status transcript.StoreStatus) storeStatusData {
	return storeStatusData{
		MetadataPath:              status.MetadataPath,
		MetadataSize:              status.MetadataSize,
		SidecarDir:                status.SidecarDir,
		SidecarSize:               status.SidecarSize,
		SidecarCount:              status.SidecarCount,
		OrphanSidecars:            status.OrphanSidecars,
		OrphanSidecarSize:         status.OrphanSidecarSize,
		VisibleEntries:            status.VisibleEntries,
		HiddenEntries:             status.HiddenEntries,
		EmptyEntries:              status.EmptyEntries,
		VisibleProfileMemberships: status.VisibleProfileMemberships,
		HiddenProfileMemberships:  status.HiddenProfileMemberships,
		SharedProfileEntries:      status.SharedProfileEntries,
		MissingSidecars:           status.MissingSidecars,
		Warnings:                  nonNilWarnings(status.Warnings),
	}
}

// nonNilWarnings keeps `warnings` an array in JSON, so a consumer can iterate
// it without first checking for null.
func nonNilWarnings(warnings []string) []string {
	if warnings == nil {
		return []string{}
	}
	return warnings
}

func printTranscriptWarnings(cmd *cobra.Command, warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
	}
}

func init() {
	transcriptsRebuildCmd.Flags().BoolVar(&transcriptsRebuildAll, "all", false, "Ignore configured transcript scan age and rebuild every discoverable sidecar")
	for _, command := range []*cobra.Command{transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd} {
		command.Flags().Bool("json", false, "emit schema-v1 JSON")
	}
	transcriptsCmd.AddCommand(transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd)
}
