package session

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/searchquery"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// sessionShowCmd prints what the index knows about one conversation. It
// resolves its selector exactly as continue and fork do — exact ID, unique
// prefix, harness-qualified or bare — so it is the way to check what one of
// those would act on, and its --json is the way to script around it (the
// directory to cd into, the transcript to open). --last N adds the closing
// user and assistant messages, which is usually what decides whether a
// conversation is the one to pick back up.
var sessionShowCmd = &cobra.Command{
	Use:               "show <harness> <session-id> | <harness>:<session-id> | <session-id>",
	ValidArgsFunction: completeConversation,
	Short:             "Show one indexed conversation",
	Long: `Shows what the transcript index knows about one conversation: its harness,
session ID, profile, project, working directory, transcript path, timestamps,
message count, and whether it is open right now. The conversation is
resolved exactly as ` + "`session continue`" + ` and ` + "`session fork`" + ` resolve it, so
show is the way to check what one of those would act on. A selector is one
of ` + conversationSelectorHelp + `.

Messages are read from the transcript only when asked for:

  --last N               the last N user and assistant messages;
  --find QUERY           a window of --count messages around the --match'th
                         message matching QUERY (session search's syntax);
  --from I               a window of --count messages starting at index I;
  --at TIME              a window around the last assistant message at or
                         before an RFC 3339 time.

With --json it reports {session, messages, messages_total, messages_start,
...}: session is a row as ` + "`session list --json`" + ` gives it, and the message
fields are present only when messages were read.`,
	Example: `  amx session show 3c1e9f0a
  amx session show claude:3c1e9f0a-7b2d-4e8f-9a1b-2c3d4e5f6a7b --last 4
  amx session show 3c1e9f0a --find "race condition" --count 20
  amx session show 3c1e9f0a --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		last, _ := cmd.Flags().GetInt("last")
		query, _ := cmd.Flags().GetString("find")
		match, _ := cmd.Flags().GetInt("match")
		from := int(sessionShowFrom)
		count, _ := cmd.Flags().GetInt("count")
		atText, _ := cmd.Flags().GetString("at")
		at, err := sessionShowMessageFlags(last, query, match, from, count, atText)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", err)
		}
		windowed := query != "" || from >= 0 || atText != ""
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "show", args)
		if err != nil {
			return err
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, sessionCommandsSessionReaders(), identity)
		if err != nil {
			return err
		}
		views := sessionViews([]transcript.SessionMeta{*meta})
		live, liveKnown := sessionCommandsLiveConversations()
		markLive(views, live, sessionCommandsCurrentWindow())
		view := views[0]
		var tail *sessionMessageTail
		if last > 0 || windowed {
			reader, ok := sessionCommandsSessionReaders()[meta.Harness]
			if !ok {
				return cli.CommandFailure(cmd, jsonOutput, "command_failed", fmt.Errorf("no transcript reader for harness %q", meta.Harness))
			}
			messages, err := reader.ReadMessages(meta.Path)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "command_failed", fmt.Errorf("reading %s: %w", meta.Path, err))
			}
			if atText != "" {
				tail = conversationMessageAt(messages, at, count)
			} else if windowed {
				tail = conversationMessageWindow(messages, query, match, from, count)
			} else {
				tail = lastConversationMessages(messages, last)
			}
		}
		if jsonOutput {
			data := sessionShowData{Session: view}
			if tail != nil {
				data.Messages, data.MessagesTotal = tail.Messages, &tail.Total
				data.MessagesStart = &tail.Start
				data.MatchMessages, data.MatchTerms, data.FocusMessage = tail.Matches, tail.Terms, tail.Focus
			}
			return cli.WriteJSON(cmd, data)
		}
		if err := writeSessionShow(cmd.OutOrStdout(), view, sessionCommandsNow(), liveKnown); err != nil {
			return err
		}
		if tail == nil {
			return nil
		}
		return writeSessionMessages(cmd.OutOrStdout(), *tail, sessionCommandsNow())
	},
}

// sessionShowMessageFlags checks the flags that choose which messages show
// reads, naming the one mistake made rather than restating every rule, and
// returns the --at time when one was given. --last reads the end alone;
// --find, --from, and --at each open a window of --count messages, and --at
// stands alone; --match picks among --find's matches.
func sessionShowMessageFlags(last int, query string, match, from, count int, atText string) (time.Time, error) {
	if last < 0 {
		return time.Time{}, fmt.Errorf("--last %d: want 0 (metadata only) or a positive count", last)
	}
	if from < -1 {
		return time.Time{}, fmt.Errorf("--from %d: want a message index of 0 or more", from)
	}
	if count < 1 || count > 500 {
		return time.Time{}, fmt.Errorf("--count must be from 1 to 500, got %d", count)
	}
	if match < 1 {
		return time.Time{}, fmt.Errorf("--match %d: want a match number of 1 or more", match)
	}
	if match != 1 && query == "" {
		return time.Time{}, errors.New("--match picks among the matches of --find, which was not given")
	}
	var at time.Time
	if atText != "" {
		var others []string
		for flag, set := range map[string]bool{"--find": query != "", "--from": from >= 0, "--last": last != 0} {
			if set {
				others = append(others, flag)
			}
		}
		if len(others) != 0 {
			slices.Sort(others)
			return time.Time{}, fmt.Errorf("--at cannot be combined with %s", strings.Join(others, " or "))
		}
		var err error
		if at, err = time.Parse(time.RFC3339Nano, atText); err != nil {
			return time.Time{}, fmt.Errorf("--at %q: want an RFC 3339 time, such as 2026-10-24T09:30:00Z", atText)
		}
	}
	if last != 0 && (query != "" || from >= 0) {
		return time.Time{}, errors.New("--last cannot be combined with --find or --from: it reads the end of the conversation, they open a window elsewhere")
	}
	return at, nil
}

type sessionShowData struct {
	Session sessionView `json:"session"`
	// Message fields are present only under --last, --find, --from, or --at: visible
	// user/assistant prose in chronological order, its total, and window start.
	Messages      []sessionMessage `json:"messages,omitempty"`
	MessagesTotal *int             `json:"messages_total,omitempty"`
	MessagesStart *int             `json:"messages_start,omitempty"`
	MatchMessages []int            `json:"match_messages,omitempty"`
	MatchTerms    []string         `json:"match_terms,omitempty"`
	FocusMessage  *int             `json:"focus_message,omitempty"`
}

// sessionMessage is one user or assistant message as show prints it. Tool
// payloads and hidden reasoning are left out by the same rule a handoff pack
// applies, so what is shown is what the two sides said to each other.
type sessionMessage struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp,omitzero"`
}

type sessionMessageTail struct {
	Messages []sessionMessage
	Total    int
	Start    int
	Matches  []int
	Terms    []string
	Focus    *int
}

// lastConversationMessages keeps the last n user/assistant messages, oldest
// first so they read in the order they were said.
func lastConversationMessages(messages []transcript.Message, n int) *sessionMessageTail {
	kept := make([]sessionMessage, 0, len(messages))
	for _, message := range messages {
		text, ok := message.TextForHandoff()
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		kept = append(kept, sessionMessage{
			Role:      strings.ToLower(strings.TrimSpace(message.Role)),
			Text:      strings.TrimSpace(text),
			Timestamp: message.Timestamp,
		})
	}
	total := len(kept)
	if n < total {
		kept = kept[total-n:]
	}
	return &sessionMessageTail{Messages: kept, Total: total, Start: total - len(kept)}
}

// Message ordinals refer only to visible prose, never hidden reasoning or tool
// payloads. The query terms are the same ones search and the TUI use.
func conversationMessageWindow(messages []transcript.Message, query string, match, from, count int) *sessionMessageTail {
	all := lastConversationMessages(messages, len(messages))
	all.Terms = searchquery.Parse(query)
	for i, message := range all.Messages {
		if searchquery.Terms(all.Terms).ContainsAny(strings.ToLower(message.Text)) {
			all.Matches = append(all.Matches, i)
		}
	}
	if len(all.Matches) > 0 {
		focus := all.Matches[min(match-1, len(all.Matches)-1)]
		all.Focus = &focus
	}
	if from < 0 {
		from = max(0, all.Total-count)
		if all.Focus != nil {
			from = max(0, *all.Focus-count/2)
		}
	}
	all.Start = min(from, all.Total)
	all.Messages = all.Messages[all.Start:min(all.Start+count, all.Total)]
	return all
}

// conversationMessageAt locates supporting assistant prose at or before an
// event. Missing timestamps and newer output cannot become evidence for it.
// Return a bounded window even when no timestamped message can be located;
// absence of focus_message tells clients to explain that limitation.
func conversationMessageAt(messages []transcript.Message, at time.Time, count int) *sessionMessageTail {
	all := lastConversationMessages(messages, len(messages))
	var latest time.Time
	for i, message := range all.Messages {
		if message.Role == "assistant" && !message.Timestamp.IsZero() && !message.Timestamp.After(at) && !message.Timestamp.Before(latest) {
			latest = message.Timestamp
			focus := i
			all.Focus = &focus
		}
	}
	all.Start = max(0, all.Total-count)
	if all.Focus != nil {
		all.Start = max(0, *all.Focus-count/2)
	}
	all.Messages = all.Messages[all.Start:min(all.Start+count, all.Total)]
	return all
}

// writeSessionMessages prints the tail under the metadata: a heading that says
// how much of the conversation it is, then each message whole behind a role
// and time line. Messages are not truncated; asking for them is asking to
// read them.
func writeSessionMessages(out io.Writer, tail sessionMessageTail, now time.Time) error {
	var b strings.Builder
	switch {
	case tail.Total == 0:
		b.WriteString("\nno user or assistant messages to show\n")
	case len(tail.Messages) == tail.Total:
		fmt.Fprintf(&b, "\nall %d user and assistant %s:\n", tail.Total, plural("message", tail.Total))
	case tail.Start+len(tail.Messages) < tail.Total:
		fmt.Fprintf(&b, "\nmessages %d–%d of %d user and assistant messages:\n", tail.Start+1, tail.Start+len(tail.Messages), tail.Total)
	default:
		fmt.Fprintf(&b, "\nlast %d of %d user and assistant messages:\n", len(tail.Messages), tail.Total)
	}
	for _, message := range tail.Messages {
		fmt.Fprintf(&b, "\n[%s]", message.Role)
		if !message.Timestamp.IsZero() {
			fmt.Fprintf(&b, " %s (%s)", formatTime(message.Timestamp), formatAgo(message.Timestamp, now))
		}
		b.WriteString("\n" + message.Text + "\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// writeSessionShow lays the conversation out as labelled lines. Paths are
// printed whole rather than with ~, so each can be pasted anywhere, and the
// last lines are the commands that pick the conversation back up or branch
// from it, spelled with its exact harness-qualified identity so they cannot
// become ambiguous later. The live line, which says whether continue would
// focus a tab or resume, is left out when the daemon or Kitty could not say.
func writeSessionShow(out io.Writer, view sessionView, now time.Time, liveKnown bool) error {
	dash := func(value string) string {
		if value == "" {
			return "-"
		}
		return value
	}
	when := func(at time.Time) string {
		if at.IsZero() {
			return "-"
		}
		return formatTime(at) + " (" + formatAgo(at, now) + ")"
	}
	rows := [][2]string{
		{"session", view.Harness + ":" + view.ID},
		{plural("profile", len(view.Profiles)), dash(strings.Join(view.Profiles, ", "))},
		{plural("project", len(view.Projects)), dash(strings.Join(view.Projects, ", "))},
		{"directory", dash(view.CWD)},
		{"started", when(view.StartedAt)},
		{"active", when(view.LastAt)},
		{"live", liveLine(view.Live)},
		{"messages", strconv.Itoa(view.MessageCount)},
		{"transcript", dash(view.Path)},
		{"title", dash(oneLine(view.Title))},
		{"summary", dash(oneLine(view.Snippet))},
		{"ended", dash(oneLine(view.Ending))},
		{"continue", "amx session continue " + view.Harness + ":" + view.ID},
		{"fork", "amx session fork " + view.Harness + ":" + view.ID},
	}
	if !liveKnown {
		rows = slices.DeleteFunc(rows, func(row [2]string) bool { return row[0] == "live" })
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%-11s %s\n", row[0]+":", row[1])
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// plural is a field label for count values: a conversation two profiles share
// lists both under "profiles". The labels stay within the column the widest
// one, "transcript", sets.
func plural(label string, count int) string {
	if count > 1 {
		return label + "s"
	}
	return label
}

// liveLine is session show's live value: the Kitty window a conversation is
// open in, which continue focuses, or "no" when continue would resume it.
func liveLine(live *liveConversation) string {
	if live == nil {
		return "no"
	}
	if live.RuntimeSessionID != "" {
		if live.Here {
			return fmt.Sprintf("open in this session (%s)", live.RuntimeSessionID)
		}
		return fmt.Sprintf("open in tmux session %s (amx attach shows it)", live.RuntimeSessionID)
	}
	if live.Here {
		return fmt.Sprintf("open in this Kitty window (%d)", live.WindowID)
	}
	return fmt.Sprintf("open in Kitty window %d (continue focuses it)", live.WindowID)
}

// sessionShowFrom is --from. It is unset (-1) by default, which --help does
// not print: "(default -1)" reads as an index.
var sessionShowFrom messageIndex = -1

// messageIndex is a message index flag that may be unset. Unset prints as ""
// and setting "" unsets it, so a flag reset to its default unsets it too.
type messageIndex int

func (m *messageIndex) String() string {
	if *m < 0 {
		return ""
	}
	return strconv.Itoa(int(*m))
}

func (m *messageIndex) Set(value string) error {
	if value == "" {
		*m = -1
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return errors.New("want a whole number")
	}
	*m = messageIndex(n)
	return nil
}

func (m *messageIndex) Type() string { return "int" }
