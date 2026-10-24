package session

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/searchquery"
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
line it matched on. The conversation the search runs from comes last, since
the query was typed there. The selector flags narrow the search the way they narrow
` + "`amx session list`" + `.`,
	Example: `  amx session search daemon race
  amx session search '"race condition"' --since 7d
  amx session search --source user migrate plans --here
  amx session search flaky test --json`,
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
			return cli.ConfigFailure(cmd, jsonOutput, err)
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
		if err := selection.resolveProjects(cfg, idx); err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", err)
		}
		out := cmd.OutOrStdout()
		terminal := !jsonOutput && sessionListIsTerminal(out)
		// A terminal table says how many automated sessions match too, as the
		// list does, so the search takes them in and sets them aside here: one
		// pass over the sidecars rather than a second search for a count.
		countHidden := (terminal || jsonOutput) && selection.filter.MainOnly
		results, err := transcript.Search(idx, readers, transcript.Query{
			Text:          query,
			Conversation:  selection.filter.Conversation,
			Project:       selection.projects,
			MessageSource: source,
			Harness:       selection.filter.Harness,
			Profile:       selection.filter.Profile,
			Since:         selection.filter.Since,
			MainOnly:      selection.filter.MainOnly && !countHidden,
		})
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "command_failed", err)
		}
		hidden := 0
		if countHidden {
			results, hidden = setAutomatedAside(results, selection)
		}
		allSelection := selection
		allSelection.limit, allSelection.offset = 0, 0
		total := len(narrowSearchResults(results, allSelection))
		results = narrowSearchResults(results, selection)
		omitted := 0
		if terminal {
			var keep int
			keep, omitted = tableDefaultLimit(cmd, len(results))
			results = results[:keep]
		}
		results = transcript.RecasePreviews(readers, results, transcript.Query{Text: query, MessageSource: source})
		views := sessionSearchViews(results)
		// Marked as list marks them, because a search run from inside a
		// conversation finds that conversation too — the query is in it — and
		// "here" is what tells it apart from the sessions being looked for.
		// Asked even when piped, since an agent searching from its own
		// conversation reads piped rows, which have no LIVE field.
		live, liveKnown := selection.liveConversations(true)
		listed := make([]sessionView, len(views))
		for i := range views {
			listed[i] = views[i].sessionView
		}
		markLive(listed, live, sessionCommandsCurrentWindow())
		for i := range views {
			views[i].sessionView = listed[i]
		}
		// Explicit pages keep the stable archive order across page boundaries.
		if !cmd.Flags().Changed("offset") {
			views = currentConversationLast(views)
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionSearchData{Query: query, Source: source, Results: views, Total: total, Offset: selection.offset, HasMore: selection.offset+len(views) < total, HiddenAutomated: hidden, MatchTerms: searchquery.Parse(query)})
		}
		if terminal {
			if len(views) == 0 {
				message := "No indexed sessions match " + strconv.Quote(query) + "."
				if flags := selectionFlagsText(cmd); flags != "" {
					message = "No indexed sessions match " + strconv.Quote(query) + " with " + flags + "."
				}
				if hidden > 0 {
					message += " Automated sessions do; --all shows them."
				}
				_, err := fmt.Fprintln(out, message)
				return err
			}
			if err := writeSessionSearchTable(out, query, views, abbreviateSessionIDs(idx.List(transcript.ListFilter{})), sessionCommandsNow(), sessionListTerminalWidth(out), liveKnown); err != nil {
				return err
			}
			if err := writeOmittedFooter(out, len(views), omitted, 0); err != nil {
				return err
			}
			return writeAutomatedMatches(out, hidden)
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

// writeAutomatedMatches is the search's counterpart of the list's hidden
// automated line: how many automated sessions match the query too, rather
// than how many there are.
func writeAutomatedMatches(out io.Writer, hidden int) error {
	var err error
	switch {
	case hidden == 1:
		_, err = fmt.Fprintln(out, "1 automated session also matches; --all includes it.")
	case hidden > 1:
		_, err = fmt.Fprintf(out, "%d automated sessions also match; --all includes them.\n", hidden)
	}
	return err
}

// currentConversationLast moves the conversation the search runs from after
// the others, keeping their order. It holds the query, since it was typed
// there, so it matches nearly every search and would otherwise lead a list of
// the sessions being looked for.
func currentConversationLast(views []sessionSearchView) []sessionSearchView {
	out := make([]sessionSearchView, 0, len(views))
	var here []sessionSearchView
	for _, view := range views {
		if view.Live != nil && view.Live.Here {
			here = append(here, view)
			continue
		}
		out = append(out, view)
	}
	return append(out, here...)
}

// setAutomatedAside keeps the results of main sessions, which is what a search
// without --all returns, and counts the automated and unclassified ones that
// the selection's other selectors would have kept, regardless of --limit.
func setAutomatedAside(results []transcript.Result, selection sessionSelection) ([]transcript.Result, int) {
	main := make([]transcript.Result, 0, len(results))
	for _, result := range results {
		if result.Meta.SessionClass == transcript.SessionClassMain {
			main = append(main, result)
		}
	}
	selection.limit, selection.offset = 0, 0
	return main, len(narrowSearchResults(results, selection)) - len(narrowSearchResults(main, selection))
}

// narrowSearchResults applies --project, --here, --live, and --limit to search results, keeping
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
	Total           int      `json:"total"`
	Offset          int      `json:"offset"`
	HasMore         bool     `json:"has_more"`
	HiddenAutomated int      `json:"hidden_automated"`
	MatchTerms      []string `json:"match_terms"`

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

func writeSessionSearchTable(out io.Writer, query string, views []sessionSearchView, ids sessionIDAbbreviations, now time.Time, width int, liveKnown bool) error {
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
		row := []string{item.Harness, item.Profile, project, formatAgo(item.LastAt, now)}
		if liveKnown {
			row = append(row, liveMark(item.Live))
		}
		rows = append(rows, append(row, strconv.Itoa(item.Matches), ids.of(item.ID), preview))
	}
	header := []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "MATCHES", "SESSION", "MATCHED"}
	if liveKnown {
		header = []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "LIVE", "MATCHES", "SESSION", "MATCHED"}
	}
	fitLastColumn(header, rows, width)
	return cli.WriteTable(out, header, rows)
}

// oneLine collapses runs of whitespace, newlines and tabs included, so a
// matched line stays one table cell or one tab-separated field.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
