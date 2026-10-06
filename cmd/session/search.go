package session

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// sessionSearchCmd is Past Sessions search without the dashboard: the same
// transcript.Search over the same disposable sidecars, so a query finds the
// same conversations in both places. Results come newest first, as they do in
// the list, because the usual next step is `session continue` on a recent one.
var sessionSearchCmd = &cobra.Command{
	Use:   "search <query>...",
	Short: "Search indexed conversations by their message text",
	Long: `Search indexed conversations by their message text.

The words of the query must all appear in a conversation, in any order and
case. Wrap words in double quotes to match them as one phrase; the shell needs
them quoted again, as in: amx session search '"race condition"' daemon.

Results come newest first, with how many times the query matched and the first
line it matched on. The selector flags narrow the search the way they narrow
` + "`amx session list`" + `.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			if !jsonOutput {
				// A person who typed the bare command most likely wants to
				// know what a query looks like, or wanted the list instead.
				return errors.New("a query is required, as in: amx session search daemon race; `amx session list` shows recent sessions without one")
			}
			return cli.MissingParameter(cmd, jsonOutput, "query", "text to search conversations for")
		}
		source, _ := cmd.Flags().GetString("source")
		source = strings.ToLower(strings.TrimSpace(source))
		if source != "any" && source != "user" && source != "harness" {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("--source %q: want any, user, or harness", source))
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		selection, code, err := readSessionSelection(cmd, cfg)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, code, err)
		}
		readers := sessionCommandsSessionReaders()
		idx, err := loadRefreshedSessionIndex(cfg, readers)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		results, err := transcript.Search(idx, readers, transcript.Query{
			Text:          query,
			MessageSource: source,
			Harness:       selection.filter.Harness,
			Profile:       selection.filter.Profile,
			Since:         selection.filter.Since,
			MainOnly:      selection.filter.MainOnly,
		})
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "command_failed", err)
		}
		results = narrowSearchResults(results, selection)
		results = transcript.RecasePreviews(readers, results, transcript.Query{Text: query, MessageSource: source})
		views := sessionSearchViews(results)
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionSearchData{Query: query, Source: source, Results: views})
		}
		out := cmd.OutOrStdout()
		if sessionListIsTerminal(out) {
			return writeSessionSearchTable(out, query, views, abbreviateSessionIDs(idx.List(transcript.ListFilter{})), sessionCommandsNow(), sessionListTerminalWidth(out))
		}
		// Off a terminal the rows follow `session list`'s piped form, with the
		// match count before the ID and the matched line last, where a tab or
		// newline inside it cannot shift the other fields.
		for _, item := range views {
			_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", item.Harness, item.Profile, item.Project, formatTime(item.LastAt), item.Matches, item.ID, oneLine(item.Preview))
		}
		return nil
	},
}

// narrowSearchResults applies --here and --limit to search results, keeping
// their order, through the same sessionSelection.narrow the list uses.
func narrowSearchResults(results []transcript.Result, selection sessionSelection) []transcript.Result {
	metas := make([]transcript.SessionMeta, 0, len(results))
	byKey := make(map[string]transcript.Result, len(results))
	for _, result := range results {
		metas = append(metas, result.Meta)
		byKey[transcript.IndexKey(result.Meta.Harness, result.Meta.ID)] = result
	}
	kept := selection.narrow(metas)
	out := make([]transcript.Result, 0, len(kept))
	for _, meta := range kept {
		out = append(out, byKey[transcript.IndexKey(meta.Harness, meta.ID)])
	}
	return out
}

type sessionSearchData struct {
	Query   string              `json:"query"`
	Source  string              `json:"source"`
	Results []sessionSearchView `json:"results"`
}

// sessionSearchView is a listed session plus what the query found in it.
type sessionSearchView struct {
	sessionView
	Matches int    `json:"matches"`
	Preview string `json:"preview"`
}

func sessionSearchViews(results []transcript.Result) []sessionSearchView {
	out := make([]sessionSearchView, 0, len(results))
	for _, result := range results {
		out = append(out, sessionSearchView{
			sessionView: sessionViews([]transcript.SessionMeta{result.Meta})[0],
			Matches:     result.Matches,
			Preview:     result.Preview,
		})
	}
	return out
}

func writeSessionSearchTable(out io.Writer, query string, views []sessionSearchView, ids sessionIDAbbreviations, now time.Time, width int) error {
	if len(views) == 0 {
		_, err := fmt.Fprintf(out, "No indexed sessions match %s.\n", strconv.Quote(query))
		return err
	}
	rows := make([][]string, 0, len(views))
	for _, item := range views {
		project := item.Project
		if project == "" {
			project = "-"
		}
		preview := oneLine(item.Preview)
		if preview == "" {
			preview = "-"
		}
		rows = append(rows, []string{item.Harness, item.Profile, project, formatAgo(item.LastAt, now), strconv.Itoa(item.Matches), ids.of(item.ID), preview})
	}
	header := []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "MATCHES", "SESSION", "MATCHED"}
	fitLastColumn(header, rows, width)
	return cli.WriteTable(out, header, rows)
}

// oneLine collapses runs of whitespace, newlines and tabs included, so a
// matched line stays one table cell or one tab-separated field.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
