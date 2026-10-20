package session

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/spf13/cobra"
)

// writeSessionLines is `session list` off a terminal: headerless and
// tab-separated with absolute timestamps, because scripts cut and sort it and
// a relative age is stale the moment it is written to a file. The opening
// prompt and then the title follow the fixed fields, each on one line, so a
// picker such as fzf has something to match on. Every field added since was
// appended, so each one before it kept its position: the title (empty when the
// harness named the conversation nothing), then the message count and the
// LIVE field the table shows — "here" for the conversation open in the window
// the command runs in, "live" for one open in another tab, empty for a closed
// one, and "?" on every row when the daemon or Kitty could not be asked — and
// last the opening of the agent's final reply, empty when it never replied in
// prose.
func writeSessionLines(out io.Writer, views []sessionView, liveKnown bool) {
	for _, item := range views {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", item.Harness, item.Profile, item.Project, formatTime(item.LastAt), item.ID, oneLine(item.Snippet), oneLine(item.Title), item.MessageCount, lineLiveField(item.Live, liveKnown), oneLine(item.Ending))
	}
}

// lineLiveField is a piped row's LIVE field, spelled in words rather than the
// table's dot so a script can match it.
func lineLiveField(live *liveConversation, known bool) string {
	switch {
	case !known:
		return "?"
	case live == nil:
		return ""
	case live.Here:
		return "here"
	default:
		return "live"
	}
}

// sessionTableRows is how many sessions a terminal table shows when --limit
// was not given. A person reading the table wants the recent ones, and a
// whole index scrolls the newest off the top; piped rows and --json keep
// every session, since a script asked for them.
const sessionTableRows = 25

// tableDefaultLimit is how many of n rows a terminal table keeps, and how
// many it leaves out: all of them when --limit was given, else at most
// sessionTableRows.
func tableDefaultLimit(cmd *cobra.Command, n int) (keep, omitted int) {
	if cmd.Flags().Changed("limit") || n <= sessionTableRows {
		return n, 0
	}
	return sessionTableRows, n - sessionTableRows
}

// writeOmittedFooter says how many rows the default limit left out, and how
// many automated sessions the same selectors would have found with --all, so
// a short table is not read as everything there is.
func writeOmittedFooter(out io.Writer, shown, omitted, hidden int) error {
	var lines []string
	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("Showing the %d most recent of %d; -n N shows more, -n 0 shows all.", shown, shown+omitted))
	}
	switch {
	case hidden == 1:
		lines = append(lines, "1 automated session is hidden; --all includes it.")
	case hidden > 1:
		lines = append(lines, fmt.Sprintf("%d automated sessions are hidden; --all includes them.", hidden))
	}
	if len(lines) == 0 {
		return nil
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

// writeSessionTable is the session list for a person: a header, aligned
// columns, how long ago each conversation was last active, and each session
// ID cut to the prefix a continuity command still resolves. MESSAGES tells
// apart conversations that opened with the same prompt — a long session and
// one abandoned after a reply read alike otherwise. The summary is the
// harness's title for the conversation, else its opening prompt, followed by
// "→" and how the conversation ended, and takes whatever the terminal, width
// cells wide, leaves it; the title is held to two fifths of that so the
// ending, which is what differs between runs of the same prompt, stays in
// view. Long paths in it are cut to their last element, which is what tells
// apart sessions opened by one command on different plans or files. Pipes
// keep their fields as they were, so the count is a table column only.
func writeSessionTable(out io.Writer, views []sessionView, ids sessionIDAbbreviations, now time.Time, width int, liveKnown bool) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(out, "No indexed sessions match.")
		return err
	}
	header := []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "MESSAGES", "SESSION", "SUMMARY"}
	if liveKnown {
		header = []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "LIVE", "MESSAGES", "SESSION", "SUMMARY"}
	}
	home, _ := os.UserHomeDir()
	rows := make([][]string, 0, len(views))
	for _, item := range views {
		project := item.Project
		if project == "" {
			project = "-"
		}
		summary := oneLine(item.Title)
		if summary == "" {
			summary = oneLine(item.Snippet)
		}
		summary = transcript.ShortenSummary(summary, home)
		if summary == "" {
			summary = "-"
		}
		row := []string{item.Harness, item.Profile, project, formatAgo(item.LastAt, now)}
		if liveKnown {
			row = append(row, liveMark(item.Live))
		}
		rows = append(rows, append(row, strconv.Itoa(item.MessageCount), ids.of(item.ID), summary))
	}
	limit := cli.LastColumnWidth(header, rows, width, sessionSummaryWidth)
	for i, row := range rows {
		last := len(row) - 1
		row[last] = layout.FitLeadAndTail(row[last], oneLine(views[i].Ending), limit)
	}
	return cli.WriteTable(out, header, rows)
}

// liveMark is a list row's LIVE cell: a dot for a conversation open in a
// Kitty tab, where `session continue` focuses rather than resumes, and "here"
// for the one open in the window the command runs in, which is otherwise
// indistinguishable from its siblings when they share an opening prompt.
func liveMark(live *liveConversation) string {
	if live == nil {
		return ""
	}
	if live.Here {
		return "here"
	}
	return "●"
}

// sessionSummaryWidth bounds the table's last column when the terminal's width
// is unknown or leaves too little room. Several sessions of one project differ
// only by ID, and the opening prompt is what tells them apart; a fixed cut
// keeps a row on one line in an ordinary terminal.
const sessionSummaryWidth = 60

// fitLastColumn truncates each row's last cell to the room a terminal width
// cells wide leaves it, so a row stays on one line however wide the window.
func fitLastColumn(header []string, rows [][]string, width int) {
	limit := cli.LastColumnWidth(header, rows, width, sessionSummaryWidth)
	for _, row := range rows {
		last := len(row) - 1
		row[last] = layout.Truncate(row[last], limit, "…")
	}
}

// sessionIDDisplayMin is the shortest session ID a table prints. It is longer
// than sessionIDPrefixMin so a printed prefix rarely needs to grow when the
// index does.
const sessionIDDisplayMin = 8

// sessionIDAbbreviations maps each session ID to the shortest prefix, at least
// sessionIDDisplayMin characters, that resolveIndexedConversation reads back
// as that conversation alone. It is built from every visible indexed session
// across harnesses rather than from the rows on screen, because a pasted
// prefix is resolved against the whole index. An ID shared by two harnesses
// has no unique prefix and is printed whole. A nil value prints every ID
// whole.
type sessionIDAbbreviations map[string]string

func abbreviateSessionIDs(metas []transcript.SessionMeta) sessionIDAbbreviations {
	ids := make([]string, 0, len(metas))
	harnessOf := make(map[string]string, len(metas))
	shared := make(map[string]bool)
	for _, meta := range metas {
		ids = append(ids, meta.ID)
		if seen, ok := harnessOf[meta.ID]; ok && seen != meta.Harness {
			shared[meta.ID] = true
		}
		harnessOf[meta.ID] = meta.Harness
	}
	sort.Strings(ids)
	ids = slices.Compact(ids)
	out := make(sessionIDAbbreviations, len(ids))
	for i, id := range ids {
		if shared[id] {
			out[id] = id
			continue
		}
		common := 0
		if i > 0 {
			common = commonPrefixLen(id, ids[i-1])
		}
		if i+1 < len(ids) {
			common = max(common, commonPrefixLen(id, ids[i+1]))
		}
		out[id] = id[:min(len(id), max(sessionIDDisplayMin, common+1))]
	}
	return out
}

func (a sessionIDAbbreviations) of(id string) string {
	if short, ok := a[id]; ok {
		return short
	}
	return id
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}
