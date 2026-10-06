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
// directory to cd into, the transcript to open).
var sessionShowCmd = &cobra.Command{
	Use:               "show <harness> <session-id> | <harness>:<session-id> | <session-id>",
	ValidArgsFunction: completeConversation,
	Short:             "Show one indexed conversation",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
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
		markLive(views, live)
		view := views[0]
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionShowData{Session: view})
		}
		return writeSessionShow(cmd.OutOrStdout(), view, sessionCommandsNow(), liveKnown)
	},
}

type sessionShowData struct {
	Session sessionView `json:"session"`
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
		{"profile", dash(strings.Join(view.Profiles, ", "))},
		{"project", dash(strings.Join(view.Projects, ", "))},
		{"directory", dash(view.CWD)},
		{"started", when(view.StartedAt)},
		{"active", when(view.LastAt)},
		{"live", liveLine(view.Live)},
		{"messages", strconv.Itoa(view.MessageCount)},
		{"transcript", dash(view.Path)},
		{"title", dash(oneLine(view.Title))},
		{"summary", dash(oneLine(view.Snippet))},
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

// liveLine is session show's live value: the Kitty window a conversation is
// open in, which continue focuses, or "no" when continue would resume it.
func liveLine(live *liveConversation) string {
	if live == nil {
		return "no"
	}
	return fmt.Sprintf("open in Kitty window %d (continue focuses it)", live.WindowID)
}
