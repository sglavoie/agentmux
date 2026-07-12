package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	indexLoadConfig     = loadRunnableConfig
	indexOpenStore      = datastore.Open
	indexTranscriptPath = transcript.IndexPath
	indexPlanPath       = plans.IndexPath
	indexPlanRoot       = defaultPlanRoot
	indexReaders        = transcriptReaders
	indexNewKitty       = func() kitty.KittyClient { return kitty.NewClient(kitty.NewRealRunner()) }
	indexDaemonHealth   = func() daemon.Health { return daemon.NewClient().Health() }
	indexReadFile       = os.ReadFile
)

type indexStatusData struct {
	Datastore       datastore.Status  `json:"datastore"`
	DaemonReachable bool              `json:"daemon_reachable"`
	ActivityWriter  *activity.Metrics `json:"activity_writer,omitempty"`
}

type indexRebuildData struct {
	Datastore           datastore.Status         `json:"datastore"`
	Conversations       int                      `json:"conversations"`
	Plans               int                      `json:"plans"`
	DiscoveryCandidates int                      `json:"discovery_candidates"`
	RepairedDocuments   map[string]int           `json:"repaired_documents"`
	Unavailable         map[string]int           `json:"unavailable_placeholders"`
	ProtectedState      datastore.ProtectedState `json:"protected_state"`
}

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Inspect and rebuild Agent Mux derived data",
}

var indexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show database, migration, import, FTS, writer, and permission health",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		ctx := commandContext(cmd)
		store, err := indexOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		status, err := store.Inspect(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "index_status_failed", err)
		}
		health := indexDaemonHealth()
		data := indexStatusData{Datastore: status, DaemonReachable: health.Alive, ActivityWriter: health.ActivityWriter}
		if jsonOutput {
			return writeJSON(cmd, data)
		}
		printIndexStatus(cmd, data)
		return nil
	},
}

var indexRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Rebuild derived metadata and search data without changing durable user state",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		result, err := rebuildIndexes(commandContext(cmd))
		if err != nil {
			return commandFailure(cmd, jsonOutput, "index_rebuild_failed", err)
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rebuilt derived indexes: %d conversations, %d plans, %d discovery candidates\n",
			result.Conversations, result.Plans, result.DiscoveryCandidates)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "protected_state_sha256: %s\n", result.ProtectedState.SHA256)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repaired_search_documents: %v\n", result.RepairedDocuments)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "unavailable_placeholders: %v\n", result.Unavailable)
		health := indexDaemonHealth()
		printIndexStatus(cmd, indexStatusData{Datastore: result.Datastore, DaemonReachable: health.Alive, ActivityWriter: health.ActivityWriter})
		return nil
	},
}

func rebuildIndexes(ctx context.Context) (indexRebuildData, error) {
	cfg, err := indexLoadConfig()
	if err != nil {
		return indexRebuildData{}, err
	}
	// Decode both rollback indexes before any derived mutation. A malformed
	// compatibility artifact therefore fails closed without disturbing the
	// currently usable database.
	conversationIndex, err := transcript.LoadIndexAt(indexTranscriptPath())
	if err != nil {
		return indexRebuildData{}, err
	}
	planIndex, err := plans.LoadIndexAt(indexPlanPath())
	if err != nil {
		return indexRebuildData{}, err
	}
	store, err := indexOpenStore(ctx)
	if err != nil {
		return indexRebuildData{}, err
	}
	defer func() { _ = store.Close() }()
	before, err := store.SnapshotProtectedState(ctx)
	if err != nil {
		return indexRebuildData{}, err
	}

	readers := indexReaders()
	conversationIndex = transcript.EnableSQLiteIndexWithMode(ctx, conversationIndex, store, readers, compatimport.SyncForce)
	if !conversationIndex.UsesSQLite() {
		return indexRebuildData{}, conversationIndex.DegradedReason()
	}
	planIndex = plans.EnableSQLiteIndexWithMode(ctx, planIndex, store, compatimport.SyncForce)
	if !planIndex.UsesSQLite() {
		return indexRebuildData{}, planIndex.DegradedReason()
	}
	if _, err := workspace.New(store).Build(ctx, cfg); err != nil {
		return indexRebuildData{}, err
	}
	if _, err := transcript.RebuildSQLite(conversationIndex, cfg, readers, true); err != nil {
		return indexRebuildData{}, err
	}
	planRoot := indexPlanRoot()
	if cfg.Defaults.PlansDirectory != "" {
		planRoot = cfg.Defaults.PlansDirectory
	}
	if _, err := os.Stat(planRoot); err == nil {
		if err := planIndex.Refresh(planRoot); err != nil {
			return indexRebuildData{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return indexRebuildData{}, err
	}
	if _, err := workspace.New(store).Build(ctx, cfg); err != nil {
		return indexRebuildData{}, err
	}
	activityRepository, err := activity.NewRepository(store)
	if err != nil {
		return indexRebuildData{}, err
	}
	if err := activityRepository.Backfill(ctx); err != nil {
		return indexRebuildData{}, err
	}
	repaired := make(map[string]int)
	transcriptRepository, err := transcript.NewRepository(store)
	if err != nil {
		return indexRebuildData{}, err
	}
	if repaired["conversation"], err = transcriptRepository.RepairMissingSearchDocuments(ctx, readers); err != nil {
		return indexRebuildData{}, err
	}
	planRepository, err := plans.NewRepository(store)
	if err != nil {
		return indexRebuildData{}, err
	}
	if repaired["plan"], err = planRepository.RepairMissingSearchDocuments(ctx); err != nil {
		return indexRebuildData{}, err
	}
	handoffRepository, err := handoff.NewRepository(store)
	if err != nil {
		return indexRebuildData{}, err
	}
	if repaired["handoff"], err = handoffRepository.RepairMissingSearchDocuments(ctx); err != nil {
		return indexRebuildData{}, err
	}
	repairedActivity, err := activityRepository.RepairMissingSearchDocuments(ctx)
	if err != nil {
		return indexRebuildData{}, err
	}
	repaired["activity"] = int(repairedActivity)

	live := make([]session.RunningSession, 0)
	if client := indexNewKitty(); client != nil {
		if windows, err := client.ListWindows(); err == nil {
			live = session.ListFromOSWindows(windows)
		}
	}
	candidates, err := workspace.New(store).DiscoverCandidates(ctx, workspace.DiscoveryOptions{
		Config: cfg, LiveSessions: live,
		Conversations: conversationIndex.List(transcript.ListFilter{}),
		Plans:         collectPlanEvidence(planIndex.List(), indexReadFile),
	})
	if err != nil {
		return indexRebuildData{}, err
	}
	if err := store.RebuildSearchIndex(ctx); err != nil {
		return indexRebuildData{}, err
	}
	if err := store.VerifySearchIndex(ctx); err != nil {
		return indexRebuildData{}, err
	}
	after, err := store.SnapshotProtectedState(ctx)
	if err != nil {
		return indexRebuildData{}, err
	}
	if before.SHA256 != after.SHA256 {
		return indexRebuildData{}, fmt.Errorf("protected durable state changed during rebuild: before %s after %s", before.SHA256, after.SHA256)
	}
	status, err := store.Inspect(ctx)
	if err != nil {
		return indexRebuildData{}, err
	}
	if !status.Healthy {
		return indexRebuildData{}, fmt.Errorf("rebuilt index remains unhealthy: integrity=%q schema=%d/%d foreign_keys=%d permissions=%t FTS_fresh=%t",
			status.Integrity, status.SchemaVersion, status.CurrentSchemaVersion,
			status.ForeignKeyViolations, status.SecurePermissions, status.Search.Fresh)
	}
	status.Search.IntegrityVerified = true
	return indexRebuildData{
		Datastore: status, Conversations: len(conversationIndex.List(transcript.ListFilter{IncludeHidden: true})),
		Plans: len(planIndex.List()), DiscoveryCandidates: len(candidates), RepairedDocuments: repaired,
		Unavailable: status.Search.UnavailablePlaceholders, ProtectedState: after,
	}, nil
}

func printIndexStatus(cmd *cobra.Command, data indexStatusData) {
	status := data.Datastore
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "database_path: %s\n", status.Path)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "schema_version: %d/%d\n", status.SchemaVersion, status.CurrentSchemaVersion)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "integrity: %s\n", status.Integrity)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "foreign_key_violations: %d\n", status.ForeignKeyViolations)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "secure_permissions: %t\n", status.SecurePermissions)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "imports: %d\n", len(status.Imports))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "search_documents: %d\n", status.Search.DocumentCount)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "fts_rows: %d\n", status.Search.FTSRowCount)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "missing_search_documents: %v\n", status.Search.MissingDocuments)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repairable_missing_search_documents: %v\n", status.Search.RepairableMissingDocuments)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "unavailable_search_placeholders: %v\n", status.Search.UnavailablePlaceholders)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "inconsistent_search_documents: %v\n", status.Search.InconsistentDocuments)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "fts_integrity_verified: %t\n", status.Search.IntegrityVerified)
	for _, warning := range status.Search.Warnings {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "fts_fresh: %t\n", status.Search.Fresh)
	if data.ActivityWriter != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "activity_writer: degraded=%t queued=%d persisted=%d dropped=%d failures=%d\n",
			data.ActivityWriter.Degraded, data.ActivityWriter.Queued, data.ActivityWriter.Persisted,
			data.ActivityWriter.Dropped, data.ActivityWriter.WriteFailures)
	} else if data.DaemonReachable {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "activity_writer: unavailable from running daemon")
	} else {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "activity_writer: daemon unreachable")
	}
}

func defaultPlanRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-plans")
	}
	return filepath.Join(home, ".local", "share", "agent-plans")
}

func init() {
	indexStatusCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	indexRebuildCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	indexCmd.AddCommand(indexStatusCmd, indexRebuildCmd)
	rootCmd.AddCommand(indexCmd)
}
