package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	searchsvc "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	searchCommandLoadConfig = loadRunnableConfig
	searchCommandOpenStore  = datastore.Open
	searchCommandSync       = func(ctx context.Context, store *datastore.Store, cfg *config.Config) error {
		_, err := workspace.New(store).Build(ctx, cfg)
		return err
	}
)

type searchData struct {
	Query   string             `json:"query"`
	Results []searchsvc.Result `json:"results"`
}

var searchCmd = newSearchCommand()

func newSearchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search projects, conversations, plans, handoffs, and activity",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			text := strings.TrimSpace(strings.Join(args, " "))
			if text == "" {
				return missingParameter(cmd, jsonOutput, "query", "normal full-text search terms")
			}
			query, err := searchQueryFromFlags(cmd, text)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "invalid_search", err)
			}
			cfg, err := searchCommandLoadConfig()
			if err != nil {
				return commandFailure(cmd, jsonOutput, "config_error", err)
			}
			ctx := commandContext(cmd)
			store, err := searchCommandOpenStore(ctx)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "datastore_error", err)
			}
			defer func() { _ = store.Close() }()
			if err := searchCommandSync(ctx, store, cfg); err != nil {
				return commandFailure(cmd, jsonOutput, "workspace_unavailable", err)
			}
			service, err := searchsvc.New(store)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "search_unavailable", err)
			}
			results, err := service.Search(ctx, query)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "search_failed", err)
			}
			if jsonOutput {
				return writeJSON(cmd, searchData{Query: text, Results: results})
			}
			for _, result := range results {
				when := "-"
				if !result.OccurredAt.IsZero() {
					when = formatTime(result.OccurredAt)
				}
				snippet := strings.Join(strings.Fields(result.Snippet), " ")
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\t%s\n",
					result.EntityType, result.EntityID, when, result.Title, snippet, result.PrimaryAction.ID)
			}
			return nil
		},
	}
	cmd.Flags().StringSlice("type", nil, "entity type filter (project, conversation, plan, handoff, activity)")
	cmd.Flags().StringSlice("workspace", nil, "workspace ID filter")
	cmd.Flags().StringSlice("harness", nil, "harness filter")
	cmd.Flags().StringSlice("profile", nil, "profile filter")
	cmd.Flags().String("from", "", "include results at or after RFC3339 timestamp or YYYY-MM-DD")
	cmd.Flags().String("to", "", "include results at or before RFC3339 timestamp or YYYY-MM-DD")
	cmd.Flags().Int("limit", 50, "maximum number of results (1-500)")
	cmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	return cmd
}

func searchQueryFromFlags(cmd *cobra.Command, text string) (searchsvc.Query, error) {
	typeNames, _ := cmd.Flags().GetStringSlice("type")
	types := make([]searchsvc.EntityType, 0, len(typeNames))
	for _, name := range typeNames {
		entityType, err := searchsvc.ParseEntityType(name)
		if err != nil {
			return searchsvc.Query{}, err
		}
		types = append(types, entityType)
	}
	fromText, _ := cmd.Flags().GetString("from")
	toText, _ := cmd.Flags().GetString("to")
	from, err := parseSearchBound(fromText, false)
	if err != nil {
		return searchsvc.Query{}, err
	}
	to, err := parseSearchBound(toText, true)
	if err != nil {
		return searchsvc.Query{}, err
	}
	workspaces, _ := cmd.Flags().GetStringSlice("workspace")
	harnesses, _ := cmd.Flags().GetStringSlice("harness")
	profiles, _ := cmd.Flags().GetStringSlice("profile")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > 500 {
		return searchsvc.Query{}, errors.New("limit must be between 1 and 500")
	}
	return searchsvc.Query{
		Text: text, Types: types, Workspaces: workspaces, Harnesses: harnesses,
		Profiles: profiles, From: from, To: to, Limit: limit,
	}, nil
}

func parseSearchBound(value string, endOfDay bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: use RFC3339 or YYYY-MM-DD", value)
	}
	if endOfDay {
		parsed = parsed.Add(24*time.Hour - time.Nanosecond)
	}
	return parsed, nil
}
