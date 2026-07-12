package cmd

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

var (
	transcriptsRebuildAll bool

	transcriptsIndexPath  = transcript.IndexPath
	transcriptsLoadConfig = loadRunnableConfig
	transcriptsReaders    = transcriptReaders
	transcriptsOpenStore  = datastore.Open
	transcriptsLoadIndex  = transcript.LoadSQLiteIndexWithStore
)

var transcriptsCmd = &cobra.Command{
	Use:   "transcripts",
	Short: "Inspect and maintain the SQLite transcript search index",
}

var transcriptsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show SQLite transcript index status",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		store, err := transcriptsOpenStore(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		idx, err := transcriptsLoadIndex(ctx, transcriptsIndexPath(), store, transcriptsReaders(), compatimport.SyncSkip)
		if err != nil {
			return err
		}
		status, err := idx.Status()
		if err != nil {
			return err
		}
		printTranscriptStatus(cmd, status)
		return nil
	},
}

var transcriptsCompactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Verify the idempotent legacy-to-SQLite transcript import",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		store, err := transcriptsOpenStore(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		readers := transcriptsReaders()
		idx, err := transcriptsLoadIndex(ctx, transcriptsIndexPath(), store, readers, compatimport.SyncSkip)
		if err != nil {
			return err
		}
		result, err := transcript.CompactSQLite(idx, readers)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "verified SQLite transcript import\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "legacy_source_found: %t\n", result.Import.SourceFound)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "legacy_imported: %t\n", result.Import.Imported)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "record_count: %d\n", result.Import.RecordCount)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "search_document_count: %d\n", result.StatusAfter.SearchDocumentCount)
		return nil
	},
}

var transcriptsRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Refresh transcripts from configured profiles and rebuild SQLite search documents",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		cfg, err := transcriptsLoadConfig()
		if err != nil {
			return err
		}
		store, err := transcriptsOpenStore(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		readers := transcriptsReaders()
		idx, err := transcriptsLoadIndex(ctx, transcriptsIndexPath(), store, readers, compatimport.SyncForce)
		if err != nil {
			return err
		}
		result, err := transcript.RebuildSQLite(idx, cfg, readers, transcriptsRebuildAll)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rebuilt SQLite transcript search documents\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "metadata_path: %s\n", result.Status.MetadataPath)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_entries: %d\n", result.Status.VisibleEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_entries: %d\n", result.Status.HiddenEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "visible_profile_memberships: %d\n", result.Status.VisibleProfileMemberships)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hidden_profile_memberships: %d\n", result.Status.HiddenProfileMemberships)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "shared_profile_entries: %d\n", result.Status.SharedProfileEntries)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "search_document_count: %d\n", result.Status.SearchDocumentCount)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "missing_search_documents: %d\n", result.Status.MissingSearchDocuments)
		return nil
	},
}

func printTranscriptStatus(cmd *cobra.Command, status transcript.StoreStatus) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "backend: %s\n", status.Backend)
	if status.DatabasePath != "" {
		_, _ = fmt.Fprintf(out, "database_path: %s\n", status.DatabasePath)
	}
	if status.DegradedReason != "" {
		_, _ = fmt.Fprintf(out, "degraded_reason: %s\n", status.DegradedReason)
	}
	_, _ = fmt.Fprintf(out, "metadata_path: %s\n", status.MetadataPath)
	_, _ = fmt.Fprintf(out, "metadata_size: %d\n", status.MetadataSize)
	_, _ = fmt.Fprintf(out, "sidecar_dir: %s\n", status.SidecarDir)
	_, _ = fmt.Fprintf(out, "sidecar_size: %d\n", status.SidecarSize)
	_, _ = fmt.Fprintf(out, "sidecar_count: %d\n", status.SidecarCount)
	_, _ = fmt.Fprintf(out, "visible_entries: %d\n", status.VisibleEntries)
	_, _ = fmt.Fprintf(out, "hidden_entries: %d\n", status.HiddenEntries)
	_, _ = fmt.Fprintf(out, "visible_profile_memberships: %d\n", status.VisibleProfileMemberships)
	_, _ = fmt.Fprintf(out, "hidden_profile_memberships: %d\n", status.HiddenProfileMemberships)
	_, _ = fmt.Fprintf(out, "shared_profile_entries: %d\n", status.SharedProfileEntries)
	_, _ = fmt.Fprintf(out, "missing_sidecars: %d\n", status.MissingSidecars)
	_, _ = fmt.Fprintf(out, "search_document_count: %d\n", status.SearchDocumentCount)
	_, _ = fmt.Fprintf(out, "missing_search_documents: %d\n", status.MissingSearchDocuments)
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
