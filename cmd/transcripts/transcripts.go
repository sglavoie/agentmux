// Package transcripts holds the `amx transcripts` command family: the
// maintenance surface for the disposable transcript metadata cache and its
// search sidecars. Its three leaves report what the store currently holds
// (status), migrate legacy search text and drop orphan sidecars (compact), and
// reparse every discoverable source transcript from the configured profiles
// (rebuild). The source transcript files themselves are never rewritten.
package transcripts

import (
	"fmt"
	"io"
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
		out := cmd.OutOrStdout()
		if transcriptsIsTerminal(out) {
			writeCompactSummary(out, result)
		} else {
			size := rawBytes
			_, _ = fmt.Fprintf(out, "compacted transcript index\n")
			_, _ = fmt.Fprintf(out, "legacy_search_text_entries: %d\n", result.LegacyFields)
			_, _ = fmt.Fprintf(out, "metadata_size_before: %s\n", size(result.StatusBefore.MetadataSize))
			_, _ = fmt.Fprintf(out, "metadata_size_after: %s\n", size(result.StatusAfter.MetadataSize))
			_, _ = fmt.Fprintf(out, "sidecar_count: %d\n", result.StatusAfter.SidecarCount)
			_, _ = fmt.Fprintf(out, "orphan_sidecars_removed: %d\n", result.RemovedOrphanSidecars)
			_, _ = fmt.Fprintf(out, "orphan_sidecar_size_removed: %s\n", size(result.RemovedOrphanSidecarSize))
		}
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
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rebuilt transcript sidecars\n")
		status := result.Status
		status.Warnings = result.Warnings
		printTranscriptStatus(cmd, status)
		return nil
	},
}

func printTranscriptStatus(cmd *cobra.Command, status transcript.StoreStatus) {
	out := cmd.OutOrStdout()
	if transcriptsIsTerminal(out) {
		writeTranscriptStatusSummary(out, status)
		printTranscriptWarnings(cmd, status.Warnings)
		printTranscriptRepairHints(out, status)
		return
	}
	// Pipes keep the key/value lines, with the single space after the colon,
	// raw byte counts, and full paths that scripts have always split on.
	size := rawBytes
	fields := [][2]string{
		{"metadata_path", status.MetadataPath},
		{"metadata_size", size(status.MetadataSize)},
		{"sidecar_dir", status.SidecarDir},
		{"sidecar_size", size(status.SidecarSize)},
		{"sidecar_count", strconv.Itoa(status.SidecarCount)},
		{"orphan_sidecars", strconv.Itoa(status.OrphanSidecars)},
		{"orphan_sidecar_size", size(status.OrphanSidecarSize)},
		{"visible_entries", strconv.Itoa(status.VisibleEntries)},
		{"hidden_entries", strconv.Itoa(status.HiddenEntries)},
		{"empty_entries", strconv.Itoa(status.EmptyEntries)},
		{"visible_profile_memberships", strconv.Itoa(status.VisibleProfileMemberships)},
		{"hidden_profile_memberships", strconv.Itoa(status.HiddenProfileMemberships)},
		{"shared_profile_entries", strconv.Itoa(status.SharedProfileEntries)},
		{"missing_sidecars", strconv.Itoa(status.MissingSidecars)},
	}
	for _, field := range fields {
		_, _ = fmt.Fprintf(out, "%s: %s\n", field[0], field[1])
	}
	printTranscriptWarnings(cmd, status.Warnings)
}

// writeTranscriptStatusSummary is the status for a person: what the index
// holds in words rather than the cache's field names, sizes in binary units,
// and paths under ~. A problem count reads "none" when there is nothing to do,
// and the repair hints that follow say what clears it when there is. The
// per-profile membership counts stay in the piped and JSON forms.
func writeTranscriptStatusSummary(out io.Writer, status transcript.StoreStatus) {
	sessions := cli.CountOf(status.VisibleEntries, "conversation", "conversations") + " in Past Sessions"
	if status.EmptyEntries > 0 {
		sessions += fmt.Sprintf(" (%d not started yet)", status.EmptyEntries)
	}
	if status.HiddenEntries > 0 {
		sessions += fmt.Sprintf("; %d removed from it", status.HiddenEntries)
	}
	profiles := "each conversation belongs to one profile"
	if status.SharedProfileEntries > 0 {
		profiles = cli.CountOf(status.SharedProfileEntries, "conversation appears", "conversations appear") + " under more than one profile"
	}
	orphans := "none"
	if status.OrphanSidecars > 0 {
		orphans = fmt.Sprintf("%s (%s) %s to no conversation", cli.CountOf(status.OrphanSidecars, "sidecar", "sidecars"), cli.FormatBytes(status.OrphanSidecarSize), cli.ByCount(status.OrphanSidecars, "belongs", "belong"))
	}
	missing := "none"
	if status.MissingSidecars > 0 {
		missing = fmt.Sprintf("%s without search text", cli.CountOf(status.MissingSidecars, "conversation", "conversations"))
	}
	rows := [][2]string{
		{"index", fmt.Sprintf("%s (%s)", cli.DisplayPath(status.MetadataPath), cli.FormatBytes(status.MetadataSize))},
		{"sessions", sessions},
		{"profiles", profiles},
		{"search", fmt.Sprintf("%s in %s (%s)", cli.CountOf(status.SidecarCount, "sidecar", "sidecars"), cli.DisplayPath(status.SidecarDir), cli.FormatBytes(status.SidecarSize))},
		{"orphaned", orphans},
		{"missing", missing},
	}
	for _, row := range rows {
		_, _ = fmt.Fprintf(out, "%-9s %s\n", row[0]+":", row[1])
	}
}

// writeCompactSummary says what compact changed, in words, for a person; the
// piped form keeps its key/value lines.
func writeCompactSummary(out io.Writer, result transcript.CompactResult) {
	_, _ = fmt.Fprintln(out, "Compacted the transcript index.")
	legacy := "no legacy search text to move"
	if result.LegacyFields > 0 {
		legacy = "moved the legacy search text of " + cli.CountOf(result.LegacyFields, "conversation", "conversations") + " into sidecars"
	}
	orphans := "no orphan sidecars to remove"
	if result.RemovedOrphanSidecars > 0 {
		orphans = fmt.Sprintf("removed %s (%s)", cli.CountOf(result.RemovedOrphanSidecars, "orphan sidecar", "orphan sidecars"), cli.FormatBytes(result.RemovedOrphanSidecarSize))
	}
	rows := [][2]string{
		{"index", fmt.Sprintf("%s, was %s", cli.FormatBytes(result.StatusAfter.MetadataSize), cli.FormatBytes(result.StatusBefore.MetadataSize))},
		{"legacy", legacy},
		{"orphaned", orphans},
		{"search", cli.CountOf(result.StatusAfter.SidecarCount, "sidecar remains", "sidecars remain")},
	}
	for _, row := range rows {
		_, _ = fmt.Fprintf(out, "%-9s %s\n", row[0]+":", row[1])
	}
}

// printTranscriptRepairHints names the command that clears each problem the
// status counts, so a nonzero count is not left for the reader to interpret.
// Pipes do not get them: scripts split the key/value lines.
func printTranscriptRepairHints(out io.Writer, status transcript.StoreStatus) {
	if status.MissingSidecars > 0 {
		// --all, because a conversation older than the configured scan age is
		// skipped by a plain rebuild and would stay unsearchable.
		_, _ = fmt.Fprintf(out, "\n%s %s no search text, so search cannot find %s; `amx transcripts rebuild --all` reindexes %s.\n",
			cli.CountOf(status.MissingSidecars, "conversation", "conversations"),
			cli.ByCount(status.MissingSidecars, "has", "have"),
			cli.ByCount(status.MissingSidecars, "it", "them"),
			cli.ByCount(status.MissingSidecars, "it", "them"))
	}
	if status.OrphanSidecars > 0 {
		_, _ = fmt.Fprintf(out, "\n%s no longer %s to a conversation; `amx transcripts compact` removes %s.\n",
			cli.CountOf(status.OrphanSidecars, "search sidecar", "search sidecars"),
			cli.ByCount(status.OrphanSidecars, "belongs", "belong"),
			cli.ByCount(status.OrphanSidecars, "it", "them"))
	}
}

// rawBytes is how the piped forms print a byte count: the raw number scripts
// have always parsed. A terminal gets cli.FormatBytes instead.
func rawBytes(v int64) string { return strconv.FormatInt(v, 10) }

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
	transcriptsRebuildCmd.Flags().BoolVar(&transcriptsRebuildAll, "all", false, "ignore configured transcript scan age and rebuild every discoverable sidecar")
	for _, command := range []*cobra.Command{transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd} {
		command.Flags().Bool("json", false, "emit schema-v1 JSON")
	}
	transcriptsCmd.AddCommand(transcriptsStatusCmd, transcriptsCompactCmd, transcriptsRebuildCmd)
}
