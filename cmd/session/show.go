package session

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		last, _ := cmd.Flags().GetInt("last")
		if last < 0 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("--last %d: want 0 (metadata only) or a positive count", last))
		}
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "show", args)
		if err != nil {
			return err
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
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
		if last > 0 {
			reader, ok := sessionCommandsSessionReaders()[meta.Harness]
			if !ok {
				return cli.CommandFailure(cmd, jsonOutput, "command_failed", fmt.Errorf("no transcript reader for harness %q", meta.Harness))
			}
			messages, err := reader.ReadMessages(meta.Path)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "command_failed", fmt.Errorf("reading %s: %w", meta.Path, err))
			}
			tail = lastConversationMessages(messages, last)
		}
		if jsonOutput {
			data := sessionShowData{Session: view}
			if tail != nil {
				data.Messages, data.MessagesTotal = tail.Messages, &tail.Total
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

type sessionShowData struct {
	Session sessionView `json:"session"`
	// Messages and MessagesTotal are present only under --last: the closing
	// user/assistant messages oldest first, and how many such messages the
	// conversation holds in all.
	Messages      []sessionMessage `json:"messages,omitempty"`
	MessagesTotal *int             `json:"messages_total,omitempty"`
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
	return &sessionMessageTail{Messages: kept, Total: total}
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
	if live.Here {
		return fmt.Sprintf("open in this Kitty window (%d)", live.WindowID)
	}
	return fmt.Sprintf("open in Kitty window %d (continue focuses it)", live.WindowID)
}
